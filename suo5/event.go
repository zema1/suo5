package suo5

import (
	"github.com/go-gost/gosocks5/server"
	"net"
)

type ClientEventHandler struct {
	Inner                   server.Handler
	OnNewClientConnection   func(event *ClientConnectionEvent)
	OnClientConnectionClose func(event *ClientConnectCloseEvent)
}

func (e *ClientEventHandler) Handle(conn net.Conn) error {
	if e.OnNewClientConnection != nil {
		e.OnNewClientConnection(&ClientConnectionEvent{conn})
	}
	defer func() {
		if e.OnClientConnectionClose != nil {
			e.OnClientConnectionClose(&ClientConnectCloseEvent{conn})
		}
	}()
	return e.Inner.Handle(conn)
}

type ConnectedEvent struct {
	Mode ConnectionType `json:"mode"`
}

type ClientConnectionEvent struct {
	Conn net.Conn
}

type ClientConnectCloseEvent struct {
	Conn net.Conn
}

type SpeedStatisticEvent struct {
	Upload   uint64
	Download uint64
}
