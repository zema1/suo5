package suo5

import (
	"context"
	log "github.com/kataras/golog"
	utls "github.com/refraction-networking/utls"
	"github.com/zema1/rawhttp"
	"github.com/zema1/suo5/netrans"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Suo5Client struct {
	Config          *Suo5Config
	NormalClient    *http.Client
	NoTimeoutClient *http.Client
	RawClient       *rawhttp.Client
	Factory         StreamFactory
	Speeder         *netrans.SpeedCaculator
	BytesPool       *sync.Pool
}

func (m *Suo5Client) Pipe(r io.Reader, w io.Writer) error {
	buf := m.BytesPool.Get().([]byte)
	defer m.BytesPool.Put(buf) //nolint:staticcheck
	for {
		n, err := r.Read(buf)
		if err != nil {
			return err
		}
		_, err = w.Write(buf[:n])
		if err != nil {
			return err
		}
	}
}

func (m *Suo5Client) DualPipe(localConn, remoteWrapper io.ReadWriteCloser, addr string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		defer remoteWrapper.Close()
		if err := m.Pipe(localConn, remoteWrapper); err != nil {
			log.Debugf("local conn closed, %s", addr)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		defer localConn.Close()
		if err := m.Pipe(remoteWrapper, localConn); err != nil {
			log.Debugf("remote readwriter closed, %s", addr)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				speeder, ok := remoteWrapper.(*netrans.SpeedTrackingReadWriterCloser)
				if ok {
					up, down := speeder.GetSpeedInterval()
					m.Speeder.AddSpeed(up, down)
				}
			}
		}
	}()
	wg.Wait()
}

func newRawClient(upstream rawhttp.ContextDialFunc, timeout time.Duration) *rawhttp.Client {
	return rawhttp.NewClient(&rawhttp.Options{
		Proxy:                  upstream,
		ProxyDialTimeout:       timeout,
		Timeout:                timeout,
		FollowRedirects:        false,
		MaxRedirects:           0,
		AutomaticHostHeader:    true,
		AutomaticContentLength: true,
		ForceReadAllBody:       false,
		TLSHandshake: func(conn net.Conn, addr string, options *rawhttp.Options) (net.Conn, error) {
			colonPos := strings.LastIndex(addr, ":")
			if colonPos == -1 {
				colonPos = len(addr)
			}
			hostname := addr[:colonPos]
			uTlsConn := utls.UClient(conn, &utls.Config{
				InsecureSkipVerify: true,
				ServerName:         hostname,
				MinVersion:         utls.VersionTLS10,
				Renegotiation:      utls.RenegotiateOnceAsClient,
			}, utls.HelloRandomizedNoALPN)
			if err := uTlsConn.Handshake(); err != nil {
				_ = conn.Close()
				return nil, err
			}
			return uTlsConn, nil
		},
	})

}
