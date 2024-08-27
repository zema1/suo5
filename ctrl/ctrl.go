package ctrl

import (
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
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-gost/gosocks5"
	"github.com/go-gost/gosocks5/client"
	"github.com/go-gost/gosocks5/server"
	log "github.com/kataras/golog"
	"github.com/kataras/pio"
	"github.com/pkg/errors"
	utls "github.com/refraction-networking/utls"
	"github.com/zema1/rawhttp"
	"github.com/zema1/suo5/netrans"
)

var rander = rand.New(rand.NewSource(time.Now().UnixNano()))

func Run(ctx context.Context, config *Suo5Config) error {
	if config.GuiLog != nil {
		// 防止多次执行出错
		log.Default = log.New()
		log.Default.AddOutput(config.GuiLog)
	}
	if config.Debug {
		log.SetLevel("debug")
	}

	err := config.Parse()
	if err != nil {
		return err
	}
	if config.DisableGzip {
		log.Infof("disable gzip")
		config.Header.Set("Accept-Encoding", "identity")
	}

	if len(config.ExcludeDomain) != 0 {
		log.Infof("exclude domains: %v", config.ExcludeDomain)
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS10,
			Renegotiation:      tls.RenegotiateOnceAsClient,
			InsecureSkipVerify: true,
		},
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := net.DialTimeout(network, addr, 5*time.Second)
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
	if config.UpstreamProxy != "" {
		proxy := strings.TrimSpace(config.UpstreamProxy)
		if !strings.HasPrefix(proxy, "socks5") && !strings.HasPrefix(proxy, "http") {
			return fmt.Errorf("invalid proxy, both socks5 and http(s) are supported, eg: socks5://127.0.0.1:1080")
		}
		config.UpstreamProxy = proxy
		u, err := url.Parse(config.UpstreamProxy)
		if err != nil {
			return err
		}
		log.Infof("using upstream proxy %v", proxy)
		tr.Proxy = http.ProxyURL(u)
	}
	if config.RedirectURL != "" {
		_, err := url.Parse(config.RedirectURL)
		if err != nil {
			return fmt.Errorf("failed to parse redirect url, %s", err)
		}
		log.Infof("using redirect url %v", config.RedirectURL)
	}
	var jar http.CookieJar
	if !config.DisableCookiejar {
		jar, _ = cookiejar.New(nil)
	}

	noTimeoutClient := &http.Client{
		Transport: tr.Clone(),
		Jar:       jar,
		Timeout:   0,
	}
	normalClient := &http.Client{
		Timeout:   time.Duration(config.Timeout) * time.Second,
		Jar:       jar,
		Transport: tr.Clone(),
	}
	rawClient := newRawClient(config.UpstreamProxy, 0)

	log.Infof("header: %s", config.HeaderString())
	log.Infof("method: %s", config.Method)
	log.Infof("connecting to target %s", config.Target)
	result, offset, err := checkConnectMode(config)
	if err != nil {
		return err
	}
	if config.Mode == AutoDuplex {
		config.Mode = result
		if result == FullDuplex {
			log.Infof("wow, you can run the proxy on FullDuplex mode")
		} else {
			log.Warnf("the target may behind a reverse proxy, fallback to HalfDuplex mode")
		}
	} else {
		if result == FullDuplex && config.Mode == HalfDuplex {
			log.Infof("the target support full duplex, you can try FullDuplex mode to obtain better performance")
		} else if result == HalfDuplex && config.Mode == FullDuplex {
			return fmt.Errorf("the target doesn't support full duplex, you should use HalfDuplex or AutoDuplex mode")
		}
	}
	config.Offset = offset

	log.Infof("starting tunnel at %s", config.Listen)
	if config.OnRemoteConnected != nil {
		config.OnRemoteConnected(&ConnectedEvent{Mode: config.Mode})
	}

	fmt.Println()
	var socks5Addr string
	msg := "[Tunnel Info]\n"
	msg += fmt.Sprintf("Target:  %s\n", config.Target)
	if config.NoAuth {
		socks5Addr = fmt.Sprintf("socks5://%s", config.Listen)
	} else {
		socks5Addr = fmt.Sprintf("socks5://%s:%s@%s", config.Username, config.Password, config.Listen)
	}
	msg += fmt.Sprintf("Proxy:   %s\n", socks5Addr)
	msg += fmt.Sprintf("Mode:    %s\n", config.Mode)
	fmt.Println(pio.Rich(msg, pio.Green))

	lis, err := net.Listen("tcp", config.Listen)
	if err != nil {
		return err
	}
	// context cancel, close the listen
	srv := &server.Server{
		Listener: lis,
	}
	go func() {
		<-ctx.Done()
		log.Infof("server stopped")
		_ = srv.Close()
	}()

	trPool := &sync.Pool{
		New: func() interface{} {
			return make([]byte, config.BufferSize)
		},
	}
	selector := server.DefaultSelector
	if !config.NoAuth {
		selector = server.NewServerSelector([]*url.Userinfo{
			url.UserPassword(config.Username, config.Password),
		})
	}

	handler := &socks5Handler{
		ctx:             ctx,
		config:          config,
		normalClient:    normalClient,
		noTimeoutClient: noTimeoutClient,
		rawClient:       rawClient,
		pool:            trPool,
		selector:        selector,
	}

	go func() {
		_ = srv.Serve(&ClientEventHandler{
			Inner:                   handler,
			OnNewClientConnection:   config.OnNewClientConnection,
			OnClientConnectionClose: config.OnClientConnectionClose,
		})
	}()
	log.Infof("creating a test connection to the remote target")
	ok := testTunnel(config.Listen, config.Username, config.Password, time.Second*10)
	time.Sleep(time.Millisecond * 500)
	if !ok {
		log.Errorf("tunnel created, but failed to establish connection")
		return fmt.Errorf("suo5 can not work on this server")
	} else {
		log.Infof("congratulations! everything works fine")
	}

	if config.TestExit != "" {
		if err := testAndExit(socks5Addr, config.TestExit, time.Second*15); err != nil {
			return errors.Wrap(err, "test connection failed")
		}
		return nil
	}

	<-ctx.Done()
	return nil
}

func checkConnectMode(config *Suo5Config) (ConnectionType, int, error) {
	// 这里的 client 需要定义 timeout，不要用外面没有 timeout 的 rawCient
	rawClient := newRawClient(config.UpstreamProxy, time.Second*5)
	randLen := rander.Intn(1024)
	if randLen <= 32 {
		randLen += 32
	}
	data := RandString(randLen)
	ch := make(chan []byte, 1)
	ch <- []byte(data)
	req, err := http.NewRequest(config.Method, config.Target, netrans.NewChannelReader(ch))
	if err != nil {
		return Undefined, 0, err
	}
	req.Header = config.Header.Clone()
	req.Header.Set(HeaderKey, HeaderValueChecking)

	now := time.Now()
	go func() {
		// timeout
		time.Sleep(time.Second * 3)
		close(ch)
	}()
	resp, err := rawClient.Do(req)
	if err != nil {
		return Undefined, 0, err
	}
	defer resp.Body.Close()

	// 如果独到响应的时间在3s内，说明请求没有被缓存, 那么就可以变成全双工的
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		// 这里不要直接返回，有时虽然 eof 了但是数据是对的，可以使用
		log.Warnf("got error %s", err)
	}
	duration := time.Since(now).Milliseconds()

	offset := strings.Index(string(body), data[:32])
	if offset == -1 {
		header, _ := httputil.DumpResponse(resp, false)
		log.Errorf("response are as follows:\n%s", string(header)+string(body))
		return Undefined, 0, fmt.Errorf("got unexpected body, remote server test failed")
	}
	log.Infof("got data offset, %d", offset)

	if duration < 3000 {
		return FullDuplex, offset, nil
	} else {
		return HalfDuplex, offset, nil
	}
}

// 检查代理是否真正有效, 只要能按有响应即可，尝试连一下 server 的 LocalPort, 这里写 0，在 jsp 里有判断
func testTunnel(socks5, username, password string, timeout time.Duration) bool {
	addr, _ := gosocks5.NewAddr("127.0.0.1:0")
	options := []client.DialOption{client.TimeoutDialOption(timeout)}
	if username != "" && password != "" {
		options = append(options, client.SelectorDialOption(client.NewClientSelector(url.UserPassword(username, password))))
	}

	conn, err := client.Dial(socks5, options...)
	if err != nil {
		log.Error(err)
		return false
	}
	defer conn.Close()
	if err := gosocks5.NewRequest(gosocks5.CmdConnect, addr).Write(conn); err != nil {
		log.Error(err)
		return false
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout))

	reply, err := gosocks5.ReadReply(conn)
	if err != nil {
		log.Error(err)
		return false
	}
	log.Debugf("recv socks5 reply: %d", reply.Rep)
	return reply.Rep == gosocks5.Succeeded || reply.Rep == gosocks5.ConnRefused
}

func testAndExit(socks5 string, remote string, timeout time.Duration) error {
	log.Infof("checking connection to %s using %s", remote, socks5)
	u, err := url.Parse(socks5)
	if err != nil {
		return err
	}
	httpClient := http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(u),
		},
	}
	req, err := http.NewRequest(http.MethodGet, remote, nil)
	if err != nil {
		return err
	}
	req.Close = true
	resp, err := httpClient.Do(req)
	if err != nil {
		if os.IsTimeout(err) {
			return err
		}
		log.Infof("test connection got error, but it's ok, %s", err)
		return nil
	}
	defer resp.Body.Close()
	data, err := httputil.DumpResponse(resp, false)
	if err != nil {
		log.Debugf("test connection got error when read response,  %s, but it's ok", err)
		return nil
	}
	log.Debugf("test connection got response for %s (without body)\n%s", remote, string(data))
	return nil
}

const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func RandString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[rander.Intn(len(letterBytes))]
	}
	return string(b)
}

func newRawClient(upstream string, timeout time.Duration) *rawhttp.Client {
	return rawhttp.NewClient(&rawhttp.Options{
		Proxy:                  upstream,
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
	})

}
