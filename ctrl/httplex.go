package ctrl

import (
	"bytes"
	"context"
	"fmt"
	log "github.com/kataras/golog"
	"github.com/zema1/suo5/netrans"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type HttpMultiplexHalf struct {
	config     *Suo5Config
	client     *http.Client
	serverResp io.ReadCloser

	mu        sync.Mutex
	channels  map[string]*TunnelConn
	writeChan chan []byte
	closed    bool
	ctx       context.Context
	cancel    func()
}

func NewHttpMultiplexHalf(rootCtx context.Context, config *Suo5Config, client *http.Client, serverResp io.ReadCloser) *HttpMultiplexHalf {
	ctx, cancel := context.WithCancel(context.Background())

	plex := &HttpMultiplexHalf{
		config:     config,
		client:     client,
		serverResp: serverResp,
		channels:   make(map[string]*TunnelConn),
		writeChan:  make(chan []byte, 4096),
		ctx:        ctx,
		cancel:     cancel,
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

func (s *HttpMultiplexHalf) init() {
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
					continue
				}
				if err := s.sendRequest(data, 0); err != nil {
					log.Errorf("failed to send request: %v", err)
					return
				}
				time.Sleep(time.Duration(s.config.RequestInterval) * time.Millisecond)
			}
		}
	}()

	go func() {
		defer s.Close()
		notfoundMap := make(map[string]bool)
		for {
			fr, err := netrans.ReadFrame(s.serverResp)
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
			id := string(m["id"])
			if id == "" {
				log.Errorf("empty id %v", m)
				continue
			}
			log.Infof("recv data from remote %d bytes", len(fr.Data))
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
					if _, ok := notfoundMap[id]; !ok {
						notfoundMap[id] = true
						log.Warnf("id %s not found, notify remote to close", id)
						body := buildBody(newDelete(id, s.config.RedirectURL))
						select {
						case s.writeChan <- body:
						default:
							log.Warnf("writeChan is full, discard message")
						}
					} else {
						//log.Warnf("drop message from %s", id)
					}
				}
				s.mu.Unlock()
				continue
			}
			s.mu.Unlock()
			conn.readChan <- m
		}
	}()
}

func (s *HttpMultiplexHalf) sendRequest(body []byte, count int) error {
	if count > s.config.MaxRetry {
		return fmt.Errorf("failed to send request after retry %d times", count)
	}
	req, err := http.NewRequestWithContext(s.ctx, s.config.Method, s.config.Target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(body))
	req.Header = s.config.Header.Clone()
	req.Header.Set(HeaderKey, HeaderValuePlexHalf)
	req.Close = true
	resp, err := s.client.Do(req)
	if err != nil {
		log.Infof("send request failed, err: %s, retrying %d, bodysize: %d", err, count, len(body))
		return s.sendRequest(body, count+1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		log.Infof("send request failed, status: %d retrying %d, bodysize: %d", resp.StatusCode, count, len(body))
		return s.sendRequest(body, count+1)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 150))
	if err != nil {
		return err
	}
	result := string(data)
	if strings.Contains(result, "nginx/1.22.0") && strings.Contains(result, "</center>  ") {
		log.Infof("send request to remote success, %d bytes", len(body))
		return nil
	}
	log.Infof("send request failed, body: %s, retrying %d, bodysize: %d", result, count, len(body))
	return s.sendRequest(body, count+1)
}

func (s *HttpMultiplexHalf) Spawn(id string) (*TunnelConn, error) {
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
		redirect:  s.config.RedirectURL,
		chunkSize: 1024 * 256,
	}
	s.channels[id] = newConn
	return newConn, nil
}

func (s *HttpMultiplexHalf) Wait() {
	<-s.ctx.Done()
}

func (s *HttpMultiplexHalf) Release(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wsConn := s.channels[id]
	if wsConn != nil {
		close(wsConn.readChan)
	}
	delete(s.channels, id)
}

func (s *HttpMultiplexHalf) Close() {
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
