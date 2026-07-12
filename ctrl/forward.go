package ctrl

import (
	"context"
	"net"

	log "github.com/kataras/golog"
	"github.com/zema1/suo5/suo5"
)

type ForwardHandler struct {
	*suo5.Suo5Client

	ctx context.Context
}

func NewForwardHandler(ctx context.Context, client *suo5.Suo5Client) *ForwardHandler {
	return &ForwardHandler{
		Suo5Client: client,
	}
}

func (f *ForwardHandler) Handle(conn net.Conn) error {
	defer conn.Close()

	log.Infof("starting forwarding connection to: %s", f.Config.ForwardTarget)

	streamRW := suo5.NewSuo5Conn(f.ctx, f.Suo5Client)
	err := streamRW.ConnectMultiplex(f.Config.ForwardTarget)
	if err != nil {
		log.Warnf("failed to connect to target: %v", err)
		return err
	}

	log.Infof("connection established: %s", f.Config.ForwardTarget)

	f.DualPipe(conn, streamRW.ReadWriteCloser, f.Config.ForwardTarget)

	log.Infof("forward connection closed: %s", f.Config.ForwardTarget)
	return nil
}
