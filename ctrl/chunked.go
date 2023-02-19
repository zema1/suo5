package ctrl

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
	serverResp io.Reader
	once       sync.Once
	chunked    bool

	readBuf  bytes.Buffer
	readTmp  []byte
	writeTmp []byte
}

// NewFullChunkedReadWriter 全双工读写流
func NewFullChunkedReadWriter(id string, reqBody io.WriteCloser, serverResp io.Reader) io.ReadWriter {
	return &fullChunkedReadWriter{
		id:         id,
		reqBody:    reqBody,
		serverResp: serverResp,
		readBuf:    bytes.Buffer{},
		readTmp:    make([]byte, 16*1024),
		writeTmp:   make([]byte, 8*1024),
	}
}

func (s *fullChunkedReadWriter) Read(p []byte) (n int, err error) {
	if s.readBuf.Len() != 0 {
		return s.readBuf.Read(p)
	}
	fr, err := netrans.ReadFrame(s.serverResp)
	if err != nil {
		return 0, err
	}
	m, err := unmarshal(fr.Data)
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
	log.Debugf("write data, length: %d", len(p))
	body := buildBody(newActionData(s.id, p))
	return s.reqBody.Write(body)
}

func (s *fullChunkedReadWriter) Close() error {
	s.once.Do(func() {
		defer s.reqBody.Close()
		body := buildBody(newDelete(s.id))
		_, _ = s.reqBody.Write(body)
	})
	return nil
}

type halfChunkedReadWriter struct {
	ctx        context.Context
	id         string
	client     *http.Client
	target     string
	serverResp io.Reader
	once       sync.Once
	chunked    bool
	baseHeader http.Header

	readBuf  bytes.Buffer
	readTmp  []byte
	writeTmp []byte
}

// NewHalfChunkedReadWriter 半双工读写流, 用发送请求的方式模拟写
func NewHalfChunkedReadWriter(ctx context.Context, id string, client *http.Client, target string, serverResp io.Reader, baseHeader http.Header) io.ReadWriter {
	return &halfChunkedReadWriter{
		ctx:        ctx,
		id:         id,
		client:     client,
		target:     target,
		serverResp: serverResp,
		readBuf:    bytes.Buffer{},
		readTmp:    make([]byte, 16*1024),
		writeTmp:   make([]byte, 8*1024),
		baseHeader: baseHeader,
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
	m, err := unmarshal(fr.Data)
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

func (s *halfChunkedReadWriter) Write(p []byte) (n int, err error) {
	body := buildBody(newActionData(s.id, p))
	log.Debugf("send request, length: %d", len(body))
	req, err := http.NewRequestWithContext(s.ctx, http.MethodPost, s.target, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	if s.chunked {
		req.ContentLength = -1
	} else {
		req.ContentLength = int64(len(body))
	}
	req.Header = s.baseHeader.Clone()
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
		body := buildBody(newDelete(s.id))
		req, err := http.NewRequestWithContext(s.ctx, http.MethodPost, s.target, bytes.NewReader(body))
		if err != nil {
			log.Error(err)
			return
		}
		req.Header = s.baseHeader.Clone()
		resp, err := s.client.Do(req)
		if err != nil {
			log.Errorf("send close error: %v", err)
			return
		}
		_ = resp.Body.Close()
	})
	return nil
}
