package suo5

import (
	"bytes"
	"context"
	"fmt"
	log "github.com/kataras/golog"
	"github.com/pkg/errors"
	"github.com/zema1/suo5/netrans"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type ClassicReadWriter struct {
	id        string
	mu        sync.Mutex
	config    *Suo5Config
	client    *http.Client
	once      sync.Once
	readBuf   io.Reader
	readChan  chan []byte
	writeChan chan []byte
	closed    atomic.Bool

	ctx    context.Context
	cancel func()
}

func NewClassicReadWriter(rootCtx context.Context, id string, client *http.Client, config *Suo5Config) *ClassicReadWriter {
	ctx, cancel := context.WithCancel(rootCtx)
	readChan := make(chan []byte, 4096)
	readBuf := netrans.NewChannelReader(readChan)
	rw := &ClassicReadWriter{
		id:        id,
		config:    config,
		client:    client,
		readBuf:   readBuf,
		readChan:  readChan,
		writeChan: make(chan []byte, 4096),
		ctx:       ctx,
		cancel:    cancel,
	}
	rw.sync()
	return rw
}

func (s *ClassicReadWriter) sync() {
	go func() {
		defer s.Close()
		for {
			select {
			case <-s.ctx.Done():
				return
			case data, ok := <-s.writeChan:
				if !ok {
					return
				}
				if s.closed.Load() {
					return
				}
				// for i := 0; i < len(s.writeChan); i++ {
				// 	tmp := <-s.writeChan
				// 	data = append(data, tmp...)
				// 	// need to be configured ?
				// 	if len(data) > 1024*512 {
				// 		break
				// 	}
				// }

				// heartbeat poll
				if len(data) == 0 {
					continue
				}
				if err := s.poll(data); err != nil {
					if !errors.Is(err, context.Canceled) {
						log.Errorf("failed to send request: %v", err)
					}
					return
				}
			}
		}
	}()

	go func() {
		defer s.Close()
		ticker := time.NewTicker(time.Millisecond * 300)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				if s.closed.Load() {
					return
				}
				body := BuildBody(NewActionData(s.id, nil), s.config.RedirectURL, Classic)
				_, err := s.WriteRaw(body)
				if err != nil {
					log.Errorf("send heartbeat error %s", err)
				}
			}
		}
	}()
}

func (s *ClassicReadWriter) poll(p []byte) error {
	log.Debugf("send polling request, body len: %d", len(p))
	req, err := http.NewRequestWithContext(s.ctx, s.config.Method, s.config.Target, bytes.NewReader(p))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(p))
	req.Header = s.config.Header.Clone()
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("unexpected status of %d", resp.StatusCode)
	}
	if resp.ContentLength == 0 {
		return nil
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	fr, err := netrans.ReadFrame(bytes.NewReader(data))
	if err != nil {
		return err
	}
	m, err := Unmarshal(fr.Data)
	if err != nil {
		return err
	}
	log.Debugf("recv polling result, action: %c, data: %d", m["ac"], len(m["dt"]))
	action := m["ac"]
	if len(action) != 1 {
		return fmt.Errorf("invalid action when read %v", action)
	}
	switch action[0] {
	case ActionData:
		dt := m["dt"]
		if len(dt) != 0 {
			s.mu.Lock()
			if s.closed.Load() {
				s.mu.Unlock()
				return nil
			}
			s.mu.Unlock()

			select {
			case s.readChan <- dt:
				return nil
			default:
				log.Warnf("connection is abnormal (read buf full), closing connection")
				return s.Close()
			}
		}
		return nil
	case ActionDelete:
		return s.Close()
	case ActionHeartbeat:
		return nil
	default:
		return fmt.Errorf("unepected action when read %v", action)
	}
}

func (s *ClassicReadWriter) Read(p []byte) (n int, err error) {
	return s.readBuf.Read(p)
}

func (s *ClassicReadWriter) Write(p []byte) (n int, err error) {
	log.Debugf("write data, length: %d", len(p))
	body := BuildBody(NewActionData(s.id, p), s.config.RedirectURL, Classic)
	return s.WriteRaw(body)
}

func (s *ClassicReadWriter) WriteRaw(p []byte) (n int, err error) {
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

func (s *ClassicReadWriter) Close() error {
	s.once.Do(func() {
		s.closed.Store(true)
		s.mu.Lock()
		close(s.readChan)
		s.mu.Unlock()

		defer s.cancel()

		body := BuildBody(NewActionDelete(s.id), s.config.RedirectURL, Classic)
		req, err := http.NewRequestWithContext(s.ctx, s.config.Method, s.config.Target, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header = s.config.Header.Clone()
		resp, err := s.client.Do(req)
		if err != nil {
			log.Errorf("send close error: %v", err)
			return
		}
		_ = resp.Body.Close()
	})
	return nil
}
