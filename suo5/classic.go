package suo5

import (
	"bytes"
	"context"
	"fmt"
	log "github.com/kataras/golog"
	"github.com/zema1/suo5/netrans"
	"io"
	"net/http"
	"sync"
	"time"
)

type ClassicReadWriter struct {
	id       string
	config   *Suo5Config
	client   *http.Client
	once     sync.Once
	readBuf  bytes.Buffer
	readTmp  []byte
	writeTmp []byte

	ctx    context.Context
	cancel func()
}

func NewClassicReadWriter(rootCtx context.Context, id string, client *http.Client, config *Suo5Config) *ClassicReadWriter {
	ctx, cancel := context.WithCancel(rootCtx)
	rw := &ClassicReadWriter{
		id:     id,
		config: config,
		client: client,
		ctx:    ctx,
		cancel: cancel,
	}
	return rw
}

func (s *ClassicReadWriter) Read(p []byte) (n int, err error) {
	if s.readBuf.Len() != 0 {
		return s.readBuf.Read(p)
	}
	rawData, err := s.readRaw()
	if err != nil {
		return 0, err
	}
	if len(rawData) == 0 {
		time.Sleep(time.Millisecond * 300)
		return 0, nil
	}
	fr, err := netrans.ReadFrame(bytes.NewReader(rawData))
	if err != nil {
		return 0, err
	}
	m, err := Unmarshal(fr.Data)
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
		return 0, io.EOF
	case ActionHeartbeat:
		return 0, nil
	default:
		return 0, fmt.Errorf("unepected action when read %v", action)
	}
}

func (s *ClassicReadWriter) readRaw() ([]byte, error) {
	log.Debugf("send read request")
	// todo: read and write in one request
	body := BuildBody(NewActionRead(s.id, s.config.RedirectURL))
	req, err := http.NewRequestWithContext(s.ctx, s.config.Method, s.config.Target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = s.config.Header.Clone()
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected status of %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1024*1024*10))
}

func (s *ClassicReadWriter) Write(p []byte) (n int, err error) {
	body := BuildBody(NewActionData(s.id, p, s.config.RedirectURL))
	log.Debugf("send request, length: %d", len(body))
	return s.WriteRaw(body)
}

func (s *ClassicReadWriter) WriteRaw(p []byte) (n int, err error) {
	req, err := http.NewRequestWithContext(s.ctx, s.config.Method, s.config.Target, bytes.NewReader(p))
	if err != nil {
		return 0, err
	}
	req.ContentLength = int64(len(p))
	req.Header = s.config.Header.Clone()
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		return len(p), nil
	} else {
		return 0, fmt.Errorf("unexpected status of %d", resp.StatusCode)
	}
}

func (s *ClassicReadWriter) Close() error {
	s.once.Do(func() {
		body := BuildBody(NewActionDelete(s.id, s.config.RedirectURL))
		req, err := http.NewRequestWithContext(s.ctx, s.config.Method, s.config.Target, bytes.NewReader(body))
		if err != nil {
			log.Error(err)
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
