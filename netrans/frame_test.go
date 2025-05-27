package netrans

import (
	"bytes"
	"crypto/rand"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestFrame(t *testing.T) {
	assert := require.New(t)

	for i := 0; i < 1000; i++ {
		data := make([]byte, 1000*i%10000)
		_, err := rand.Read(data)
		assert.Nil(err)

		fr := NewDataFrame(data)
		bin := fr.MarshalBinary()

		newFr, err := ReadFrame(bytes.NewReader(bin))
		assert.Nil(err)
		assert.Equal(newFr.Length, uint32(len(data)))
		assert.Equal(data, newFr.Data)
	}
}
