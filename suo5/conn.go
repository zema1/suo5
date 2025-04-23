package suo5

import (
	"bytes"
	"context"
	"fmt"
	log "github.com/kataras/golog"
	"github.com/pkg/errors"
	"github.com/zema1/suo5/netrans"
	"io"
	"net"
	"net/http"
	"strconv"
)

var (
	ErrHostUnreachable = errors.New("host unreachable")
	ErrDialFailed      = errors.New("dial failed")
	ErrConnRefused     = errors.New("connection refused")
)

func NewSuo5Conn(ctx context.Context, client *Suo5Client) *Suo5Conn {
	return &Suo5Conn{
		ctx:        ctx,
		Suo5Client: client,
	}
}

type Suo5Conn struct {
	io.ReadWriteCloser
	ctx context.Context
	*Suo5Client
}

func (suo *Suo5Conn) Connect(address string) error {
	id := RandString(8)
	var req *http.Request
	var resp *http.Response
	var err error
	host, port, _ := net.SplitHostPort(address)
	uport, _ := strconv.Atoi(port)
	dialData := BuildBody(NewActionCreate(id, host, uint16(uport)), suo.Config.RedirectURL, suo.Config.Mode)
	ch, chWR := netrans.NewChannelWriteCloser(suo.ctx)

	baseHeader := suo.Config.Header.Clone()

	if suo.Config.Mode == FullDuplex {
		body := netrans.MultiReadCloser(
			io.NopCloser(bytes.NewReader(dialData)),
			io.NopCloser(netrans.NewChannelReader(ch)),
		)
		req, _ = http.NewRequestWithContext(suo.ctx, suo.Config.Method, suo.Config.Target, body)
		req.Header = baseHeader
		resp, err = suo.RawClient.Do(req)
	} else if suo.Config.Mode == HalfDuplex {
		req, _ = http.NewRequestWithContext(suo.ctx, suo.Config.Method, suo.Config.Target, bytes.NewReader(dialData))
		req.Header = baseHeader
		resp, err = suo.NoTimeoutClient.Do(req)
	} else if suo.Config.Mode == Classic {
		req, _ = http.NewRequestWithContext(suo.ctx, suo.Config.Method, suo.Config.Target, bytes.NewReader(dialData))
		req.Header = baseHeader
		resp, err = suo.NormalClient.Do(req)
	} else {
		return errors.Wrap(ErrDialFailed, "unknown mode")
	}
	if err != nil {
		log.Debugf("request error to target, %s", err)
		return errors.Wrap(ErrHostUnreachable, err.Error())
	}

	if resp.Header.Get("Set-Cookie") != "" && suo.Config.EnableCookieJar {
		log.Infof("update cookie with %s", resp.Header.Get("Set-Cookie"))
	}

	// skip offset
	if suo.Config.Offset > 0 {
		log.Debugf("skipping offset %d", suo.Config.Offset)
		_, err = io.CopyN(io.Discard, resp.Body, int64(suo.Config.Offset))
		if err != nil {
			log.Errorf("failed to skip offset, %s", err)
			return errors.Wrap(ErrDialFailed, err.Error())
		}
	}
	fr, err := netrans.ReadFrame(resp.Body)
	if err != nil {
		log.Errorf("failed to read response frame, may be the target has load balancing?")

		return errors.Wrap(ErrHostUnreachable, err.Error())
	}
	log.Debugf("recv dial response from server: length: %d", fr.Length)

	serverData, err := Unmarshal(fr.Data)
	if err != nil {
		log.Errorf("failed to process frame, %v", err)
		return errors.Wrap(ErrHostUnreachable, err.Error())
	}
	status := serverData["s"]
	if len(status) != 1 || status[0] != 0x00 {
		return errors.Wrap(ErrHostUnreachable, fmt.Sprintf("failed to dial, status: %v", status))
	}

	var streamRW io.ReadWriteCloser
	if suo.Config.Mode == FullDuplex {
		streamRW = NewFullChunkedReadWriter(id, chWR, resp.Body)
	} else if suo.Config.Mode == HalfDuplex {
		streamRW = NewHalfChunkedReadWriter(suo.ctx, id, suo.NormalClient,
			resp.Body, suo.Config)
	} else {
		streamRW = NewClassicReadWriter(suo.ctx, id, suo.NormalClient, suo.Config)
	}

	if !suo.Config.DisableHeartbeat {
		streamRW = NewHeartbeatRW(streamRW.(RawReadWriteCloser), id, suo.Config.RedirectURL, suo.Config.Mode)
	}

	suo.ReadWriteCloser = streamRW
	return nil
}
