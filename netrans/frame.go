package netrans

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
)

type DataFrame struct {
	Length uint32
	Obs    byte
	Data   []byte
}

func NewDataFrame(data []byte) *DataFrame {
	b := make([]byte, 1)
	_, _ = rand.Read(b)
	return &DataFrame{
		Length: uint32(len(data)),
		Obs:    b[0],
		Data:   data,
	}
}

func (d *DataFrame) MarshalBinary() []byte {
	result := make([]byte, 4, 4+1+d.Length)
	binary.BigEndian.PutUint32(result, d.Length)
	result = append(result, d.Obs)
	result = append(result, d.Data...)
	for i := 5; i < len(result); i++ {
		result[i] = result[i] ^ d.Obs
	}
	return result
}

func ReadFrame(r io.Reader) (*DataFrame, error) {
	var bs [4]byte
	// read xor and magic number
	_, err := io.ReadFull(r, bs[:])
	if err != nil {
		return nil, err
	}
	fr := &DataFrame{}

	fr.Length = binary.BigEndian.Uint32(bs[:])
	if fr.Length > 1024*1024*32 {
		return nil, fmt.Errorf("frame is too big, %d", fr.Length)
	}
	n, err := r.Read(bs[:1])
	if n != 1 || err != nil {
		return nil, fmt.Errorf("read type error %v", err)
	}
	fr.Obs = bs[0]
	buf := make([]byte, fr.Length)
	_, err = io.ReadFull(r, buf)
	if err != nil {
		return nil, fmt.Errorf("read data error: %v", err)
	}
	for i := 0; i < len(buf); i++ {
		buf[i] = buf[i] ^ fr.Obs
	}
	fr.Data = buf
	return fr, nil
}
