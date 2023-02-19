package ctrl

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"github.com/go-gost/gosocks5"
	log "github.com/kataras/golog"
	"github.com/zema1/rawhttp"
	"github.com/zema1/suo5/netrans"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"strconv"
	"sync"
)

type ConnectionType string

const (
	AutoDuplex ConnectionType = "auto"
	FullDuplex ConnectionType = "full"
	HalfDuplex ConnectionType = "half"
)

const (
	ContentTypeChecking = "application/plain"
	ContentTypeFull     = "application/octet-stream"
	ContentTypeHalf     = "application/x-binary"
)

type socks5Handler struct {
	ctx             context.Context
	target          string
	normalClient    *http.Client
	noTimeoutClient *http.Client
	rawClient       *rawhttp.Client
	bufSize         int
	pool            *sync.Pool
	selector        gosocks5.Selector
	mode            ConnectionType
	baseHeader      http.Header
}

func (m *socks5Handler) Handle(conn net.Conn) error {
	log.Infof("new connection from %s", conn.RemoteAddr())
	conn = gosocks5.ServerConn(conn, m.selector)
	req, err := gosocks5.ReadRequest(conn)
	if err != nil {
		return err
	}

	log.Infof("handshake success %s", conn.RemoteAddr())
	switch req.Cmd {
	case gosocks5.CmdConnect:
		m.handleConnect(conn, req)
		return nil
	default:
		return fmt.Errorf("%d: unsupported command", gosocks5.CmdUnsupported)
	}
}

func (m *socks5Handler) handleConnect(conn net.Conn, sockReq *gosocks5.Request) {
	defer conn.Close()
	id := RandString(8)

	var req *http.Request
	var err error
	var resp *http.Response

	dialData := buildBody(newActionCreate(id, sockReq.Addr.Host, sockReq.Addr.Port))
	ch, chWR := netrans.NewChannelWriteCloser(m.ctx)
	defer chWR.Close()

	baseHeader := m.baseHeader.Clone()

	if m.mode == FullDuplex {
		body := netrans.MultiReadCloser(
			ioutil.NopCloser(bytes.NewReader(dialData)),
			ioutil.NopCloser(netrans.NewChannelReader(ch)),
		)
		req, _ = http.NewRequestWithContext(m.ctx, http.MethodPost, m.target, body)
		baseHeader.Set("Content-Type", ContentTypeFull)
		req.Header = baseHeader
		resp, err = m.rawClient.Do(req)
	} else {
		req, _ = http.NewRequestWithContext(m.ctx, http.MethodPost, m.target, bytes.NewReader(dialData))
		baseHeader.Set("Content-Type", ContentTypeHalf)
		req.Header = baseHeader
		resp, err = m.noTimeoutClient.Do(req)
	}
	if err != nil {
		log.Debugf("request error to target, %s", err)
		rep := gosocks5.NewReply(gosocks5.HostUnreachable, nil)
		rep.Write(conn)
		return
	}
	defer resp.Body.Close()
	fr, err := netrans.ReadFrame(resp.Body)
	if err != nil {
		log.Errorf("error read response frame, %+v, connection goes to shutdown", err)
		rep := gosocks5.NewReply(gosocks5.HostUnreachable, nil)
		rep.Write(conn)
		return
	}
	log.Debugf("recv dial response from server: length: %d", fr.Length)

	serverData, err := unmarshal(fr.Data)
	if err != nil {
		log.Errorf("failed to process frame, %v", err)
		rep := gosocks5.NewReply(gosocks5.HostUnreachable, nil)
		rep.Write(conn)
		return
	}
	status := serverData["s"]
	if len(status) != 1 || status[0] != 0x00 {
		log.Errorf("connection refused to %s", sockReq.Addr)
		rep := gosocks5.NewReply(gosocks5.ConnRefused, nil)
		rep.Write(conn)
		return
	}
	rep := gosocks5.NewReply(gosocks5.Succeeded, nil)
	rep.Write(conn)
	log.Infof("conn successfully connected to %s", sockReq.Addr)

	var streamRW io.ReadWriter
	if m.mode == FullDuplex {
		streamRW = NewFullChunkedReadWriter(id, chWR, resp.Body)
	} else {
		streamRW = NewHalfChunkedReadWriter(m.ctx, id, m.normalClient, m.target, resp.Body, baseHeader)
	}
	defer streamRW.(io.Closer).Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := m.pipe(conn, streamRW); err != nil {
			log.Debugf("local conn closed")
			_ = streamRW.(io.Closer).Close()
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := m.pipe(streamRW, conn); err != nil {
			log.Debugf("remote readwriter closed")
			_ = conn.Close()
		}
	}()
	wg.Wait()
	log.Infof("connection from %s closed", conn.RemoteAddr())
}

func (m *socks5Handler) pipe(r io.Reader, w io.Writer) error {
	buf := m.pool.Get().([]byte)
	defer m.pool.Put(buf)
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

func buildBody(m map[string][]byte) []byte {
	return netrans.NewDataFrame(marshal(m)).MarshalBinary()
}

const (
	ActionCreate byte = 0x00
	ActionData   byte = 0x01
	ActionDelete byte = 0x02
	ActionResp   byte = 0x03
)

func newActionCreate(id, addr string, port uint16) map[string][]byte {
	m := make(map[string][]byte)
	m["ac"] = []byte{ActionCreate}
	m["id"] = []byte(id)
	m["h"] = []byte(addr)
	m["p"] = []byte(strconv.Itoa(int(port)))
	return m
}

func newActionData(id string, data []byte) map[string][]byte {
	m := make(map[string][]byte)
	m["ac"] = []byte{ActionData}
	m["id"] = []byte(id)
	m["dt"] = []byte(data)
	return m
}

func newDelete(id string) map[string][]byte {
	m := make(map[string][]byte)
	m["ac"] = []byte{ActionDelete}
	m["id"] = []byte(id)
	return m
}

// 定义一个最简的序列化协议，k,v 交替，每一项是len+data
// 其中 k 最长 255，v 最长 MaxUInt32
func marshal(m map[string][]byte) []byte {
	var buf bytes.Buffer
	u32Buf := make([]byte, 4)
	for k, v := range m {
		buf.WriteByte(byte(len(k)))
		buf.WriteString(k)
		binary.BigEndian.PutUint32(u32Buf, uint32(len(v)))
		buf.Write(u32Buf)
		buf.Write(v)
	}
	return buf.Bytes()

}

func unmarshal(bs []byte) (map[string][]byte, error) {
	m := make(map[string][]byte)
	total := len(bs)
	for i := 0; i < total-1; {
		kLen := int(bs[i])
		i += 1

		if i+kLen >= total {
			return nil, fmt.Errorf("unexpected eof when read key")
		}
		key := string(bs[i : i+kLen])
		i += kLen

		if i+4 >= total {
			return nil, fmt.Errorf("unexpected eof when read value size")
		}
		vLen := int(binary.BigEndian.Uint32(bs[i : i+4]))
		i += 4

		if i+vLen > total {
			return nil, fmt.Errorf("unexpected eof when read value")
		}
		value := bs[i : i+vLen]
		m[key] = value
		i += vLen
	}
	return m, nil
}
