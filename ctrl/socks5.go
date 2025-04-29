package ctrl

import (
	"context"
	"fmt"
	"github.com/go-gost/gosocks5/server"
	"github.com/pkg/errors"
	"net"
	"net/url"
	"time"

	"github.com/go-gost/gosocks5"
	log "github.com/kataras/golog"
	"github.com/zema1/suo5/netrans"
	"github.com/zema1/suo5/suo5"
)

type Socks5Handler struct {
	*suo5.Suo5Client

	ctx      context.Context
	selector gosocks5.Selector
}

func NewSocks5Handler(ctx context.Context, client *suo5.Suo5Client) *Socks5Handler {
	selector := server.DefaultSelector
	if !client.Config.NoAuth() {
		selector = server.NewServerSelector([]*url.Userinfo{
			url.UserPassword(client.Config.Username, client.Config.Password),
		})
	}

	return &Socks5Handler{
		ctx:        ctx,
		Suo5Client: client,
		selector:   selector,
	}
}

func (m *Socks5Handler) Handle(conn net.Conn) error {
	defer conn.Close()

	conn = netrans.NewTimeoutConn(conn, 0, time.Second*3)
	conn = gosocks5.ServerConn(conn, m.selector)
	req, err := gosocks5.ReadRequest(conn)
	if err != nil {
		return err
	}

	if len(m.Config.ExcludeGlobs) != 0 {
		for _, g := range m.Config.ExcludeGlobs {
			if g.Match(req.Addr.Host) {
				log.Debugf("drop connection to %s", req.Addr.Host)
				return nil
			}
		}
	}

	log.Infof("start connection to %s", req.Addr.String())
	switch req.Cmd {
	case gosocks5.CmdConnect:
		m.handleConnect(conn, req)
		return nil
	default:
		return fmt.Errorf("%d: unsupported command", gosocks5.CmdUnsupported)
	}
}

func (m *Socks5Handler) handleConnect(conn net.Conn, sockReq *gosocks5.Request) {
	streamRW := suo5.NewSuo5Conn(m.ctx, m.Suo5Client)
	err := streamRW.ConnectMultiplex(sockReq.Addr.String())
	if err != nil {
		log.Errorf("failed to connect to %s, %v", sockReq.Addr, err)
		ReplyError(conn, err)
		return
	}
	rep := gosocks5.NewReply(gosocks5.Succeeded, nil)
	err = rep.Write(conn)
	if err != nil {
		log.Errorf("write data failed, %w", err)
		return
	}
	log.Infof("successfully connected to %s", sockReq.Addr)

	m.DualPipe(conn, streamRW.ReadWriteCloser, sockReq.Addr.String())

	log.Infof("connection closed, %s", sockReq.Addr)
}

func ReplyError(conn net.Conn, err error) {
	var rep *gosocks5.Reply
	if errors.Is(err, suo5.ErrHostUnreachable) {
		rep = gosocks5.NewReply(gosocks5.HostUnreachable, nil)
	} else if errors.Is(err, suo5.ErrDialFailed) {
		rep = gosocks5.NewReply(gosocks5.Failure, nil)
	} else if errors.Is(err, suo5.ErrConnRefused) {
		rep = gosocks5.NewReply(gosocks5.ConnRefused, nil)
	} else {
		rep = gosocks5.NewReply(gosocks5.Failure, nil)
	}
	_ = rep.Write(conn)
}
