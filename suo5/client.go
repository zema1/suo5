package suo5

import (
	utls "github.com/refraction-networking/utls"
	"github.com/zema1/rawhttp"
	"net"
	"net/http"
	"strings"
	"time"
)

type ConnectionType string

const (
	Undefined  ConnectionType = "undefined"
	AutoDuplex ConnectionType = "auto"
	FullDuplex ConnectionType = "full"
	HalfDuplex ConnectionType = "half"
)

const (
	HeaderKey           = "Content-Type"
	HeaderValueChecking = "application/plain"
	HeaderValueFull     = "application/octet-stream"
	HeaderValueHalf     = "application/x-binary"
)

type Suo5Client struct {
	Config          *Suo5Config
	NormalClient    *http.Client
	NoTimeoutClient *http.Client
	RawClient       *rawhttp.Client
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
