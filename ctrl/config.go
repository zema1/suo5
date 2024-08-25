package ctrl

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Suo5Config struct {
	Method           string         `json:"method"`
	Listen           string         `json:"listen"`
	Target           string         `json:"target"`
	NoAuth           bool           `json:"no_auth"`
	Username         string         `json:"username"`
	Password         string         `json:"password"`
	Mode             ConnectionType `json:"mode"`
	BufferSize       int            `json:"buffer_size"`
	Timeout          int            `json:"timeout"`
	Debug            bool           `json:"debug"`
	UpstreamProxy    string         `json:"upstream_proxy"`
	RedirectURL      string         `json:"redirect_url"`
	RawHeader        []string       `json:"raw_header"`
	DisableHeartbeat bool           `json:"disable_heartbeat"`
	DisableGzip      bool           `json:"disable_gzip"`
	DisableCookiejar bool           `json:"disable_cookiejar"`
	ExcludeDomain    []string       `json:"exclude_domain"`

	TestExit                string                               `json:"-"`
	ExcludeDomainMap        map[string]bool                      `json:"-"`
	Offset                  int                                  `json:"-"`
	Header                  http.Header                          `json:"-"`
	OnRemoteConnected       func(e *ConnectedEvent)              `json:"-"`
	OnNewClientConnection   func(event *ClientConnectionEvent)   `json:"-"`
	OnClientConnectionClose func(event *ClientConnectCloseEvent) `json:"-"`
	GuiLog                  io.Writer                            `json:"-"`
}

func (s *Suo5Config) Parse() error {
	s.parseExcludeDomain()
	return s.parseHeader()
}

func (s *Suo5Config) parseExcludeDomain() {
	s.ExcludeDomainMap = make(map[string]bool)
	for _, domain := range s.ExcludeDomain {
		s.ExcludeDomainMap[strings.ToLower(strings.TrimSpace(domain))] = true
	}
}

func (s *Suo5Config) parseHeader() error {
	s.Header = make(http.Header)
	for _, value := range s.RawHeader {
		if value == "" {
			continue
		}
		parts := strings.SplitN(value, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid header value %s", value)
		}
		s.Header.Set(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
	}

	if s.Header.Get("Referer") == "" {
		n := strings.LastIndex(s.Target, "/")
		if n == -1 {
			s.Header.Set("Referer", s.Target)
		} else {
			s.Header.Set("Referer", s.Target[:n+1])
		}
	}

	return nil
}

func (s *Suo5Config) HeaderString() string {
	ret := ""
	for k := range s.Header {
		ret += fmt.Sprintf("\n%s: %s", k, s.Header.Get(k))
	}
	return ret
}

func DefaultSuo5Config() *Suo5Config {
	return &Suo5Config{
		Method:           "POST",
		Listen:           "127.0.0.1:1111",
		Target:           "",
		NoAuth:           true,
		Username:         "",
		Password:         "",
		Mode:             "auto",
		BufferSize:       1024 * 320,
		Timeout:          10,
		Debug:            false,
		UpstreamProxy:    "",
		RedirectURL:      "",
		RawHeader:        []string{"User-Agent: Mozilla/5.0 (Linux; Android 6.0; Nexus 5 Build/MRA58N) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/109.1.2.3"},
		DisableHeartbeat: false,
		DisableCookiejar: false,
	}
}
