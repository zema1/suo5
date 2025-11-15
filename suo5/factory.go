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
	id      string
	data    []byte
	noDelay bool
}

type IdWriteFunc func(idata *IdData) error

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

	directWriteFunc IdWriteFunc
	plexWriteFunc   func([]byte) error
	ctx             context.Context
	cancel          func()
}

func NewBaseStreamFactory(rootCtx context.Context, config *Suo5Config) *BaseStreamFactory {
	ctx, cancel := context.WithCancel(context.Background())

	limiter := rate.NewLimiter(rate.Limit(config.ClassicPollQPS), config.ClassicPollQPS)
	fac := &BaseStreamFactory{
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
			fac.Shutdown()
		case <-ctx.Done():
		}
	}()

	fac.startPlex()
	return fac
}

func (s *BaseStreamFactory) startPlex() {
	go func() {
		defer log.Infof("sync remote connection finished")
		defer s.Shutdown()

		// 等待 writeChan 里所有的数据都发完再 cancel，外层会 Wait() 住
		// 这里失败需要先 cancel
		defer s.cancel()

		noDelayPlexWrite := func(data []byte) {
			log.Debugf("no delay write to remote, data: %d", len(data))
			go func() {
				if err := s.reliablePlexWrite(data); err != nil {
					log.Errorf("failed to write plex data to remote, %v", err)
				}
			}()
		}

		buf := make([]byte, 0, s.config.MaxBodySize)

		for {
			select {
			case <-s.ctx.Done():
				return
			case idData, ok := <-s.writeChan:
				if !ok {
					return
				}
				if s.directWriteFunc != nil {
					err := s.directWriteFunc(idData)
					if err != nil {
						log.Errorf("failed to write direct data to remote, %v", err)
					}
					continue
				}
				if idData.noDelay {
					noDelayPlexWrite(idData.data)
					continue
				}

				err := s.limiter.Wait(s.ctx)
				if err != nil {
					return
				}

				buf = append(buf[:0], idData.data...)
				size := len(s.writeChan)
				if size > 0 {
					for i := 0; i < size; i++ {
						tmp := <-s.writeChan
						if tmp.noDelay {
							noDelayPlexWrite(tmp.data)
							continue
						}
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

				bufCopy := make([]byte, len(buf))
				copy(bufCopy, buf)

				err = s.reliablePlexWrite(bufCopy)
				if err != nil {
					log.Errorf("failed to write plex data to remote 2, %v", err)
					return
				}
			}
		}
	}()
}

func (s *BaseStreamFactory) OnRemotePlexWrite(plexWriteFunc func([]byte) error) {
	s.plexWriteFunc = plexWriteFunc
}

func (s *BaseStreamFactory) OnRemoteDirectWrite(idWriteFunc IdWriteFunc) {
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

		actions := m["ac"]
		if len(actions) != 1 {
			return fmt.Errorf("invalid action when read %v", actions)
		}

		if actions[0] == ActionDirty {
			log.Debugf("recv dirty chunk, size %d", len(m["d"]))
			continue
		}

		id := string(m["id"])
		if id == "" {
			log.Warnf("empty id in data packet, packet will be dropped, action: %v", actions[0])
			continue
		}
		log.Debugf("recv data from remote, id: %s, action: %v, data: %d", id, actions, len(m["dt"]))

		if actions[0] == ActionHeartbeat {
			log.Debugf("received heartbeat from remote, id: %s", id)
			continue
		}

		s.tunnelMu.Lock()
		conn, ok := s.tunnels[id]
		if ok {
			// 找到连接，正常分发数据
			s.tunnelMu.Unlock()
			conn.RemoteData(m)
			continue
		}

		// send only once for each id
		if s.notifyOnce[id] {
			s.tunnelMu.Unlock()
			continue
		}
		s.notifyOnce[id] = true
		s.tunnelMu.Unlock()

		log.Warnf("id %s not found, notify remote to close", id)
		body := BuildBody(NewActionDelete(id), s.config.RedirectURL, s.config.SessionId, s.config.Mode)

		s.closeMu.Lock()
		if s.closed.Load() {
			s.closeMu.Unlock()
			return nil
		}

		select {
		case s.writeChan <- &IdData{id, body, false}:
		default:
			log.Warnf("writeChan is full, discard message")
		}
		s.closeMu.Unlock()
	}
	return nil
}

func (s *BaseStreamFactory) reliablePlexWrite(data []byte) error {
	success := false
	for i := 0; i <= s.config.RetryCount; i++ {
		err := s.plexWriteFunc(data)
		if err == nil {
			success = true
			break
		}
		log.Infof("failed to write plex data, retrying %d/%d, %s", i, s.config.RetryCount, err)
	}
	if !success {
		return fmt.Errorf("retry limit exceeded, consider to increase retry count")
	}
	return nil
}

func (s *BaseStreamFactory) Create(id string) (*TunnelConn, error) {
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	if s.closed.Load() {
		return nil, ErrFactoryStopped
	}
	newConn := NewTunnelConn(id, s.config, func(idata *IdData) error {
		select {
		case s.writeChan <- idata:
			return nil
		default:
			return fmt.Errorf("discard data as write buffer is full, id %s,  len %d", idata.id, len(idata.data))
		}
	})
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
	delete(s.notifyOnce, id)
}

func (s *BaseStreamFactory) Shutdown() {
	s.once.Do(func() {
		s.closed.Store(true)

		s.tunnelMu.Lock()
		channels := make([]*TunnelConn, 0, len(s.tunnels))
		for _, conn := range s.tunnels {
			channels = append(channels, conn)
		}
		s.tunnelMu.Unlock()

		for _, conn := range channels {
			_ = conn.Close()
		}
		s.closeMu.Lock()
		close(s.writeChan)
		s.closeMu.Unlock()
		s.Wait()
	})
}
