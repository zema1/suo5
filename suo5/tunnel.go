package suo5

import (
	"bytes"
	"context"
	"fmt"
	log "github.com/kataras/golog"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

type TunnelConn struct {
	id            string
	once          sync.Once
	writeMu       sync.Mutex
	readChan      chan map[string][]byte
	readBuf       bytes.Buffer
	remoteWrite   IdWriteFunc
	config        *Suo5Config
	lastHaveWrite atomic.Bool
	closed        atomic.Bool
	onClose       []func()
	ctx           context.Context
	cancel        func()
}

func NewTunnelConn(id string, config *Suo5Config, remoteWrite IdWriteFunc) *TunnelConn {
	ctx, cancel := context.WithCancel(context.Background())
	return &TunnelConn{
		id:          id,
		readChan:    make(chan map[string][]byte, 32),
		remoteWrite: remoteWrite,
		config:      config,
		ctx:         ctx,
		cancel:      cancel,
	}
}

func (s *TunnelConn) ReadUnmarshal() (map[string][]byte, error) {
	if s.closed.Load() {
		return nil, fmt.Errorf("tunnel %s is closed", s.id)
	}
	timer := time.NewTimer(s.config.TimeoutTime())
	defer timer.Stop()
	select {
	case m := <-s.readChan:
		return m, nil
	case <-s.ctx.Done():
		return nil, io.EOF
	case <-timer.C:
		return nil, fmt.Errorf("timeout when read from tunnel %s", s.id)
	}
}

func (s *TunnelConn) AddCloseCallback(fn func()) {
	s.onClose = append(s.onClose, fn)
}

func (s *TunnelConn) RemoteData(m map[string][]byte) {
	if s.closed.Load() {
		return
	}
	// Preserve frame ordering and propagate backpressure to the remote reader.
	// The tunnel context makes a blocked send cancelable without closing readChan.
	select {
	case s.readChan <- m:
	case <-s.ctx.Done():
	}
}

func (s *TunnelConn) Read(p []byte) (n int, err error) {
	if s.readBuf.Len() != 0 {
		return s.readBuf.Read(p)
	}
	m, err := s.readRemoteData()
	if err != nil {
		return 0, err
	}

	action := m["ac"]
	if len(action) != 1 {
		return 0, fmt.Errorf("invalid action when read %v", action)
	}
	switch action[0] {
	case ActionData:
		data := m["dt"]
		s.readBuf.Reset()
		s.readBuf.Write(data)
		return s.readBuf.Read(p)
	case ActionDelete:
		s.CloseSelf()
		return 0, io.EOF
	default:
		return 0, fmt.Errorf("unpected action when read %v", action)
	}
}

func (s *TunnelConn) readRemoteData() (map[string][]byte, error) {
	// Once DispatchRemoteData has accepted a frame it must be delivered even if
	// the remote HTTP response reaches EOF and closes the tunnel immediately.
	select {
	case m := <-s.readChan:
		return m, nil
	default:
	}
	select {
	case m := <-s.readChan:
		return m, nil
	case <-s.ctx.Done():
		return nil, io.EOF
	}
}

func (s *TunnelConn) Write(p []byte) (int, error) {
	partWrite := 0
	chunkSize := s.config.MaxBodySize
	if len(p) > chunkSize {
		log.Debugf("splitting data to %d chunks, length: %d", len(p)/chunkSize, len(p))
		for i := 0; i < len(p); i += chunkSize {
			act := NewActionData(s.id, p[i:minInt(i+chunkSize, len(p))])
			body := BuildBody(act, s.config.RedirectURL, s.config.SessionId, s.config.Mode)
			n, err := s.WriteRaw(body, false)
			if err != nil {
				return partWrite, err
			}
			partWrite += n
		}
		return partWrite, nil
	} else {
		body := BuildBody(NewActionData(s.id, p), s.config.RedirectURL, s.config.SessionId, s.config.Mode)
		return s.WriteRaw(body, false)
	}
}

func (s *TunnelConn) WriteRaw(p []byte, noDelay bool) (n int, err error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed.Load() {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	s.lastHaveWrite.Store(true)

	err = s.remoteWrite(&IdData{s.id, p, noDelay})
	if err != nil {
		return 0, err
	} else {
		return len(p), nil
	}
}

func (s *TunnelConn) CloseSelf() {
	s.once.Do(func() {
		log.Debugf("closing tunnel by itself: %s", s.id)
		s.closed.Store(true)
		s.cancel()
		for _, fn := range s.onClose {
			fn()
		}
	})
}

func (s *TunnelConn) Close() error {
	s.once.Do(func() {
		log.Debugf("closing tunnel: %s", s.id)
		defer log.Debugf("tunnel closed: %s", s.id)
		body := BuildBody(NewActionDelete(s.id), s.config.RedirectURL, s.config.SessionId, s.config.Mode)
		_, _ = s.WriteRaw(body, false)

		s.closed.Store(true)
		s.cancel()
		for _, fn := range s.onClose {
			fn()
		}
	})
	return nil
}

func (s *TunnelConn) SetupActivePoll() {
	ticker := time.NewTicker(time.Millisecond * time.Duration(s.config.ClassicPollInterval))
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				if s.lastHaveWrite.Load() {
					s.lastHaveWrite.Store(false)
					continue
				}
				_, err := s.Write(nil)
				if err != nil {
					log.Warnf("poll write failed for tunnel %s: %v", s.id, err)
					return
				}
			}
		}
	}()
}

func minInt(i int, i2 int) int {
	if i < i2 {
		return i
	}
	return i2
}
