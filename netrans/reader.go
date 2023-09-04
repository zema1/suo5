package netrans

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

var ErrReadTimeout = errors.New("read timeout")
var ErrReaderClosed = errors.New("reader has been closed")

var errNormal = errors.New("normal read")

type TimeoutReader struct {
	rc     io.ReadCloser
	buf    *bufio.Reader
	t      time.Duration
	errCh  chan error
	mu     sync.Mutex
	closed bool
	ctx    context.Context
	cancel func()
}

func NewTimeoutReader(ctx context.Context, r io.Reader, timeout time.Duration) io.Reader {
	return NewTimeoutReadCloser(ctx, io.NopCloser(r), timeout)
}

func NewTimeoutReadCloser(ctx context.Context, rc io.ReadCloser, timeout time.Duration) io.ReadCloser {
	if timeout < 0 {
		panic("invalid timeout")
	}
	ctx, cancel := context.WithCancel(ctx)

	tr := &TimeoutReader{
		rc:     rc,
		buf:    bufio.NewReaderSize(rc, 4096),
		t:      timeout,
		ctx:    ctx,
		cancel: cancel,
	}
	tr.startLoop()
	return tr
}

func (r *TimeoutReader) startLoop() {
	r.errCh = make(chan error)
	go func() {
		defer close(r.errCh)
		for {
			_, err := r.buf.Peek(1)
			nErr := err
			if nErr == nil {
				nErr = errNormal
			}
			select {
			case r.errCh <- nErr:
			case <-r.ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
}

func (r *TimeoutReader) Read(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, ErrReaderClosed
	}

reread:
	select {
	case err := <-r.errCh: // Timeout
		if r.buf.Buffered() > 0 {
			return r.buf.Read(b)
		}
		if errors.Is(err, errNormal) {
			// 非预期的情况
			goto reread
		}
		if err == nil {
			// channel closed
			r.closed = true
			return 0, ErrReaderClosed
		} else {
			return 0, err
		}
	case <-time.After(r.t):
		return 0, ErrReadTimeout
	}
}

func (r *TimeoutReader) Close() error {
	err := r.rc.Close()
	r.cancel()
	r.closed = true
	return err
}

type channelReader struct {
	ch  chan []byte
	mu  sync.Mutex
	buf bytes.Buffer
}

func NewChannelReader(ch chan []byte) io.Reader {
	return &channelReader{ch: ch}
}

func (c *channelReader) Read(p []byte) (n int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.buf.Len() != 0 {
		return c.buf.Read(p)
	}
	var data []byte
	for {
		data = <-c.ch
		// channel closed
		if data == nil {
			return 0, io.EOF
		}
		if len(data) != 0 {
			break
		}
	}
	c.buf.Write(data)
	return c.buf.Read(p)
}

type channelWriterCloser struct {
	ch     chan []byte
	mu     sync.Mutex
	closed bool
	ctx    context.Context
	cancel func()
}

func NewChannelWriteCloser(ctx context.Context) (chan []byte, io.WriteCloser) {
	ch := make(chan []byte)
	ctx, cancel := context.WithCancel(ctx)
	return ch, &channelWriterCloser{
		ch:     ch,
		ctx:    ctx,
		cancel: cancel,
	}
}

func (c *channelWriterCloser) Close() error {
	c.cancel()

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.closed {
		close(c.ch)
		c.closed = true
	}
	return nil
}

func (c *channelWriterCloser) Write(p []byte) (n int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return 0, io.EOF
	}
	select {
	case c.ch <- p:
		return len(p), nil
	case <-c.ctx.Done():
		return 0, c.ctx.Err()
	}
}

type multiReadCloser struct {
	rcs []io.ReadCloser
	r   io.Reader
}

func MultiReadCloser(rcs ...io.ReadCloser) io.ReadCloser {
	var rs []io.Reader
	for _, rc := range rcs {
		rs = append(rs, rc.(io.Reader))
	}
	return &multiReadCloser{
		rcs: rcs,
		r:   io.MultiReader(rs...),
	}
}

func (m *multiReadCloser) Read(p []byte) (n int, err error) {
	return m.r.Read(p)
}

func (m *multiReadCloser) Close() error {
	var err error
	for _, rc := range m.rcs {
		if e := rc.Close(); e != nil {
			err = e
		}
	}
	return err
}
