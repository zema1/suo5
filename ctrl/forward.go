package ctrl

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	log "github.com/kataras/golog"
	"github.com/zema1/suo5/netrans"
	"github.com/zema1/suo5/suo5"
)

type ForwardHandler struct {
	*suo5.Suo5Client

	ctx        context.Context
	pool       *sync.Pool
	targetAddr string
}

func NewForwardHandler(ctx context.Context, client *suo5.Suo5Client, pool *sync.Pool, targetAddr string) *ForwardHandler {
	return &ForwardHandler{
		Suo5Client: client,
		ctx:        ctx,
		pool:       pool,
		targetAddr: targetAddr,
	}
}

func (f *ForwardHandler) Handle(conn net.Conn) error {
	defer conn.Close()

	conn = netrans.NewTimeoutConn(conn, 0, time.Second*3)
	log.Infof("start forwarding connection to %s", f.targetAddr)

	streamRW := suo5.NewSuo5Conn(f.ctx, f.Suo5Client)
	err := streamRW.ConnectMultiplex(f.targetAddr)
	if err != nil {
		log.Errorf("failed to connect to target: %v", err)
		return err
	}

	log.Infof("successfully connected to %s", f.targetAddr)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer streamRW.Close()
		if err := f.pipe(conn, streamRW); err != nil {
			log.Debugf("local conn closed, %s", f.targetAddr)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer conn.Close()
		if err := f.pipe(streamRW, conn); err != nil {
			log.Debugf("remote readwriter closed, %s", f.targetAddr)
		}
	}()

	wg.Wait()
	log.Infof("forwarded connection closed, %s", f.targetAddr)
	return nil
}

func (f *ForwardHandler) pipe(r io.Reader, w io.Writer) error {
	buf := f.pool.Get().([]byte)
	defer f.pool.Put(buf) //nolint:staticcheck
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
