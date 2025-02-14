package ctrl

import (
	"context"
	"fmt"
	"github.com/pkg/errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/go-gost/gosocks5"
	log "github.com/kataras/golog"
	"github.com/zema1/suo5/netrans"
	"github.com/zema1/suo5/suo5"
)

type socks5Handler struct {
	*suo5.Suo5Client

	ctx      context.Context
	pool     *sync.Pool
	selector gosocks5.Selector
}

func (m *socks5Handler) Handle(conn net.Conn) error {
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

func (m *socks5Handler) handleConnect(conn net.Conn, sockReq *gosocks5.Request) {
	streamRW := suo5.NewSuo5Conn(m.ctx, m.Suo5Client)
	err := streamRW.Connect(sockReq.Addr.String())
	if err != nil {
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

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer streamRW.Close()
		if err := m.pipe(conn, streamRW); err != nil {
			log.Debugf("local conn closed, %s", sockReq.Addr)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer conn.Close()
		if err := m.pipe(streamRW, conn); err != nil {
			log.Debugf("remote readwriter closed, %s", sockReq.Addr)
		}
	}()

	wg.Wait()
	log.Infof("connection closed, %s", sockReq.Addr)
}

func (m *socks5Handler) pipe(r io.Reader, w io.Writer) error {
	buf := m.pool.Get().([]byte)
	defer m.pool.Put(buf) //nolint:staticcheck
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

func ReplyError(conn net.Conn, err error) {
	var rep *gosocks5.Reply
	if errors.Is(err, suo5.ErrHostUnreachable) {
		rep = gosocks5.NewReply(gosocks5.HostUnreachable, nil)
	} else if errors.Is(err, suo5.ErrDialFailed) {
		rep = gosocks5.NewReply(gosocks5.Failure, nil)
	} else if errors.Is(err, suo5.ErrConnRefused) {
		rep = gosocks5.NewReply(gosocks5.ConnRefused, nil)
	}
	_ = rep.Write(conn)
}
