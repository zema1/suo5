package suo5

import (
	"bytes"
	"context"
	"fmt"
	log "github.com/kataras/golog"
	"github.com/pkg/errors"
	"github.com/zema1/rawhttp"
	"github.com/zema1/suo5/netrans"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync"
	"time"
)

type FullChunkedStreamFactory struct {
	*BaseStreamFactory
	mu        sync.Mutex
	rawClient *rawhttp.Client
	rcs       map[string]io.ReadCloser
	wcs       map[string]io.WriteCloser
}

func NewFullChunkedStreamFactory(ctx context.Context, config *Suo5Config, rawClient *rawhttp.Client) StreamFactory {
	s := &FullChunkedStreamFactory{
		BaseStreamFactory: NewBaseStreamFactory(ctx, config),
		rawClient:         rawClient,
		rcs:               make(map[string]io.ReadCloser),
		wcs:               make(map[string]io.WriteCloser),
	}

	go func() {
		for {
			select {
			case <-s.ctx.Done():
				return
			default:
				time.Sleep(time.Second * 5)
				s.mu.Lock()
				log.Debugf("connection count: r: %d w: %d", len(s.rcs), len(s.wcs))
				s.mu.Unlock()
			}
		}
	}()

	s.OnRemoteWrite(func(id string, p []byte) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		conn, ok := s.wcs[id]
		if !ok {
			rc := s.rcs[id]
			if rc != nil {
				_ = rc.Close()
			}
			return nil
		}
		_, err := conn.Write(p)
		return err
	})
	return s
}

func (h *FullChunkedStreamFactory) Spawn(id, address string) (tunnel *TunnelConn, err error) {
	tunnel, err = h.BaseStreamFactory.Create(id)
	if err != nil {
		return nil, err
	}

	tunnelRef := tunnel
	defer func() {
		if err != nil && tunnelRef != nil {
			tunnelRef.CloseSelf()
		}
	}()

	host, port, _ := net.SplitHostPort(address)
	uport, _ := strconv.Atoi(port)
	dialData := BuildBody(NewActionCreate(id, host, uint16(uport)), h.config.RedirectURL, h.config.SessionId, h.config.Mode)

	ch, wc := netrans.NewChannelWriteCloser(h.ctx)
	body := netrans.MultiReadCloser(
		io.NopCloser(bytes.NewReader(dialData)),
		io.NopCloser(netrans.NewChannelReader(ch)),
	)
	req := h.config.NewRequest(h.ctx, body, 0)
	resp, err := h.rawClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(ErrDialFailed, err.Error())
	}

	serverData, bufData, err := UnmarshalFrameWithBuffer(resp.Body)
	if err != nil {
		bodyData, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		bufData = append(bufData, bodyData...)
		header, _ := httputil.DumpResponse(resp, false)
		return nil, fmt.Errorf("%s, response is:\n%s", err, string(header)+string(bufData))
	}

	status := serverData["s"]

	log.Debugf("recv dial response from server:  %v", status)
	if len(status) != 1 || status[0] != 0x00 {
		return nil, errors.Wrap(ErrConnRefused, fmt.Sprintf("status: %v", status))
	}

	cleanUp := func() {
		_ = resp.Body.Close()
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.rcs, id)
		delete(h.wcs, id)
	}

	tunnel.AddCloseCallback(cleanUp)

	go func() {
		defer cleanUp()

		err := h.DispatchRemoteData(resp.Body)
		if err != nil && !errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "use of closed network") {
			log.Errorf("dispatch remote data error: %v", err)
		}
	}()

	h.mu.Lock()
	defer h.mu.Unlock()
	h.rcs[id] = resp.Body
	h.wcs[id] = wc
	return tunnel, nil
}

type HalfChunkedStreamFactory struct {
	*BaseStreamFactory
	client *http.Client
	mu     sync.Mutex
	rcs    map[string]io.ReadCloser
}

func NewHalfChunkedStreamFactory(ctx context.Context, config *Suo5Config, client *http.Client) StreamFactory {
	s := &HalfChunkedStreamFactory{
		BaseStreamFactory: NewBaseStreamFactory(ctx, config),
		client:            client,
		rcs:               make(map[string]io.ReadCloser),
	}

	go func() {
		for {
			select {
			case <-s.ctx.Done():
				return
			default:
				time.Sleep(time.Second * 5)
				s.mu.Lock()
				log.Debugf("connection count: %d", len(s.rcs))
				s.mu.Unlock()
			}
		}
	}()

	s.OnRemotePlexWrite(func(p []byte) error {
		log.Debugf("send remote write request, body len: %d", len(p))
		req := s.config.NewRequest(s.ctx, bytes.NewReader(p), int64(len(p)))
		resp, err := s.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return errors.Wrap(errExpectedRetry, fmt.Sprintf("unexpected status of %d", resp.StatusCode))
		}
		return nil
	})
	return s
}

func (h *HalfChunkedStreamFactory) Spawn(id, address string) (tunnel *TunnelConn, err error) {
	tunnel, err = h.BaseStreamFactory.Create(id)
	if err != nil {
		return nil, err
	}

	tunnelRef := tunnel
	defer func() {
		if err != nil && tunnelRef != nil {
			tunnelRef.CloseSelf()
		}
	}()

	host, port, _ := net.SplitHostPort(address)
	uport, _ := strconv.Atoi(port)
	var status []byte
	var resp *http.Response

	for i := 0; i <= h.config.RetryCount; i++ {
		dialData := BuildBody(NewActionCreate(id, host, uint16(uport)), h.config.RedirectURL, h.config.SessionId, h.config.Mode)
		req := h.config.NewRequest(h.ctx, bytes.NewReader(dialData), int64(len(dialData)))
		resp, err = h.client.Do(req)
		if err != nil {
			return nil, errors.Wrap(ErrDialFailed, err.Error())
		}

		serverData, bufData, err := UnmarshalFrameWithBuffer(resp.Body)
		if err != nil {
			bodyData, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			bufData = append(bufData, bodyData...)
			header, _ := httputil.DumpResponse(resp, false)
			log.Debugf("unmarshal frame data failed, retry %d/%d, response is:\n%s", i, h.config.RetryCount, string(header)+string(bufData))
			continue
		}

		status = serverData["s"]
		break
	}
	if len(status) == 0 {
		return nil, errors.Wrap(ErrDialFailed, "retry limit exceeded, consider to increase retry count?")
	}

	log.Debugf("recv dial response from server:  %v", status)
	if len(status) != 1 || status[0] != 0x00 {
		return nil, errors.Wrap(ErrConnRefused, fmt.Sprintf("status: %v", status))
	}

	cleanUp := func() {
		_ = resp.Body.Close()
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.rcs, id)
	}

	tunnel.AddCloseCallback(cleanUp)

	go func() {
		defer cleanUp()

		err := h.DispatchRemoteData(resp.Body)
		if err != nil && !errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "use of closed network") {
			log.Errorf("dispatch remote data error: %v", err)
		}
	}()

	h.mu.Lock()
	defer h.mu.Unlock()
	h.rcs[id] = resp.Body
	return tunnel, nil
}
