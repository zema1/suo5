package netrans

import (
	"bytes"
	"fmt"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestFrame(t *testing.T) {
	assert := require.New(t)

	data := []byte("hello")
	fr := NewDataFrame(data)
	bin := fr.MarshalBinary()
	fmt.Println(string(bin))

	newFr, err := ReadFrame(bytes.NewReader(bin))
	assert.Nil(err)
	assert.Equal(newFr.Length, uint32(len(data)))
	assert.Equal(data, newFr.Data)
}
