package suo5

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/chainreactors/proxyclient"
	"github.com/gobwas/glob"
	log "github.com/kataras/golog"
	utls "github.com/refraction-networking/utls"
	"github.com/zema1/rawhttp"
	"github.com/zema1/suo5/netrans"
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
)

var (
	DefaultMaxRequestSize = 1024 * 1024
	DefaultBufferSize     = 1024 * 64
	DefaultTimeout        = 5
	DefaultClassicPollQPS = 5
	DefaultRetryCount     = 1
)

type Suo5Config struct {
	Method           string         `json:"method"`
	Listen           string         `json:"listen"`
	Target           string         `json:"target"`
	Username         string         `json:"username"`
	Password         string         `json:"password"`
	Mode             ConnectionType `json:"mode"`
	BufferSize       int            `json:"buffer_size"`
	Timeout          int            `json:"timeout"`
	Debug            bool           `json:"debug"`
	UpstreamProxy    []string       `json:"upstream_proxy"`
	RedirectURL      string         `json:"redirect_url"`
	RawHeader        []string       `json:"raw_header"`
	DisableHeartbeat bool           `json:"disable_heartbeat"`
	DisableGzip      bool           `json:"disable_gzip"`
	EnableCookieJar  bool           `json:"enable_cookiejar"`
	ExcludeDomain    []string       `json:"exclude_domain"`
	ForwardTarget    string         `json:"forward_target"`
	MaxRequestSize   int            `json:"max_request_size"`
	ClassicPollQPS   int            `json:"classic_poll_qps"`
	RetryCount       int            `json:"retry_count"`

	SessionId               string                               `json:"-"`
	TestExit                string                               `json:"-"`
	ExcludeGlobs            []glob.Glob                          `json:"-"`
	Offset                  int                                  `json:"-"`
	Header                  http.Header                          `json:"-"`
	ProxyClient             proxyclient.Dial                     `json:"-"`
	OnRemoteConnected       func(e *ConnectedEvent)              `json:"-"`
	OnNewClientConnection   func(event *ClientConnectionEvent)   `json:"-"`
	OnClientConnectionClose func(event *ClientConnectCloseEvent) `json:"-"`
	OnSpeedInfo             func(event *SpeedStatisticEvent)     `json:"-"`
	GuiLog                  io.Writer                            `json:"-"`
}

func DefaultSuo5Config() *Suo5Config {
	return &Suo5Config{
		Method:           http.MethodPost,
		Listen:           "127.0.0.1:1111",
		Target:           "",
		Username:         "",
		Password:         "",
		Mode:             AutoDuplex,
		BufferSize:       DefaultBufferSize,
		Timeout:          DefaultTimeout,
		Debug:            false,
		UpstreamProxy:    []string{},
		RedirectURL:      "",
		RawHeader:        []string{},
		DisableHeartbeat: false,
		EnableCookieJar:  false,
		ForwardTarget:    "",
		MaxRequestSize:   DefaultMaxRequestSize,
		ClassicPollQPS:   DefaultClassicPollQPS,
		RetryCount:       DefaultRetryCount,
	}
}

func (conf *Suo5Config) Parse() error {
	if conf.Timeout <= 0 {
		conf.Timeout = DefaultTimeout
	}
	if conf.BufferSize <= 0 {
		conf.BufferSize = DefaultBufferSize
	}
	if conf.MaxRequestSize <= 0 {
		conf.MaxRequestSize = DefaultMaxRequestSize
	}

	if conf.ClassicPollQPS <= 0 {
		conf.ClassicPollQPS = DefaultClassicPollQPS
	}

	if !(conf.Mode == AutoDuplex || conf.Mode == FullDuplex || conf.Mode == HalfDuplex || conf.Mode == Classic) {
		return fmt.Errorf("invalid mode, expected auto or full or half or classic")
	}

	if err := conf.parseExcludeDomain(); err != nil {
		return err
	}
	return conf.parseHeader()
}

func (conf *Suo5Config) parseExcludeDomain() error {
	conf.ExcludeGlobs = make([]glob.Glob, 0)
	for _, domain := range conf.ExcludeDomain {
		g, err := glob.Compile(domain)
		if err != nil {
			return err
		}
		conf.ExcludeGlobs = append(conf.ExcludeGlobs, g)
	}
	return nil
}

func (conf *Suo5Config) parseHeader() error {
	conf.Header = make(http.Header)
	for _, value := range conf.RawHeader {
		if value == "" {
			continue
		}
		parts := strings.SplitN(value, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid header value %s", value)
		}
		conf.Header.Set(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
	}

	if conf.Header.Get("Referer") == "" {
		n := strings.LastIndex(conf.Target, "/")
		if n == -1 {
			conf.Header.Set("Referer", conf.Target)
		} else {
			conf.Header.Set("Referer", conf.Target[:n+1])
		}
	}

	return nil
}

func (conf *Suo5Config) Init(ctx context.Context) (*Suo5Client, error) {
	err := conf.Parse()
	if err != nil {
		return nil, err
	}

	log.Infof("method: %s", conf.Method)
	log.Infof("header: %s", conf.HeaderString())
	log.Infof("connecting to target %s", conf.Target)

	if conf.DisableGzip {
		log.Infof("disable gzip")
		conf.Header.Set("Accept-Encoding", "identity")
	}

	if len(conf.ExcludeDomain) != 0 {
		log.Infof("exclude domains: %v", conf.ExcludeDomain)
	}

	if conf.RedirectURL != "" {
		_, err := url.Parse(conf.RedirectURL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse redirect url, %s", err)
		}
		log.Infof("redirect traffic to %v", conf.RedirectURL)
	}

	if conf.RetryCount != 0 {
		log.Infof("request max retry: %d", conf.RetryCount)
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
	if len(conf.UpstreamProxy) > 0 {
		proxies, err := proxyclient.ParseProxyURLs(conf.UpstreamProxy)
		if err != nil {
			return nil, err
		}
		log.Infof("using upstream proxy %v", proxies)

		conf.ProxyClient, err = proxyclient.NewClientChain(proxies)
		if err != nil {
			return nil, err
		}
		tr.DialContext = conf.ProxyClient.DialContext
	}

	var jar http.CookieJar
	if conf.EnableCookieJar {
		jar, _ = cookiejar.New(nil)
	} else {
		// 对 PHP的特殊处理一下, 如果是 PHP 的站点则自动启用 cookiejar, 其他站点保持不启用
		jar = NewSwitchableCookieJar([]string{"PHPSESSID"})
	}

	noTimeoutClient := &http.Client{
		Transport: tr.Clone(),
		Jar:       jar,
		Timeout:   0,
	}
	normalClient := &http.Client{
		Timeout:   conf.TimeoutTime(),
		Jar:       jar,
		Transport: tr.Clone(),
	}

	var rawClient *rawhttp.Client
	if conf.ProxyClient != nil {
		rawClient = newRawClient(conf.ProxyClient.DialContext, 0)
	} else {
		rawClient = newRawClient(nil, 0)
	}

	err = conf.CheckConnectMode(ctx)
	if err != nil {
		return nil, err
	}
	log.Infof("suo5 is going to work in %s mode", conf.Mode)

	var factory StreamFactory
	if conf.Mode == FullDuplex {
		factory = NewFullChunkedStreamFactory(ctx, conf, rawClient)
	} else if conf.Mode == HalfDuplex {
		factory = NewHalfChunkedStreamFactory(ctx, conf, noTimeoutClient)
	} else if conf.Mode == Classic {
		factory = NewClassicStreamFactory(ctx, conf, normalClient)
	} else {
		return nil, fmt.Errorf("unknown mode %s", conf.Mode)
	}

	speeder := netrans.NewSpeedCaculator()
	if conf.OnSpeedInfo != nil {
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					up, down := speeder.Statistic()
					conf.OnSpeedInfo(&SpeedStatisticEvent{
						Upload:   up,
						Download: down,
					})
				}
			}
		}()
	}

	pool := &sync.Pool{
		New: func() interface{} {
			return make([]byte, conf.BufferSize)
		},
	}

	return &Suo5Client{
		Config:          conf,
		NormalClient:    normalClient,
		NoTimeoutClient: noTimeoutClient,
		RawClient:       rawClient,
		Factory:         factory,
		Speeder:         speeder,
		BytesPool:       pool,
	}, nil
}

func (conf *Suo5Config) HeaderString() string {
	ret := ""
	for k := range conf.Header {
		ret += fmt.Sprintf("\n%s: %s", k, conf.Header.Get(k))
	}
	return ret
}

func (conf *Suo5Config) NoAuth() bool {
	return conf.Username == "" && conf.Password == ""
}

func (conf *Suo5Config) TimeoutTime() time.Duration {
	return time.Duration(conf.Timeout) * time.Second
}

func (conf *Suo5Config) CheckConnectMode(ctx context.Context) error {
	// 这里的 client 需要定义 timeout，不要用外面没有 timeout 的 rawClient
	var rawClient *rawhttp.Client
	timeout := conf.TimeoutTime()
	if timeout < 6*time.Second {
		timeout = 6 * time.Second
	}

	if conf.ProxyClient != nil {
		rawClient = newRawClient(conf.ProxyClient.DialContext, timeout)
	} else {
		rawClient = newRawClient(nil, timeout)
	}
	randLen := rand.Intn(4096)
	if randLen <= 32 {
		randLen += 32
	}
	identifier := RandString(randLen)
	actionData := NewActionData(RandString(8), []byte(identifier))
	if conf.Mode == AutoDuplex {
		actionData["a"] = []byte{0x01}
	} else {
		actionData["a"] = []byte{0x00}
	}
	data := BuildBody(actionData, conf.RedirectURL, conf.SessionId, Checking)
	ch := make(chan []byte, 1)
	ch <- data
	req, err := http.NewRequestWithContext(ctx, conf.Method, conf.Target, netrans.NewChannelReader(ch))
	if err != nil {
		return err
	}
	req.Header = conf.Header

	now := time.Now()
	go func() {
		// timeout
		time.Sleep(time.Second * 3)
		close(ch)
	}()
	resp, err := rawClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 如果独到响应的时间在3s内，说明请求没有被缓存, 那么就可以变成全双工的
	// 如果响应时间在 3~6s 之间，说明请求被缓存了， 但响应仍然是流式的, 但是可以使用半双工
	// 否则只能用短链接了
	echoData, bodyData, err := UnmarshalFrameWithBuffer(resp.Body)
	if err != nil {
		// 这里不要直接返回，有时虽然 eof 了但是数据是对的，可以使用
		// todo: why listener eof
		header, _ := httputil.DumpResponse(resp, false)
		log.Errorf("response are as follows:\n%s", string(header)+string(bodyData))
		return fmt.Errorf("got unexpected body, remote server test failed")
	}
	// ignore bodyData
	sessionData, _, err := UnmarshalFrameWithBuffer(resp.Body)
	if err != nil {
		return err
	}
	duration := time.Since(now).Milliseconds()

	if !strings.EqualFold(string(echoData["dt"]), identifier) {
		header, _ := httputil.DumpResponse(resp, false)
		log.Errorf("response are as follows:\n%s", string(header)+string(bodyData))
		return fmt.Errorf("got unexpected body, remote server test failed")
	}

	sid := string(sessionData["dt"])
	conf.SessionId = sid
	log.Infof("handshake success, using session id %s", sid)

	if conf.Mode == AutoDuplex {
		if duration < 3000 {
			conf.Mode = FullDuplex
		} else if duration < 5000 {
			conf.Mode = HalfDuplex
		} else {
			conf.Mode = Classic
		}
	}
	return nil
}
