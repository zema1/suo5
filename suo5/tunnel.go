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

type StreamFactory interface {
	Spawn(id string) (*TunnelConn, error)
	Release(id string)
	Wait()
	Shutdown()
}

type TunnelConn struct {
	id        string
	once      sync.Once
	mu        sync.Mutex
	readChan  chan map[string][]byte
	readBuf   bytes.Buffer
	writeChan chan []byte
	config    *Suo5Config
	closed    atomic.Bool
	onClose   func()
	ctx       context.Context
	cancel    func()
}

func NewTunnelConn(id string, config *Suo5Config, writeChan chan []byte, onClose func()) *TunnelConn {
	ctx, cancel := context.WithCancel(context.Background())
	return &TunnelConn{
		id:        id,
		readChan:  make(chan map[string][]byte, 32),
		writeChan: writeChan,
		config:    config,
		onClose:   onClose,
		ctx:       ctx,
		cancel:    cancel,
	}
}

func (s *TunnelConn) ReadUnmarshal() (map[string][]byte, error) {
	if s.closed.Load() {
		return nil, fmt.Errorf("tunnel %s is closed", s.id)
	}
	select {
	case m, ok := <-s.readChan:
		if !ok {
			return nil, io.EOF
		}
		return m, nil
	case <-time.After(time.Duration(s.config.Timeout) * time.Second):
		return nil, fmt.Errorf("timeout when read from tunnel %s", s.id)
	}
}

func (s *TunnelConn) SetupConnHeartBeat() {
	ticker := time.NewTicker(time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				_, err := s.Write(nil)
				if err != nil {
					log.Error(err)
					return
				}
			}
		}
	}()
}

func (s *TunnelConn) RemoteData(m map[string][]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return
	}
	select {
	case s.readChan <- m:
	default:
	}
}

func (s *TunnelConn) Read(p []byte) (n int, err error) {
	if s.readBuf.Len() != 0 {
		return s.readBuf.Read(p)
	}
	m, ok := <-s.readChan
	if !ok {
		return 0, io.EOF
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

func (s *TunnelConn) Write(p []byte) (n int, err error) {
	partWrite := 0
	chunkSize := s.config.MaxRequestSize
	if len(p) > chunkSize {
		// todo: recheck
		log.Debugf("split data to %d chunk, length: %d", len(p)/chunkSize, len(p))
		for i := 0; i < len(p); i += chunkSize {
			act := NewActionData(s.id, p[i:minInt(i+chunkSize, len(p))])
			body := BuildBody(act, s.config.RedirectURL, s.config.Mode)
			n, err = s.WriteRaw(body)
			if err != nil {
				return partWrite, err
			}
			partWrite += n
		}
		return partWrite, nil
	} else {
		body := BuildBody(NewActionData(s.id, p), s.config.RedirectURL, s.config.Mode)
		return s.WriteRaw(body)
	}
}

func (s *TunnelConn) WriteRaw(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed.Load() {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}

	select {
	case s.writeChan <- p:
		return len(p), nil
	default:
		log.Warnf("write buffer is full, discard current message, data len %d", len(p))
		return 0, nil
	}
}

func (s *TunnelConn) CloseSelf() {
	s.once.Do(func() {
		log.Debugf("closing tunnel byself %s", s.id)
		s.cancel()
		if s.onClose != nil {
			s.onClose()
		}

		s.mu.Lock()
		defer s.mu.Unlock()

		close(s.readChan)
		s.closed.Store(true)
	})
}

func (s *TunnelConn) Close() error {
	s.once.Do(func() {
		log.Debugf("closing tunnel %s", s.id)
		s.cancel()
		if s.onClose != nil {
			s.onClose()
		}
		body := BuildBody(NewActionDelete(s.id), s.config.RedirectURL, s.config.Mode)
		_, _ = s.WriteRaw(body)

		s.mu.Lock()
		defer s.mu.Unlock()

		close(s.readChan)
		s.closed.Store(true)
	})
	return nil
}

func minInt(i int, i2 int) int {
	if i < i2 {
		return i
	}
	return i2
}
