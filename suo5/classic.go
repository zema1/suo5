package suo5

import (
	"bytes"
	"context"
	"fmt"
	log "github.com/kataras/golog"
	"github.com/pkg/errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ClassicStreamFactory struct {
	*BaseStreamFactory
	client *http.Client
}

func NewClassicStreamFactory(ctx context.Context, config *Suo5Config, client *http.Client) StreamFactory {
	s := &ClassicStreamFactory{
		BaseStreamFactory: NewBaseStreamFactory(ctx, config),
		client:            client,
	}

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
		if resp.ContentLength == 0 {
			log.Debugf("no data from server")
			return nil
		}

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			// todo: why listener eof
			if !strings.Contains(err.Error(), "unexpected EOF") {
				return errors.Wrap(errExpectedRetry, fmt.Sprintf("read body err, %s", err))
			}
		}
		err = s.DispatchRemoteData(bytes.NewReader(data))
		if err != nil {
			return errors.Wrap(errExpectedRetry, fmt.Sprintf("dispatch data err, %s", err))
		}

		return nil
	})

	go func() {
		for {
			select {
			case <-s.ctx.Done():
				return
			default:
				time.Sleep(time.Second * 5)
				s.BaseStreamFactory.tunnelMu.Lock()
				log.Debugf("connection count: %d", len(s.BaseStreamFactory.tunnels))
				s.BaseStreamFactory.tunnelMu.Unlock()
			}
		}
	}()
	return s
}

func (c *ClassicStreamFactory) Spawn(id, address string) (tunnel *TunnelConn, err error) {
	tunnel, err = c.BaseStreamFactory.Create(id)
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
	dialData := BuildBody(NewActionCreate(id, host, uint16(uport)), c.config.RedirectURL, c.config.SessionId, c.config.Mode)

	_, err = tunnel.WriteRaw(dialData)
	if err != nil {
		return nil, errors.Wrap(ErrDialFailed, err.Error())
	}

	// classic 只能通过轮询来获取远端数据
	tunnel.SetupActivePoll()

	// recv dial status
	serverData, err := tunnel.ReadUnmarshal()
	if err != nil {
		return nil, errors.Wrap(ErrDialFailed, err.Error())
	}

	status := serverData["s"]

	log.Debugf("recv dial response from server:  %v", status)
	if len(status) != 1 || status[0] != 0x00 {
		return nil, errors.Wrap(ErrConnRefused, fmt.Sprintf("status: %v", status))
	}

	return tunnel, nil
}

func (s *TunnelConn) SetupActivePoll() {
	ticker := time.NewTicker(time.Millisecond * 500)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				_, err := s.Write(nil)
				if err != nil {
					log.Error(err)
					return
				}
			}
		}
	}()
}
