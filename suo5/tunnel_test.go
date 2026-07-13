package suo5

import (
	"io"
	"testing"
	"time"
)

func TestTunnelConnRemoteDataAppliesBackpressure(t *testing.T) {
	tunnel := NewTunnelConn("backpressure", testTunnelConfig(), nil)
	defer tunnel.CloseSelf()

	for i := 0; i < cap(tunnel.readChan); i++ {
		tunnel.RemoteData(NewActionData(tunnel.id, []byte{byte(i)}))
	}

	done := make(chan struct{})
	go func() {
		tunnel.RemoteData(NewActionData(tunnel.id, []byte("last")))
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("RemoteData returned while the receive queue was full")
	case <-time.After(50 * time.Millisecond):
	}

	buf := make([]byte, 8)
	if _, err := tunnel.Read(buf); err != nil {
		t.Fatalf("read queued frame: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RemoteData did not resume after queue capacity became available")
	}

	for i := 1; i < cap(tunnel.readChan); i++ {
		if _, err := tunnel.Read(buf); err != nil {
			t.Fatalf("drain queued frame %d: %v", i, err)
		}
	}
	n, err := tunnel.Read(buf)
	if err != nil {
		t.Fatalf("read backpressured frame: %v", err)
	}
	if got := string(buf[:n]); got != "last" {
		t.Fatalf("backpressured frame was lost: got %q", got)
	}
}

func TestTunnelConnCloseUnblocksRemoteData(t *testing.T) {
	tunnel := NewTunnelConn("close-write", testTunnelConfig(), nil)
	for i := 0; i < cap(tunnel.readChan); i++ {
		tunnel.RemoteData(NewActionData(tunnel.id, []byte{byte(i)}))
	}

	done := make(chan struct{})
	go func() {
		tunnel.RemoteData(NewActionData(tunnel.id, []byte("blocked")))
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("RemoteData returned while the receive queue was full")
	case <-time.After(50 * time.Millisecond):
	}

	tunnel.CloseSelf()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("closing the tunnel did not unblock RemoteData")
	}
}

func TestTunnelConnCloseUnblocksRead(t *testing.T) {
	tunnel := NewTunnelConn("close-read", testTunnelConfig(), nil)
	done := make(chan error, 1)
	go func() {
		_, err := tunnel.Read(make([]byte, 1))
		done <- err
	}()

	tunnel.CloseSelf()
	select {
	case err := <-done:
		if err != io.EOF {
			t.Fatalf("read returned %v, want EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("closing the tunnel did not unblock Read")
	}
}

func TestTunnelConnCloseDrainsQueuedData(t *testing.T) {
	tunnel := NewTunnelConn("close-drain", testTunnelConfig(), nil)
	tunnel.RemoteData(NewActionData(tunnel.id, []byte("pending")))
	tunnel.CloseSelf()

	buf := make([]byte, 16)
	n, err := tunnel.Read(buf)
	if err != nil {
		t.Fatalf("read queued data after close: %v", err)
	}
	if got := string(buf[:n]); got != "pending" {
		t.Fatalf("queued data was not preserved: got %q", got)
	}
	if _, err := tunnel.Read(buf); err != io.EOF {
		t.Fatalf("read after draining closed tunnel returned %v, want EOF", err)
	}
}

func TestTunnelConnCloseUnblocksReadUnmarshal(t *testing.T) {
	tunnel := NewTunnelConn("close-unmarshal", testTunnelConfig(), nil)
	done := make(chan error, 1)
	go func() {
		_, err := tunnel.ReadUnmarshal()
		done <- err
	}()

	tunnel.CloseSelf()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ReadUnmarshal returned nil after the tunnel was closed")
		}
	case <-time.After(time.Second):
		t.Fatal("closing the tunnel did not unblock ReadUnmarshal")
	}
}

func testTunnelConfig() *Suo5Config {
	return &Suo5Config{Timeout: 1}
}
