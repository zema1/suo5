package suo5

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	log "github.com/kataras/golog"
	"github.com/zema1/suo5/netrans"
	"golang.org/x/time/rate"
	"io"
	"sync"
	"sync/atomic"
)

var _ StreamFactory = (*BaseStreamFactory)(nil)

var errExpectedRetry = errors.New("retry for error")

type StreamFactory interface {
	Spawn(id, address string) (*TunnelConn, error)
	Release(id string)
	Wait()
	Shutdown()
}

type IdData struct {
	id   string
	data []byte
}

type BaseStreamFactory struct {
	once    sync.Once
	config  *Suo5Config
	closeMu sync.Mutex
	closed  atomic.Bool
	limiter *rate.Limiter

	tunnelMu   sync.Mutex
	tunnels    map[string]*TunnelConn
	notifyOnce map[string]bool

	writeChan chan *IdData

	directWriteFunc func(string, []byte) error
	plexWriteFunc   func([]byte) error
	ctx             context.Context
	cancel          func()
}

func NewBaseStreamFactory(rootCtx context.Context, config *Suo5Config) *BaseStreamFactory {
	ctx, cancel := context.WithCancel(context.Background())

	limiter := rate.NewLimiter(rate.Limit(config.ClassicPollQPS), config.ClassicPollQPS)
	plex := &BaseStreamFactory{
		config:     config,
		limiter:    limiter,
		tunnels:    make(map[string]*TunnelConn),
		writeChan:  make(chan *IdData, 4096),
		notifyOnce: make(map[string]bool),
		ctx:        ctx,
		cancel:     cancel,
	}

	// 留点时间关闭远程连接
	go func() {
		select {
		case <-rootCtx.Done():
			log.Infof("start to cleanup remote connections")
			plex.Shutdown()
		case <-ctx.Done():
		}
	}()

	plex.sync()
	return plex
}

func (s *BaseStreamFactory) sync() {
	go func() {
		defer log.Infof("sync remote connection finished")
		defer s.Shutdown()

		// 等待 writeChan 里所有的数据都发完再 cancel，外层会 Wait() 住
		// 这里失败需要先 cancel
		defer s.cancel()

		buf := make([]byte, 0, s.config.MaxBodySize)

		for {
			select {
			case <-s.ctx.Done():
				return
			case idData, ok := <-s.writeChan:
				if !ok {
					return
				}
				if s.closed.Load() {
					return
				}
				if s.directWriteFunc != nil {
					log.Debugf("write to remote, id: %s, data: %d", idData.id, len(idData.data))
					if err := s.directWriteFunc(idData.id, idData.data); err != nil {
						log.Errorf("failed to write to remote, %v", err)
						s.tunnelMu.Lock()
						if conn, ok := s.tunnels[idData.id]; ok {
							s.tunnelMu.Unlock()
							_ = conn.Close()
						} else {
							s.tunnelMu.Unlock()
						}
						s.Release(idData.id)

					}
					continue
				}
				err := s.limiter.Wait(s.ctx)
				if err != nil {
					return
				}
				if s.closed.Load() {
					return
				}

				buf = append(buf[:0], idData.data...)
				size := len(s.writeChan)
				if size > 0 {
					for i := 0; i < size; i++ {
						tmp := <-s.writeChan
						buf = append(buf, tmp.data...)
						if len(buf) > s.config.MaxBodySize {
							break
						}
					}
				}
				if len(buf) == 0 {
					log.Debugf("empty data sent to remote")
					continue
				}
				if s.plexWriteFunc == nil {
					log.Errorf("write to remote handle is nil")
					return
				}

				success := false
				bufCopy := make([]byte, len(buf))
				copy(bufCopy, buf)
				for i := 0; i <= s.config.RetryCount; i++ {
					err = s.plexWriteFunc(bufCopy)
					if err == nil {
						success = true
						break
					}
					if errors.Is(err, errExpectedRetry) {
						log.Debugf("plex write %s, retry %d/%d", err, i, s.config.RetryCount)
						continue
					} else {
						if !errors.Is(err, context.Canceled) {
							log.Errorf("failed to write plex data to remote, %v", err)
						}
						return
					}
				}
				if !success {
					log.Errorf("failed to write plex data to remote, retry limit exceeded, consider to increase retry count?")
					return
				}
			}
		}
	}()
}

func (s *BaseStreamFactory) OnRemotePlexWrite(plexWriteFunc func([]byte) error) {
	s.plexWriteFunc = plexWriteFunc
}

func (s *BaseStreamFactory) OnRemoteWrite(idWriteFunc func(string, []byte) error) {
	s.directWriteFunc = idWriteFunc
}

func (s *BaseStreamFactory) DispatchRemoteData(reader io.Reader) error {
	for {
		if rd, ok := reader.(*bytes.Reader); ok {
			if rd.Len() == 0 {
				break
			}
		}

		fr, err := netrans.ReadFrameBase64(reader)
		if err != nil {
			return err
		}
		m, err := Unmarshal(fr.Data)
		if err != nil {
			return err
		}
		id := string(m["id"])
		if id == "" {
			log.Warnf("empty id in data packet, packet will be dropped")
			continue
		}
		actions := m["ac"]
		if len(actions) != 1 {
			return fmt.Errorf("invalid action when read %v", actions)
		}
		log.Debugf("recv data from remote, id: %s, action: %v, data: %d", id, actions, len(m["dt"]))

		s.tunnelMu.Lock()
		conn, ok := s.tunnels[id]
		if !ok {
			// send only once for each id
			if s.notifyOnce[id] {
				s.tunnelMu.Unlock()
				continue
			}
			s.notifyOnce[id] = true
			s.tunnelMu.Unlock()

			log.Warnf("id %s not found, notify remote to close", id)
			body := BuildBody(NewActionDelete(id), s.config.RedirectURL, s.config.SessionId, s.config.Mode)

			// todo: 还是会存在 writeChan close 的情况
			if s.closed.Load() {
				return nil
			}
			select {
			case s.writeChan <- &IdData{id, body}:
			case <-s.ctx.Done():
				return nil
			default:
				log.Warnf("writeChan is full, discard message")
			}
			continue
		}
		s.tunnelMu.Unlock()
		conn.RemoteData(m)
	}
	return nil
}

func (s *BaseStreamFactory) Create(id string) (*TunnelConn, error) {
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	if s.closed.Load() {
		return nil, ErrFactoryStopped
	}
	newConn := NewTunnelConn(id, s.config, s.writeChan)
	newConn.AddCloseCallback(func() {
		s.Release(id)
	})
	s.tunnels[id] = newConn
	return newConn, nil
}

func (s *BaseStreamFactory) Spawn(id, target string) (*TunnelConn, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *BaseStreamFactory) Wait() {
	<-s.ctx.Done()
}

func (s *BaseStreamFactory) Release(id string) {
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	delete(s.tunnels, id)
}

func (s *BaseStreamFactory) Shutdown() {
	s.once.Do(func() {
		if s.closed.Load() {
			return
		}
		channels := make([]*TunnelConn, 0, len(s.tunnels))
		for _, conn := range s.tunnels {
			channels = append(channels, conn)
		}

		for _, conn := range channels {
			_ = conn.Close()
		}
		s.closed.Store(true)
		close(s.writeChan)
		s.Wait()
	})
}
