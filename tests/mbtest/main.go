package main

import (
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"
)

var (
	listenAddr   = flag.String("listen", "127.0.0.1:9977", "local HTTP target address")
	targetAddr   = flag.String("target", "", "HTTP target address advertised through the tunnel (defaults to listen)")
	proxyAddr    = flag.String("proxy", "127.0.0.1:1111", "suo5 SOCKS5 address")
	echoWorkers  = flag.Int("echo-workers", 50, "concurrent workers for the echo load test")
	echoRequests = flag.Int("echo-requests", 30, "echo requests per worker")
	echoClose    = flag.String("echo-close", "mixed", "target connection policy for echo: mixed, always, or never")
	onlyEcho     = flag.Bool("only-echo", false, "run only the echo load test")
	serveOnly    = flag.Bool("serve-only", false, "run only the local HTTP target server")
)

type suite struct {
	baseURL  string
	proxyURL *url.URL
}

type largeCase struct {
	name        string
	size        int
	concurrent  int
	pause       time.Duration
	readChunk   int
	readDelay   time.Duration
	closeTarget bool
	timeout     time.Duration
}

func main() {
	flag.Parse()
	if *echoWorkers <= 0 || *echoRequests <= 0 {
		fmt.Fprintln(os.Stderr, "echo-workers and echo-requests must be positive")
		os.Exit(2)
	}
	if *echoClose != "mixed" && *echoClose != "always" && *echoClose != "never" {
		fmt.Fprintln(os.Stderr, "echo-close must be mixed, always, or never")
		os.Exit(2)
	}

	server, err := startTargetServer(*listenAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start target server: %v\n", err)
		os.Exit(1)
	}
	defer server.Close()
	if *serveOnly {
		fmt.Printf("target server listening on %s\n", *listenAddr)
		select {}
	}

	proxyURL, err := url.Parse("socks5://" + *proxyAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse proxy: %v\n", err)
		os.Exit(1)
	}
	advertisedAddr := *targetAddr
	if advertisedAddr == "" {
		advertisedAddr = *listenAddr
	}
	s := &suite{baseURL: "http://" + advertisedAddr, proxyURL: proxyURL}

	tests := []struct {
		name string
		run  func() error
	}{
		{name: "echo load", run: s.runEchoLoad},
		{name: "4 MiB fast response", run: func() error {
			return s.runLarge(largeCase{name: "large-fast", size: 4 * 1024 * 1024, concurrent: 1, readChunk: 32 * 1024, timeout: 30 * time.Second})
		}},
		{name: "4 MiB paused consumer", run: func() error {
			return s.runLarge(largeCase{name: "large-pause", size: 4 * 1024 * 1024, concurrent: 1, pause: 3 * time.Second, readChunk: 32 * 1024, timeout: 30 * time.Second})
		}},
		{name: "4 MiB slow consumer", run: func() error {
			return s.runLarge(largeCase{name: "large-slow", size: 4 * 1024 * 1024, concurrent: 1, readChunk: 4 * 1024, readDelay: 5 * time.Millisecond, timeout: 45 * time.Second})
		}},
		{name: "concurrent paused consumers", run: func() error {
			return s.runLarge(largeCase{name: "concurrent-pause", size: 1024 * 1024, concurrent: 4, pause: 3 * time.Second, readChunk: 32 * 1024, timeout: 30 * time.Second})
		}},
		{name: "remote EOF drain", run: func() error {
			return s.runLarge(largeCase{name: "remote-eof", size: 1536 * 1024, concurrent: 1, pause: 12 * time.Second, readChunk: 32 * 1024, closeTarget: true, timeout: 30 * time.Second})
		}},
	}
	if *onlyEcho {
		tests = tests[:1]
	}

	suiteStarted := time.Now()
	failed := false
	for _, test := range tests {
		started := time.Now()
		fmt.Printf("=== RUN  %s\n", test.name)
		if err := test.run(); err != nil {
			fmt.Fprintf(os.Stderr, "--- FAIL: %s (%s)\n%s\n", test.name, time.Since(started).Round(time.Millisecond), err)
			failed = true
			continue
		}
		fmt.Printf("--- PASS: %s (%s)\n", test.name, time.Since(started).Round(time.Millisecond))
	}
	if failed {
		fmt.Fprintf(os.Stderr, "FAIL: one or more integration tests failed (%s)\n", time.Since(suiteStarted).Round(time.Millisecond))
		os.Exit(1)
	}
	fmt.Printf("PASS: all integration tests (%s)\n", time.Since(suiteStarted).Round(time.Millisecond))
}

func startTargetServer(address string) (*http.Server, error) {
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/echo", serveEcho)
	mux.HandleFunc("/large", serveLarge)
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(ln) }()
	return server, nil
}

func serveEcho(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	if r.URL.Query().Get("close") == "1" {
		w.Header().Set("Connection", "close")
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

func serveLarge(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.URL.Query().Get("size"))
	if err != nil || n < 0 {
		http.Error(w, "invalid size", http.StatusBadRequest)
		return
	}
	if r.URL.Query().Get("close") == "1" {
		serveLargeAndClose(w, n)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(n))
	writePattern(w, n)
}

func serveLargeAndClose(w http.ResponseWriter, n int) {
	conn, rw, err := w.(http.Hijacker).Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer conn.Close()
	_, _ = fmt.Fprintf(rw, "HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", n)
	writePattern(rw, n)
	_ = rw.Flush()
}

func (s *suite) runEchoLoad() error {
	var wg sync.WaitGroup
	errs := make(chan error, *echoWorkers)
	for worker := 0; worker < *echoWorkers; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(worker + 1)))
			client := s.newClient(30*time.Second, 2)
			for request := 0; request < *echoRequests; request++ {
				data := make([]byte, rng.Intn(40*1024))
				_, _ = rng.Read(data)
				closeTarget := *echoClose == "always" || (*echoClose == "mixed" && request%2 == 0)
				target := fmt.Sprintf("%s/echo?close=%d", s.baseURL, boolInt(closeTarget))
				resp, err := client.Post(target, "application/octet-stream", bytes.NewReader(data))
				if err != nil {
					errs <- fmt.Errorf("worker=%d request=%d POST: %w", worker, request, err)
					return
				}
				body, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if readErr != nil {
					errs <- fmt.Errorf("worker=%d request=%d read: %w", worker, request, readErr)
					return
				}
				if resp.StatusCode != http.StatusOK || !bytes.Equal(data, body) {
					errs <- fmt.Errorf("worker=%d request=%d echo mismatch: status=%d sent=%d received=%d", worker, request, resp.StatusCode, len(data), len(body))
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		return err
	}
	return nil
}

func (s *suite) runLarge(tc largeCase) error {
	client := s.newClient(tc.timeout, tc.concurrent)
	wantHash := expectedHash(tc.size)
	var wg sync.WaitGroup
	errs := make(chan error, tc.concurrent)
	for worker := 0; worker < tc.concurrent; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.runLargeRequest(client, tc, wantHash); err != nil {
				errs <- fmt.Errorf("case=%s worker=%d: %w", tc.name, worker, err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		return err
	}
	return nil
}

func (s *suite) runLargeRequest(client *http.Client, tc largeCase, wantHash string) error {
	target := fmt.Sprintf("%s/large?size=%d&close=%d", s.baseURL, tc.size, boolInt(tc.closeTarget))
	resp, err := client.Get(target)
	if err != nil {
		return fmt.Errorf("GET: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status=%s", resp.Status)
	}
	if tc.pause > 0 {
		time.Sleep(tc.pause)
	}

	hash := sha256.New()
	buf := make([]byte, tc.readChunk)
	total := 0
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			_, _ = hash.Write(buf[:n])
			total += n
			if tc.readDelay > 0 {
				time.Sleep(tc.readDelay)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read after %d/%d bytes: %w", total, tc.size, readErr)
		}
	}
	gotHash := fmt.Sprintf("%x", hash.Sum(nil))
	if total != tc.size || gotHash != wantHash {
		return fmt.Errorf("mismatch: bytes=%d/%d sha256=%s want=%s", total, tc.size, gotHash, wantHash)
	}
	return nil
}

func (s *suite) newClient(timeout time.Duration, maxConnections int) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:               http.ProxyURL(s.proxyURL),
			DisableCompression:  true,
			MaxIdleConns:        maxConnections,
			MaxIdleConnsPerHost: maxConnections,
		},
		Timeout: timeout,
	}
}

func writePattern(w io.Writer, n int) {
	buf := make([]byte, 64*1024)
	for offset := 0; offset < n; {
		count := minInt(len(buf), n-offset)
		fillPattern(buf[:count], offset)
		written, err := w.Write(buf[:count])
		offset += written
		if err != nil || written != count {
			return
		}
	}
}

func expectedHash(n int) string {
	hash := sha256.New()
	buf := make([]byte, 64*1024)
	for offset := 0; offset < n; {
		count := minInt(len(buf), n-offset)
		fillPattern(buf[:count], offset)
		_, _ = hash.Write(buf[:count])
		offset += count
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func fillPattern(buf []byte, offset int) {
	for i := range buf {
		buf[i] = byte((offset + i) % 251)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
