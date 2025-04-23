package suo5

import (
	"context"
	"io"
	"sync/atomic"
	"time"

	log "github.com/kataras/golog"
)

type RawReadWriteCloser interface {
	io.ReadWriteCloser
	WriteRaw(p []byte) (n int, err error)
}

func NewHeartbeatRW(rw RawReadWriteCloser, id, redirect string, mode ConnectionType) io.ReadWriteCloser {
	ctx, cancel := context.WithCancel(context.Background())
	h := &heartbeatRW{
		rw:       rw,
		id:       id,
		redirect: redirect,
		mode:     mode,
		cancel:   cancel,
	}
	go h.heartbeat(ctx)
	return h
}

type heartbeatRW struct {
	id            string
	redirect      string
	mode          ConnectionType
	rw            RawReadWriteCloser
	lastHaveWrite atomic.Bool
	cancel        func()
}

func (h *heartbeatRW) Read(p []byte) (n int, err error) {
	return h.rw.Read(p)
}

func (h *heartbeatRW) Write(p []byte) (n int, err error) {
	h.lastHaveWrite.Store(true)
	return h.rw.Write(p)
}

func (h *heartbeatRW) Close() error {
	h.cancel()
	return h.rw.Close()
}

// write data to the remote server to avoid server's ReadTimeout
func (h *heartbeatRW) heartbeat(ctx context.Context) {
	t := time.NewTicker(time.Second * 10)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if h.lastHaveWrite.Load() {
				h.lastHaveWrite.Store(false)
				continue
			}
			body := BuildBody(NewActionHeartbeat(h.id), h.redirect, h.mode)
			log.Debugf("send heartbeat, length: %d", len(body))
			_, err := h.rw.WriteRaw(body)
			if err != nil {
				log.Errorf("send heartbeat error %s", err)
				return
			}
			h.lastHaveWrite.Store(false)
		case <-ctx.Done():
			return
		}
	}
}
