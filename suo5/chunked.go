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
)

type fullChunkedReadWriter struct {
	id         string
	reqBody    io.WriteCloser
	serverResp io.ReadCloser
	once       sync.Once

	readBuf  bytes.Buffer
	readTmp  []byte
	writeTmp []byte
}

// NewFullChunkedReadWriter 全双工读写流
func NewFullChunkedReadWriter(id string, reqBody io.WriteCloser, serverResp io.ReadCloser) io.ReadWriteCloser {
	rw := &fullChunkedReadWriter{
		id:         id,
		reqBody:    reqBody,
		serverResp: serverResp,
		readBuf:    bytes.Buffer{},
		readTmp:    make([]byte, 16*1024),
		writeTmp:   make([]byte, 8*1024),
	}
	return rw
}

func (s *fullChunkedReadWriter) Read(p []byte) (n int, err error) {
	if s.readBuf.Len() != 0 {
		return s.readBuf.Read(p)
	}
	fr, err := netrans.ReadFrame(s.serverResp)
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
	default:
		return 0, fmt.Errorf("unpected action when read %v", action)
	}
}

func (s *fullChunkedReadWriter) Write(p []byte) (n int, err error) {
	log.Debugf("write socket data, length: %d", len(p))
	body := BuildBody(NewActionData(s.id, p, ""))
	return s.WriteRaw(body)
}

func (s *fullChunkedReadWriter) WriteRaw(p []byte) (n int, err error) {
	return s.reqBody.Write(p)
}

func (s *fullChunkedReadWriter) Close() error {
	s.once.Do(func() {
		defer s.reqBody.Close()
		body := BuildBody(NewActionDelete(s.id, ""))
		_, _ = s.reqBody.Write(body)
		_ = s.serverResp.Close()
	})
	return nil
}

type halfChunkedReadWriter struct {
	ctx        context.Context
	id         string
	client     *http.Client
	serverResp io.ReadCloser
	once       sync.Once
	config     *Suo5Config

	readBuf  bytes.Buffer
	readTmp  []byte
	writeTmp []byte
}

// NewHalfChunkedReadWriter 半双工读写流, 用发送请求的方式模拟写
func NewHalfChunkedReadWriter(ctx context.Context, id string, client *http.Client, serverResp io.ReadCloser, config *Suo5Config) io.ReadWriteCloser {
	return &halfChunkedReadWriter{
		ctx:        ctx,
		id:         id,
		client:     client,
		serverResp: serverResp,
		config:     config,
		readBuf:    bytes.Buffer{},
		readTmp:    make([]byte, 16*1024),
		writeTmp:   make([]byte, 8*1024),
	}
}

func (s *halfChunkedReadWriter) Read(p []byte) (n int, err error) {
	if s.readBuf.Len() != 0 {
		return s.readBuf.Read(p)
	}
	fr, err := netrans.ReadFrame(s.serverResp)
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
		log.Debugf("recv data, length: %d", len(data))
		s.readBuf.Reset()
		s.readBuf.Write(data)
		return s.readBuf.Read(p)
	case ActionDelete:
		return 0, io.EOF
	case ActionHeartbeat:
		return 0, nil
	default:
		return 0, fmt.Errorf("unpected action when read %v", action)
	}
}

func (s *halfChunkedReadWriter) Write(p []byte) (n int, err error) {
	body := BuildBody(NewActionData(s.id, p, s.config.RedirectURL))
	log.Debugf("send request, length: %d", len(body))
	return s.WriteRaw(body)
}

func (s *halfChunkedReadWriter) WriteRaw(p []byte) (n int, err error) {
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

func (s *halfChunkedReadWriter) Close() error {
	s.once.Do(func() {
		body := BuildBody(NewActionDelete(s.id, s.config.RedirectURL))
		req, err := http.NewRequestWithContext(s.ctx, s.config.Method, s.config.Target, bytes.NewReader(body))
		if err != nil {
			log.Error(err)
			return
		}
		req.Header = s.config.Header.Clone()
		log.Debugf("send close request to %s", s.config.Target)
		resp, err := s.client.Do(req)
		if err != nil {
			log.Errorf("send close error: %v", err)
			return
		}
		_ = resp.Body.Close()
		_ = s.serverResp.Close()
		log.Debugf("close remote request done")
	})
	return nil
}
