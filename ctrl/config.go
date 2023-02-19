package ctrl

import "io"

type Suo5Config struct {
	Listen     string         `json:"listen"`
	Target     string         `json:"target"`
	NoAuth     bool           `json:"no_auth"`
	Username   string         `json:"username"`
	Password   string         `json:"password"`
	Mode       ConnectionType `json:"mode"`
	UserAgent  string         `json:"ua"`
	BufferSize int            `json:"buffer_size"`
	Timeout    int            `json:"timeout"`
	Debug      bool           `json:"debug"`

	OnRemoteConnected       func(e *ConnectedEvent)              `json:"-"`
	OnNewClientConnection   func(event *ClientConnectionEvent)   `json:"-"`
	OnClientConnectionClose func(event *ClientConnectCloseEvent) `json:"-"`
	GuiLog                  io.Writer                            `json:"-"`
}

func DefaultSuo5Config() *Suo5Config {
	return &Suo5Config{
		Listen:     "127.0.0.1:1111",
		Target:     "",
		NoAuth:     true,
		Username:   "",
		Password:   "",
		Mode:       "auto",
		UserAgent:  "Mozilla/5.0 (Linux; Android 6.0; Nexus 5 Build/MRA58N) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/109.1.2.3",
		BufferSize: 1024 * 320,
		Timeout:    10,
		Debug:      false,
	}
}
