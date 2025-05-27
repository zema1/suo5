package netrans

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
)

type DataFrame struct {
	Length uint32
	Obs    []byte
	Data   []byte
}

func NewDataFrame(data []byte) *DataFrame {
	obs := make([]byte, 2)
	_, _ = rand.Read(obs[:])
	return &DataFrame{
		Length: uint32(len(data)),
		Obs:    obs,
		Data:   data,
	}
}

func (d *DataFrame) MarshalBinary() []byte {
	result := make([]byte, 4, 4+2+d.Length)
	binary.BigEndian.PutUint32(result, d.Length)
	result = append(result, d.Obs...)
	result = append(result, d.Data...)
	for i := 6; i < len(result); i++ {
		result[i] = result[i] ^ d.Obs[(i-6)%2]
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
	n, err := r.Read(bs[:2])
	if n != 2 || err != nil {
		return nil, fmt.Errorf("read type error %v", err)
	}
	fr.Obs = bs[:2]
	buf := make([]byte, fr.Length)
	_, err = io.ReadFull(r, buf)
	if err != nil {
		return nil, fmt.Errorf("read data error: %v", err)
	}
	for i := 0; i < len(buf); i++ {
		buf[i] = buf[i] ^ fr.Obs[i%2]
	}
	fr.Data = buf
	return fr, nil
}

func ReadFrameWithBuffer(r io.Reader) (*DataFrame, []byte, error) {
	buf := make([]byte, 1024*8)
	n, err := r.Read(buf)
	if err != nil && err != io.EOF {
		return nil, nil, fmt.Errorf("read data error: %v", err)
	}
	buf = buf[:n]
	rd := io.MultiReader(bytes.NewReader(buf), r)
	fr, err := ReadFrame(rd)
	if err != nil {
		return nil, buf, err
	}
	return fr, buf, nil
}
