package suo5

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gobwas/glob"
	"github.com/zema1/suo5/tpl"
)

var (
	DefaultMaxRequestSize      = 1024 * 512
	DefaultTimeout             = 5
	DefaultClassicPollQPS      = 6
	DefaultClassicPollInterval = 200 // ms, 1s
	DefaultRetryCount          = 1
	DefaultRotateCount         = 8
	DefaultBufferSize          = 1024 * 64
)

type Suo5Config struct {
	Method           string         `json:"method"`
	Listen           string         `json:"listen"`
	Target           string         `json:"target"`
	Username         string         `json:"username"`
	Password         string         `json:"password"`
	Mode             ConnectionType `json:"mode"`
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
	MaxBodySize      int            `json:"max_body_size"`
	ClassicPollQPS   int            `json:"classic_poll_qps"`
	RetryCount       int            `json:"retry_count"`

	ClassicPollInterval int  `json:"classic_poll_interval"`
	ImpersonateBrowser  bool `json:"impersonate_browser"`

	SessionId               string                               `json:"-"`
	TestExit                string                               `json:"-"`
	ExcludeGlobs            []glob.Glob                          `json:"-"`
	Offset                  int                                  `json:"-"`
	Header                  http.Header                          `json:"-"`
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
		Timeout:          DefaultTimeout,
		Debug:            false,
		UpstreamProxy:    []string{},
		RedirectURL:      "",
		RawHeader:        []string{},
		DisableHeartbeat: false,
		EnableCookieJar:  false,
		ForwardTarget:    "",
		MaxBodySize:      DefaultMaxRequestSize,
		ClassicPollQPS:   DefaultClassicPollQPS,
		RetryCount:       DefaultRetryCount,

		ClassicPollInterval: DefaultClassicPollInterval,
		ImpersonateBrowser:  true,
	}
}

func (conf *Suo5Config) Parse() error {
	conf.Target = strings.TrimSpace(conf.Target)

	if conf.Timeout <= 0 {
		conf.Timeout = DefaultTimeout
	}

	if conf.MaxBodySize <= 0 {
		conf.MaxBodySize = DefaultMaxRequestSize
	}

	if conf.ClassicPollQPS <= 0 {
		conf.ClassicPollQPS = DefaultClassicPollQPS
	}

	if conf.ClassicPollInterval <= 0 {
		conf.ClassicPollInterval = DefaultClassicPollInterval
	}

	isValidMode := false
	for _, m := range AllConnectionTypes() {
		if conf.Mode == m {
			isValidMode = true
			break
		}
	}
	if !isValidMode {
		return fmt.Errorf("invalid connection mode: %s", conf.Mode)
	}

	err := conf.parseExcludeDomain()
	if err != nil {
		return err
	}
	err = conf.parseHeader()
	if err != nil {
		return err
	}
	return nil
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
		target := conf.GetTarget()
		n := strings.LastIndex(target, "/")
		if n == -1 {
			conf.Header.Set("Referer", target)
		} else {
			conf.Header.Set("Referer", target)
		}
	}

	return nil
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

func (conf *Suo5Config) RequestHeader() http.Header {
	header := conf.Header.Clone()
	if header.Get("User-Agent") == "" {
		header.Set("User-Agent", RandUserAgent())
	}

	if conf.ImpersonateBrowser {
		browserHeaders := GetBrowserHeaders(header.Get("User-Agent"))
		for k, v := range browserHeaders {
			if header.Get(k) == "" {
				header.Set(k, v)
			}
		}

	}

	return header
}

func (conf *Suo5Config) NewRequest(ctx context.Context, body io.Reader, contentLength int64) *http.Request {
	req, err := http.NewRequestWithContext(ctx, conf.Method, conf.GetTarget(), body)
	if err != nil {
		panic(err)
	}
	req.Header = conf.RequestHeader()
	req.ContentLength = contentLength
	return req
}

func (conf *Suo5Config) GetTarget() string {
	target := conf.Target
	if strings.Contains(target, "{rand}") {
		target = strings.ReplaceAll(target, "{rand}", tpl.RandWord())
	}
	return target
}
