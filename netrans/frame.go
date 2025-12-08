package netrans

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

type DataFrame struct {
	Obs    []byte
	Length uint32
	Data   []byte
}

func NewDataFrame(data []byte) *DataFrame {
	obs := make([]byte, 2)
	_, _ = rand.Read(obs[:])
	return &DataFrame{
		Obs:    obs,
		Length: uint32(len(data)),
		Data:   data,
	}
}

func (d *DataFrame) MarshalBinaryBase64() []byte {
	newData := make([]byte, len(d.Data))
	copy(newData, d.Data)
	for i := 0; i < len(newData); i++ {
		newData[i] = newData[i] ^ d.Obs[i%2]
	}
	newData = []byte(base64.RawURLEncoding.EncodeToString(newData))
	newLen := uint32(len(newData))

	result := make([]byte, 4)
	binary.BigEndian.PutUint32(result, newLen)
	for i := 0; i < len(result); i++ {
		result[i] = result[i] ^ d.Obs[i%2]
	}

	result = append(d.Obs, result...)
	result = []byte(base64.RawURLEncoding.EncodeToString(result))
	result = append(result, newData...)
	return result
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

// ReadFrameBase64 reads a base64 encoded DataFrame from an io.Reader.
func ReadFrameBase64(r io.Reader) (*DataFrame, error) {
	// Read the first part (length and obs)
	// The first part is base64 encoded from 6 bytes (4 bytes length + 2 bytes obs).
	// Base64 encoding 6 bytes results in 8 bytes.
	headerBase64 := make([]byte, 8)
	n, err := io.ReadFull(r, headerBase64)
	if err != nil {
		return nil, errors.New("failed to read header base64: " + err.Error())
	}
	if n != 8 {
		return nil, errors.New("incomplete header base64 read")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(string(headerBase64))
	if err != nil {
		return nil, errors.New("failed to decode header base64: " + err.Error())
	}
	if len(headerBytes) != 6 {
		return nil, errors.New("invalid header length, expected 6 bytes after decoding")
	}
	// Extract obs and length from the decoded header
	obs := make([]byte, 2)
	copy(obs, headerBytes[:2])
	for i := 2; i < 6; i++ {
		headerBytes[i] = headerBytes[i] ^ obs[(i-2)%2]
	}
	dataLength := binary.BigEndian.Uint32(headerBytes[2:])

	buf := make([]byte, dataLength)
	_, err = io.ReadFull(r, buf)
	if err != nil {
		return nil, errors.New("failed to read data base64: " + err.Error())
	}
	dataBytes, err := base64.RawURLEncoding.DecodeString(string(buf))
	if err != nil {
		return nil, errors.New("failed to decode data base64: " + err.Error())
	}
	// Decode the data using obs
	for i := 0; i < len(dataBytes); i++ {
		dataBytes[i] = dataBytes[i] ^ obs[i%2]
	}
	return &DataFrame{
		Length: uint32(len(dataBytes)),
		Obs:    obs,
		Data:   dataBytes,
	}, nil
}
