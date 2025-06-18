package suo5

import (
	"context"
	"fmt"
	"github.com/gobwas/glob"
	"io"
	"net/http"
	"strings"
	"time"
)

var (
	DefaultMaxRequestSize = 1024 * 512
	DefaultTimeout        = 10
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
	}
}

func (conf *Suo5Config) Parse() error {
	if conf.Timeout <= 0 {
		conf.Timeout = DefaultTimeout
	}

	if conf.MaxBodySize <= 0 {
		conf.MaxBodySize = DefaultMaxRequestSize
	}

	if conf.ClassicPollQPS <= 0 {
		conf.ClassicPollQPS = DefaultClassicPollQPS
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
	return header
}

func (conf *Suo5Config) NewRequest(ctx context.Context, body io.Reader, contentLength int64) *http.Request {
	req, _ := http.NewRequestWithContext(ctx, conf.Method, conf.Target, body)
	req.ContentLength = contentLength
	req.Header = conf.RequestHeader()
	return req
}
