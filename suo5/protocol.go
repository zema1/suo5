package suo5

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/zema1/suo5/netrans"
	"strconv"
)

type ConnectionType string

const (
	Undefined  ConnectionType = "undefined"
	Checking   ConnectionType = "checking"
	AutoDuplex ConnectionType = "auto"
	FullDuplex ConnectionType = "full"
	HalfDuplex ConnectionType = "half"
	Classic    ConnectionType = "classic"
)

func (c ConnectionType) Bin() byte {
	switch c {
	case Checking:
		return 0x00
	case FullDuplex:
		return 0x01
	case HalfDuplex:
		return 0x02
	case Classic:
		return 0x03
	default:
		return 0xff
	}
}

const (
	ActionCreate    byte = 0x00
	ActionData      byte = 0x01
	ActionDelete    byte = 0x02
	ActionHeartbeat byte = 0x03
)

func BuildBody(m map[string][]byte, redirect string, ct ConnectionType) []byte {
	if len(redirect) != 0 {
		m["r"] = []byte(redirect)
	}
	m["m"] = []byte{ct.Bin()}
	return netrans.NewDataFrame(Marshal(m)).MarshalBinary()
}

func NewActionCreate(id, addr string, port uint16) map[string][]byte {
	m := make(map[string][]byte)
	m["ac"] = []byte{ActionCreate}
	m["id"] = []byte(id)
	m["h"] = []byte(addr)
	m["p"] = []byte(strconv.Itoa(int(port)))

	return m
}

func NewActionData(id string, data []byte) map[string][]byte {
	m := make(map[string][]byte)
	m["ac"] = []byte{ActionData}
	m["id"] = []byte(id)
	m["dt"] = []byte(data)
	return m
}

func NewActionDelete(id string) map[string][]byte {
	m := make(map[string][]byte)
	m["ac"] = []byte{ActionDelete}
	m["id"] = []byte(id)
	return m
}

func NewActionHeartbeat(id string) map[string][]byte {
	m := make(map[string][]byte)
	m["ac"] = []byte{ActionHeartbeat}
	m["id"] = []byte(id)
	return m
}

// 定义一个最简的序列化协议，k,v 交替，每一项是len+data
// 其中 k 最长 255，v 最长 MaxUInt32
func Marshal(m map[string][]byte) []byte {
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

func Unmarshal(bs []byte) (map[string][]byte, error) {
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

		if i+4 > total {
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
