package timesync

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/pkg/pbstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRPC(t *testing.T) {
	log := hclog.New(&hclog.LoggerOptions{
		Name: "timesync",
	})

	s := &Server{log: log}

	_, h := PBSNewTimeSyncHandler(s)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	defer l.Close()

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}

			rs, err := pbstream.Open(log, c)
			require.NoError(t, err)

			err = h.HandleRPC(ctx, rs)
			require.NoError(t, err)
		}
	}()

	pbc := pbstream.StreamOpenFunc(func() (*pbstream.Stream, error) {
		s, err := net.DialTCP("tcp", nil, l.Addr().(*net.TCPAddr))
		if err != nil {
			return nil, err
		}

		return pbstream.Open(log, s)
	})

	client := &Client{
		log:    log,
		client: PBSNewTimeSyncClient(pbc),
	}

	t1 := time.Now().UnixNano()
	var t2 int64

	client.Sync(ctx, func(base, offset int64) error {
		t2 = base + offset
		return io.EOF
	})

	assert.InDelta(t, t1, t2, float64(time.Millisecond))
}
