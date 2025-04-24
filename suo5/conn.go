package suo5

import (
	"context"
	"github.com/pkg/errors"
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
	ctx context.Context
	*Suo5Client
}

func (suo *Suo5Conn) ConnectMultiplex(address string) error {
	id := RandString(8)
	plexConn, err := suo.Factory.Spawn(id, address)
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
	if !suo.Config.DisableHeartbeat {
		streamRW = NewHeartbeatRW(streamRW.(RawReadWriteCloser), id, suo.Config.RedirectURL, suo.Config.Mode)
	}
	suo.ReadWriteCloser = streamRW
	return nil
}
