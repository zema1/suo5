package ctrl

import (
	"bytes"
	"fmt"
	"io"
	"sync"
)

type MultiPlexer interface {
	Spawn(id string) (*TunnelConn, error)
	Release(id string)
	Wait()
	Close()
}

type TunnelConn struct {
	id        string
	mu        sync.Mutex
	once      sync.Once
	readBuf   bytes.Buffer
	readChan  chan map[string][]byte
	writeChan chan []byte
	onClose   func()
	redirect  string
	chunkSize int
}

func (s *TunnelConn) ReadUnmarshal() (map[string][]byte, error) {
	m, ok := <-s.readChan
	if !ok {
		return nil, io.EOF
	}
	return m, nil
}

func (s *TunnelConn) Read(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
		return 0, io.EOF
	default:
		return 0, fmt.Errorf("unpected action when read %v", action)
	}
}

func (s *TunnelConn) Write(p []byte) (n int, err error) {
	partWrite := 0
	if len(p) > s.chunkSize {
		for i := 0; i < len(p); i += s.chunkSize {
			body := buildBody(newActionData(s.id, p[i:min(i+s.chunkSize, len(p))], s.redirect))
			n, err = s.WriteRaw(body)
			if err != nil {
				return partWrite, err
			}
			partWrite += n
		}
		return partWrite, nil
	} else {
		body := buildBody(newActionData(s.id, p, s.redirect))
		return s.WriteRaw(body)
	}
}

func (s *TunnelConn) WriteRaw(p []byte) (n int, err error) {
	s.writeChan <- p
	return len(p), nil
}

func (s *TunnelConn) Close() error {
	s.once.Do(func() {
		body := buildBody(newDelete(s.id, s.redirect))
		_, _ = s.WriteRaw(body)
		if s.onClose != nil {
			s.onClose()
		}
	})
	return nil
}
