package suo5

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/proxyclient"
	log "github.com/kataras/golog"
	utls "github.com/refraction-networking/utls"
	"github.com/zema1/rawhttp"
	"github.com/zema1/suo5/netrans"
)

type Suo5Client struct {
	StreamFactory
	Config *Suo5Config

	ctx       context.Context
	speeder   *netrans.SpeedCaculator
	bytesPool *sync.Pool
}

func Connect(ctx context.Context, config *Suo5Config) (*Suo5Client, error) {
	err := config.Parse()
	if err != nil {
		return nil, err
	}

	log.Infof("header: %s", config.HeaderString())
	log.Infof("connecting to target %s", config.GetTarget())

	if config.DisableGzip {
		log.Infof("disable gzip")
		config.Header.Set("Accept-Encoding", "identity")
	}

	if len(config.ExcludeDomain) != 0 {
		log.Infof("exclude domains: %v", config.ExcludeDomain)
	}

	if config.RedirectURL != "" {
		_, err := url.Parse(config.RedirectURL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse redirect url, %s", err)
		}
		log.Infof("redirect traffic to %v", config.RedirectURL)
	}

	if config.RetryCount != 0 {
		log.Infof("request max retry: %d", config.RetryCount)
	}

	if config.Mode != AutoDuplex {
		log.Infof("preferred connection mode: %s", config.Mode)
	}

	if len(config.UpstreamProxy) != 0 {
		log.Infof("using upstream proxy: %s", strings.Join(config.UpstreamProxy, " -> "))
	}

	var jar http.CookieJar
	if config.EnableCookieJar {
		jar, _ = cookiejar.New(nil)
	} else {
		// 对 PHP的特殊处理一下, 如果是 PHP 的站点则自动启用 cookiejar, 其他站点保持不启用
		jar = NewSwitchableCookieJar([]string{"PHPSESSID"})
	}

	tr, err := NewHttpTransport(config.UpstreamProxy, config.TimeoutTime())
	if err != nil {
		return nil, err
	}

	rawClient, err := NewRawHttpClient(config.UpstreamProxy, config.TimeoutTime(), 0)
	if err != nil {
		return nil, err
	}

	retry := config.RetryCount

	for i := 0; i <= retry; i++ {
		err = checkConnectMode(ctx, config, jar)
		if err != nil {
			if i == retry {
				return nil, fmt.Errorf("handshake failed after %d retries", retry)
			} else {
				log.Errorf("handshake failed: %s, retrying %d/%d", err, i+1, retry)
			}
		} else {
			log.Infof("suo5 is going to work on %s mode", config.Mode)
			break
		}
	}

	var factory StreamFactory
	switch config.Mode {
	case FullDuplex:
		factory = NewFullChunkedStreamFactory(ctx, config, rawClient)

	case HalfDuplex:
		noTimeoutClient := &http.Client{
			Transport: tr.Clone(),
			Jar:       jar,
			Timeout:   0,
		}
		factory = NewHalfChunkedStreamFactory(ctx, config, noTimeoutClient)

	case Classic:
		normalClient := &http.Client{
			Timeout:   config.TimeoutTime(),
			Jar:       jar,
			Transport: tr.Clone(),
		}
		factory = NewClassicStreamFactory(ctx, config, normalClient)

	default:
		return nil, fmt.Errorf("unknown mode %s", config.Mode)
	}

	speeder := netrans.NewSpeedCaculator()
	if config.OnSpeedInfo != nil {
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					up, down := speeder.Statistic()
					config.OnSpeedInfo(&SpeedStatisticEvent{
						Upload:   up,
						Download: down,
					})
				}
			}
		}()
	}

	pool := &sync.Pool{
		New: func() interface{} {
			return make([]byte, DefaultBufferSize)
		},
	}

	suo5Client := &Suo5Client{
		StreamFactory: factory,
		ctx:           ctx,
		Config:        config,
		speeder:       speeder,
		bytesPool:     pool,
	}

	return suo5Client, nil
}

func checkConnectMode(ctx context.Context, config *Suo5Config, jar http.CookieJar) error {

	randLen := rand.Intn(4096)
	if randLen <= 32 {
		randLen += 32
	}
	identifier := RandString(randLen)
	actionData := NewActionData(RandString(8), []byte(identifier))

	now := time.Now()
	var resp *http.Response
	if config.Mode == AutoDuplex || config.Mode == FullDuplex {
		actionData["a"] = []byte{0x01}
		data := BuildBody(actionData, config.RedirectURL, config.SessionId, Checking)
		ch := make(chan []byte, 1)
		ch <- data

		req := config.NewRequest(ctx, netrans.NewChannelReader(ch), -1)

		go func() {
			// no need to use stream in classic mode
			time.Sleep(time.Second * 3)
			close(ch)
		}()

		// 这里的 client 需要定义 timeout，不要用外面没有 timeout 的 rawClient
		timeout := config.TimeoutTime()
		if timeout < 6*time.Second {
			timeout = 6 * time.Second
		}
		rawClient, err := NewRawHttpClient(config.UpstreamProxy, timeout, timeout)
		if err != nil {
			return err
		}
		resp, err = rawClient.Do(req)
		if err != nil {
			return err
		}

	} else {
		actionData["a"] = []byte{0x00}
		data := BuildBody(actionData, config.RedirectURL, config.SessionId, Checking)
		req := config.NewRequest(ctx, bytes.NewReader(data), int64(len(data)))

		tr, err := NewHttpTransport(config.UpstreamProxy, config.TimeoutTime())
		if err != nil {
			return err
		}
		normalClient := &http.Client{
			Timeout:   config.TimeoutTime(),
			Transport: tr,
		}
		resp, err = normalClient.Do(req)
		if err != nil {
			return err
		}
	}

	cookies := resp.Cookies()
	if len(cookies) != 0 {
		u, err := url.Parse(config.GetTarget())
		if err == nil {
			jar.SetCookies(u, cookies)
			log.Infof("handling cookies: %s", resp.Header.Get("Set-Cookie"))
		}
	}

	defer resp.Body.Close()

	// 如果独到响应的时间在3s内，说明请求没有被缓存, 那么就可以变成全双工的
	// 如果响应时间在 3~6s 之间，说明请求被缓存了， 但响应仍然是流式的, 但是可以使用半双工
	// 否则只能用短链接了
	bodyReader := netrans.OffsetReader(resp.Body, int64(config.Offset))
	echoData, bufData, err := UnmarshalFrameWithBuffer(bodyReader)
	if err != nil {
		// 这里不要直接返回，有时虽然 eof 了但是数据是对的，可以使用
		// todo: why listener eof
		bodyData, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		bufData = append(bufData, bodyData...)
		header, _ := httputil.DumpResponse(resp, false)
		log.Errorf("response are as follows:\n%s", string(header)+string(bufData))
		return fmt.Errorf("got unexpected body, remote server test failed")
	}
	duration := time.Since(now).Milliseconds()

	// ignore bodyData
	sessionData, _, err := UnmarshalFrameWithBuffer(bodyReader)
	if err != nil {
		return err
	}

	if !strings.EqualFold(string(echoData["dt"]), identifier) {
		header, _ := httputil.DumpResponse(resp, false)
		log.Errorf("response are as follows:\n%s", string(header)+string(bufData))
		return fmt.Errorf("got unexpected body, remote server test failed")
	}

	sid := string(sessionData["dt"])
	config.SessionId = sid
	log.Infof("handshake success, using session id %s", sid)

	if config.Mode == AutoDuplex {
		log.Infof("handshake duration: %d ms", duration)
		if duration < 3000 {
			config.Mode = FullDuplex
		} else if duration < 5000 {
			config.Mode = HalfDuplex
		} else {
			config.Mode = Classic
		}
	}
	return nil
}

func (m *Suo5Client) Pipe(r io.Reader, w io.Writer) error {
	buf := m.bytesPool.Get().([]byte)
	defer m.bytesPool.Put(buf) //nolint:staticcheck
	for {
		n, err := r.Read(buf)
		if err != nil {
			return err
		}
		_, err = w.Write(buf[:n])
		if err != nil {
			return err
		}
	}
}

func (m *Suo5Client) DualPipe(localConn, remoteWrapper io.ReadWriteCloser, addr string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		defer remoteWrapper.Close()
		if err := m.Pipe(localConn, remoteWrapper); err != nil {
			log.Debugf("local conn closed, %s", addr)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		defer localConn.Close()
		if err := m.Pipe(remoteWrapper, localConn); err != nil {
			log.Debugf("remote readwriter closed, %s", addr)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		calc := func() {
			speeder, ok := remoteWrapper.(*netrans.SpeedTrackingReadWriterCloser)
			if ok {
				up, down := speeder.GetSpeedInterval()
				m.speeder.AddSpeed(up, down)
			}
		}

		// 退出前把数据上报一次
		defer calc()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				calc()
			}
		}
	}()
	wg.Wait()
}

func GetDialFunc(upstreamProxies []string) (rawhttp.ContextDialFunc, error) {
	var dialMethod rawhttp.ContextDialFunc
	if len(upstreamProxies) > 0 {
		proxies, err := proxyclient.ParseProxyURLs(upstreamProxies)
		if err != nil {
			return nil, err
		}

		clientChain, err := proxyclient.NewClientChain(proxies)
		if err != nil {
			return nil, err
		}
		dialMethod = clientChain.DialContext
	} else {
		dialMethod = (&net.Dialer{}).DialContext
	}
	return dialMethod, nil
}

func NewRawHttpClient(upstreamProxies []string, dialTimeout, timeout time.Duration) (*rawhttp.Client, error) {
	dialMethod, err := GetDialFunc(upstreamProxies)
	if err != nil {
		return nil, err
	}
	return rawhttp.NewClient(&rawhttp.Options{
		Proxy:                  dialMethod,
		ProxyDialTimeout:       dialTimeout,
		Timeout:                timeout,
		FollowRedirects:        false,
		MaxRedirects:           0,
		AutomaticHostHeader:    true,
		AutomaticContentLength: true,
		ForceReadAllBody:       false,
		TLSHandshake: func(conn net.Conn, addr string, options *rawhttp.Options) (net.Conn, error) {
			colonPos := strings.LastIndex(addr, ":")
			if colonPos == -1 {
				colonPos = len(addr)
			}
			hostname := addr[:colonPos]
			uTlsConn := utls.UClient(conn, &utls.Config{
				InsecureSkipVerify: true,
				ServerName:         hostname,
				MinVersion:         utls.VersionTLS10,
				Renegotiation:      utls.RenegotiateOnceAsClient,
			}, utls.HelloRandomizedNoALPN)
			if err := uTlsConn.Handshake(); err != nil {
				_ = conn.Close()
				return nil, err
			}
			return uTlsConn, nil
		},
	}), nil

}

func NewHttpTransport(upstreamProxies []string, timeout time.Duration) (*http.Transport, error) {
	dialFunc, err := GetDialFunc(upstreamProxies)
	if err != nil {
		return nil, err
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS10,
			Renegotiation:      tls.RenegotiateOnceAsClient,
			InsecureSkipVerify: true,
		},
		DialContext: dialFunc,
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			conn, err := dialFunc(ctx, network, addr)
			if err != nil {
				return nil, err
			}

			colonPos := strings.LastIndex(addr, ":")
			if colonPos == -1 {
				colonPos = len(addr)
			}
			hostname := addr[:colonPos]
			tlsConfig := &utls.Config{
				ServerName:         hostname,
				InsecureSkipVerify: true,
				Renegotiation:      utls.RenegotiateOnceAsClient,
				MinVersion:         utls.VersionTLS10,
			}
			uTlsConn := utls.UClient(conn, tlsConfig, utls.HelloRandomizedNoALPN)
			if err = uTlsConn.HandshakeContext(ctx); err != nil {
				_ = conn.Close()
				return nil, err
			}
			return uTlsConn, nil
		},
	}
	return tr, nil
}
