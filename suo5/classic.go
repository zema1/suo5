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

type ClassicStreamFactory struct {
	once   sync.Once
	config *Suo5Config
	client *http.Client

	closeMu sync.Mutex
	closed  atomic.Bool

	chanMu   sync.Mutex
	channels map[string]*TunnelConn

	writeChan chan []byte
	ctx       context.Context
	cancel    func()
}

func NewClassicStreamFactory(rootCtx context.Context, config *Suo5Config, client *http.Client) *ClassicStreamFactory {
	ctx, cancel := context.WithCancel(context.Background())

	plex := &ClassicStreamFactory{
		config:    config,
		client:    client,
		channels:  make(map[string]*TunnelConn),
		writeChan: make(chan []byte, 4096),
		ctx:       ctx,
		cancel:    cancel,
	}

	// 留点时间关闭远程连接
	go func() {
		select {
		case <-rootCtx.Done():
			log.Infof("root context is closed, start to cleanup remote connections")
			time.Sleep(time.Second)
			plex.Shutdown()
		case <-ctx.Done():
		}
	}()

	plex.sync()
	return plex
}

func (s *ClassicStreamFactory) sync() {
	go func() {
		defer log.Infof("sync remote connection finished")
		defer s.Shutdown()

		// 等待 writeChan 里所有的数据都发完再 cancel，外层会 Wait() 住
		// 这里失败需要先 cancel
		defer s.cancel()

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
				size := len(s.writeChan)
				if size > 0 {
					for i := 0; i < size; i++ {
						tmp := <-s.writeChan
						data = append(data, tmp...)
						if len(data) > s.config.MaxRequestSize {
							break
						}
					}
				}
				if len(data) == 0 {
					log.Debugf("empty data")
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
		defer log.Infof("channel counter finished")
		for {
			select {
			case <-s.ctx.Done():
				return
			default:
				time.Sleep(time.Second * 5)
				s.chanMu.Lock()
				log.Infof("classic conn count: %d", len(s.channels))
				s.chanMu.Unlock()
			}
		}
	}()
}

func (s *ClassicStreamFactory) poll(p []byte) error {
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

	reader := bytes.NewReader(data)

	for {
		if reader.Len() == 0 {
			break
		}
		fr, err := netrans.ReadFrame(reader)
		if err != nil {
			return err
		}
		m, err := Unmarshal(fr.Data)
		if err != nil {
			return err
		}
		id := string(m["id"])
		if id == "" {
			log.Warnf("empty id found")
			continue
		}
		actions := m["ac"]
		if len(actions) != 1 {
			return fmt.Errorf("invalid action when read %v", actions)
		}
		log.Debugf("recv data from remote, id: %s, action: %v, data: %d", id, actions, len(m["dt"]))

		s.chanMu.Lock()
		conn, ok := s.channels[id]
		if !ok {
			s.chanMu.Unlock()
			log.Warnf("id %s not found, notify remote to close", id)
			body := BuildBody(NewActionDelete(id), s.config.RedirectURL, s.config.Mode)
			select {
			case s.writeChan <- body:
			case <-s.ctx.Done():
				return nil
			default:
				log.Warnf("writeChan is full, discard message")
			}
			continue
		}
		s.chanMu.Unlock()
		conn.RemoteData(m)
	}
	return nil
}

func (s *ClassicStreamFactory) Spawn(id string) (*TunnelConn, error) {
	s.chanMu.Lock()
	defer s.chanMu.Unlock()
	if s.closed.Load() {
		return nil, ErrFactoryStopped
	}
	newConn := NewTunnelConn(id, s.config, s.writeChan, func() {
		s.Release(id)
	})
	s.channels[id] = newConn
	newConn.SetupConnHeartBeat()
	return newConn, nil
}

func (s *ClassicStreamFactory) Wait() {
	<-s.ctx.Done()
}

func (s *ClassicStreamFactory) Release(id string) {
	s.chanMu.Lock()
	defer s.chanMu.Unlock()
	delete(s.channels, id)
}

func (s *ClassicStreamFactory) Shutdown() {
	s.once.Do(func() {
		s.closeMu.Lock()
		if s.closed.Load() {
			s.closeMu.Unlock()
			return
		}
		channels := make([]*TunnelConn, 0, len(s.channels))
		for _, conn := range s.channels {
			channels = append(channels, conn)
		}
		s.closeMu.Unlock()

		for _, conn := range channels {
			_ = conn.Close()
		}
		close(s.writeChan)
		s.Wait()
		s.closed.Store(true)
	})
}
