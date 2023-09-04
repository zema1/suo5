package netrans

import (
	"bytes"
	"context"
	"github.com/stretchr/testify/require"
	"io"
	"strings"
	"testing"
	"time"
)

func TestReader(t *testing.T) {
	assert := require.New(t)
	ctx := context.Background()

	data := []byte("hello")
	tr := NewTimeoutReadCloser(ctx, io.NopCloser(bytes.NewReader(data)), time.Second*3)
	buf := make([]byte, 1024)
	now := time.Now()
	n, err := tr.Read(buf)
	assert.Nil(err)
	assert.True(time.Since(now).Seconds() < 1)
	assert.Equal(buf[:n], data)
	_ = tr.Close()

	pr, pw := io.Pipe()
	tr = NewTimeoutReadCloser(ctx, pr, time.Second*3)
	now = time.Now()
	_, err = tr.Read(buf)
	assert.ErrorIs(err, ErrReadTimeout)
	assert.True(time.Since(now).Seconds() > 3)

	_, _ = pw.Write(data)
	now = time.Now()
	_, err = tr.Read(buf)
	assert.Nil(err)
	assert.True(time.Since(now).Seconds() < 1)
	assert.Equal(buf[:n], data)

	_ = pw.Close()
	_, err = tr.Read(buf)
	assert.ErrorIs(err, io.EOF)
	_, err = tr.Read(buf)
	assert.ErrorIs(err, ErrReaderClosed)
	_ = tr.Close()
}

func TestChannelReader(t *testing.T) {
	assert := require.New(t)

	ch := make(chan []byte, 1)
	r := NewChannelReader(ch)
	buf := make([]byte, 2)
	ch <- []byte("hel")
	n, err := r.Read(buf)
	assert.Equal(n, 2)
	assert.Nil(err)
	assert.Equal(buf[:n], []byte("he"))

	n, err = r.Read(buf)
	assert.Equal(n, 1)
	assert.Nil(err)
	assert.Equal(buf[:n], []byte("l"))

	go func() {
		time.Sleep(time.Second * 2)
		ch <- []byte("e")
	}()
	now := time.Now()
	n, err = r.Read(buf)
	assert.Nil(err)
	assert.True(time.Since(now).Seconds() >= 2)
	assert.Equal(n, 1)
	assert.Equal(buf[:n], []byte("e"))

	close(ch)
	_, err = r.Read(buf)
	assert.ErrorIs(err, io.EOF)
}

func TestMultiReaderClosed(t *testing.T) {
	assert := require.New(t)

	rc := MultiReadCloser(io.NopCloser(strings.NewReader("hello")), io.NopCloser(strings.NewReader("world")))
	data, err := io.ReadAll(rc)
	assert.Nil(err)
	assert.Equal(data, []byte("helloworld"))
	assert.Nil(rc.Close())
}
