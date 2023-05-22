package ctrl

import (
	"bytes"
	"context"
	"fmt"
	"github.com/gorilla/websocket"
	log "github.com/kataras/golog"
	"github.com/zema1/suo5/netrans"
	"strings"
	"sync"
	"time"
)

const websocketBufferSize = 2*1024*1024 + 1024

type WebsocketMultiplex struct {
	wsConn *websocket.Conn
	config *Suo5Config

	mu        sync.Mutex
	channels  map[string]*TunnelConn
	writeChan chan []byte
	closed    bool
	ctx       context.Context
	cancel    func()
}

func NewWebsocketMultiplex(rootCtx context.Context, wsConn *websocket.Conn, config *Suo5Config) *WebsocketMultiplex {
	ctx, cancel := context.WithCancel(context.Background())

	plex := &WebsocketMultiplex{
		wsConn:    wsConn,
		channels:  make(map[string]*TunnelConn),
		writeChan: make(chan []byte, 10),
		ctx:       ctx,
		cancel:    cancel,
		config:    config,
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				time.Sleep(time.Second * 5)
				plex.mu.Lock()
				log.Infof("conn count: %d", len(plex.channels))
				plex.mu.Unlock()
			}
		}
	}()

	// 留3s时间关闭远程连接
	go func() {
		select {
		case <-rootCtx.Done():
			// 先等 socks5 服务关了
			time.Sleep(time.Second)
			plex.Close()
		case <-ctx.Done():
		}
	}()

	plex.init()
	return plex
}

func (s *WebsocketMultiplex) init() {
	go func() {
		defer s.cancel()
		defer s.Close()
		for {
			select {
			case <-s.ctx.Done():
				return
			case data, ok := <-s.writeChan:
				if !ok {
					return
				}
				log.Debugf("write ws data %d", len(data))
				err := s.wsConn.WriteMessage(websocket.BinaryMessage, data)
				if err != nil {
					log.Errorf("write ws message failed: %v, proxy shutdown", err)
					// 这里不能直接 return，要不然就没有消费 message 的了
					continue
				}
			}
		}
	}()

	go func() {
		defer s.Close()
		for {
			typ, data, err := s.wsConn.ReadMessage()
			if err != nil {
				if !strings.Contains(err.Error(), "closed network") {
					log.Errorf("read ws message failed, proxy shutdown, err: %s", err)
				} else {
					log.Infof("read ws message finished, proxy shutdown")
				}
				return
			}
			if typ != websocket.BinaryMessage {
				continue
			}
			fr, err := netrans.ReadFrame(bytes.NewBuffer(data))
			if err != nil {
				log.Errorf("read frame failed: %v", err)
				return
			}
			m, err := unmarshal(fr.Data)
			if err != nil {
				log.Errorf("unmarshal message failed: %v", err)
				return
			}
			if len(m["ac"]) != 0 && m["ac"][0] == ActionHeartbeat {
				continue
			}
			log.Debugf("recv ws data %d %d", len(data), len(m["dt"]))
			id := string(m["id"])
			if id == "" {
				log.Errorf("empty id %v", m)
				continue
			}
			s.mu.Lock()
			conn, ok := s.channels[id]
			if !ok {
				action := m["ac"]
				if len(action) != 1 {
					log.Errorf("invalid action when read %v", action)
					s.mu.Unlock()
					continue
				}
				if action[0] != ActionDelete {
					log.Warnf("id %s not found, notify remote to close", id)
					body := buildBody(newDelete(id, ""))
					s.writeChan <- body
				}
				s.mu.Unlock()
				continue
			}
			s.mu.Unlock()
			conn.readChan <- m
		}
	}()
}

func (s *WebsocketMultiplex) Spawn(id string) (*TunnelConn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("websocket has closed, please retry")
	}
	newConn := &TunnelConn{
		id:        id,
		readChan:  make(chan map[string][]byte, 10),
		writeChan: s.writeChan,
		readBuf:   bytes.Buffer{},
		onClose: func() {
			s.Release(id)
		},
		chunkSize: s.config.MaxRequestSize,
	}
	s.channels[id] = newConn
	return newConn, nil
}

func (s *WebsocketMultiplex) Wait() {
	<-s.ctx.Done()
}

func (s *WebsocketMultiplex) Release(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wsConn := s.channels[id]
	if wsConn != nil {
		close(wsConn.readChan)
	}
	delete(s.channels, id)
}

func (s *WebsocketMultiplex) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	channels := make([]*TunnelConn, 0, len(s.channels))
	for _, conn := range s.channels {
		channels = append(channels, conn)
	}
	s.mu.Unlock()
	for _, conn := range channels {
		_ = conn.Close()
	}
	close(s.writeChan)
}
