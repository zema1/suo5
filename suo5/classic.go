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
		log.Debugf("send polling request, body len: %d", len(p))
		req, err := http.NewRequestWithContext(s.ctx, s.config.Method, s.config.Target, bytes.NewReader(p))
		if err != nil {
			return err
		}
		req.ContentLength = int64(len(p))
		req.Header = s.config.Header.Clone()
		resp, err := s.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("unexpected status of %d", resp.StatusCode)
		}
		if resp.ContentLength == 0 {
			return nil
		}

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		return s.DispatchRemoteData(bytes.NewReader(data))
	})

	go func() {
		for {
			select {
			case <-s.ctx.Done():
				return
			default:
				time.Sleep(time.Second * 5)
				s.BaseStreamFactory.tunnelMu.Lock()
				log.Infof("connection count: %d", len(s.BaseStreamFactory.tunnels))
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
			_ = tunnelRef.Close()
		}
	}()

	host, port, _ := net.SplitHostPort(address)
	uport, _ := strconv.Atoi(port)
	dialData := BuildBody(NewActionCreate(id, host, uint16(uport)), c.config.RedirectURL, c.config.Mode)

	_, err = tunnel.WriteRaw(dialData)
	if err != nil {
		return nil, errors.Wrap(ErrDialFailed, err.Error())
	}
	tunnel.SetupConnHeartBeat()

	// recv dial status
	serverData, err := tunnel.ReadUnmarshal()
	if err != nil {
		return nil, errors.Wrap(ErrDialFailed, err.Error())
	}

	if err != nil {
		return nil, errors.Wrap(ErrDialFailed, err.Error())
	}
	status := serverData["s"]

	log.Debugf("recv dial response from server:  %v", status)
	if len(status) != 1 || status[0] != 0x00 {
		return nil, errors.Wrap(ErrHostUnreachable, fmt.Sprintf("status: %v", status))
	}

	return tunnel, nil
}

func (s *TunnelConn) SetupConnHeartBeat() {
	// todo: speed limit
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
