package guestapi

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"gotest.tools/v3/assert"
)

func TestPacket(t *testing.T) {
	t.Run("fast unmarshal works", func(t *testing.T) {
		data, err := proto.Marshal(&Packet{
			Channel: Packet_STDOUT,
			Data:    []byte("hello"),
		})
		require.NoError(t, err)

		var p2 Packet

		err = p2.FastUnmarshalProto(data)
		require.NoError(t, err)

		assert.Equal(t, Packet_STDOUT, p2.Channel)
		assert.Equal(t, "hello", string(p2.Data))

		data, err = proto.Marshal(&Packet{
			Channel: Packet_STDOUT,
			Data:    []byte(" world"),
		})
		require.NoError(t, err)

		err = p2.FastUnmarshalProto(data)
		require.NoError(t, err)

		assert.Equal(t, Packet_STDOUT, p2.Channel)
		assert.Equal(t, " world", string(p2.Data))

	})

}
