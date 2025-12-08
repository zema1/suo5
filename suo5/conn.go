package suo5

import (
	"context"
	"github.com/kataras/golog"
	"github.com/pkg/errors"
	"github.com/zema1/suo5/netrans"
	"io"
)

var (
	ErrHostUnreachable = errors.New("host unreachable")
	ErrDialFailed      = errors.New("dial failed")
	ErrConnRefused     = errors.New("connection refused")
	ErrFactoryStopped  = errors.New("factory has stopped")
)

func NewSuo5Conn(ctx context.Context, client *Suo5Client) *Suo5Conn {
	return &Suo5Conn{
		ctx:        ctx,
		Suo5Client: client,
	}
}

type Suo5Conn struct {
	io.ReadWriteCloser
	*Suo5Client

	ctx context.Context
}

func (suo5 *Suo5Conn) ConnectMultiplex(address string) error {
	id := RandString(8)
	golog.Debugf("trying to connect to %s with id %s", address, id)
	plexConn, err := suo5.Spawn(id, address)
	if err != nil {
		return err
	}

	// todo: offset
	// skip offset
	// if suo.Config.Offset > 0 {
	// 	log.Debugf("skipping offset %d", suo.Config.Offset)
	// 	_, err = io.CopyN(io.Discard, resp.Body, int64(suo.Config.Offset))
	// 	if err != nil {
	// 		log.Errorf("failed to skip offset, %s", err)
	// 		return errors.Wrap(ErrDialFailed, err.Error())
	// 	}
	// }

	streamRW := io.ReadWriteCloser(plexConn)
	if !suo5.Config.DisableHeartbeat {
		streamRW = NewHeartbeatRW(streamRW.(RawReadWriteCloser), id, suo5.Config)
	}
	if suo5.Config.OnSpeedInfo != nil {
		streamRW = netrans.NewSpeedTrackingReadWriteCloser(streamRW)
	}
	suo5.ReadWriteCloser = streamRW
	return nil
}
