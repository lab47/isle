package guest

import (
	"fmt"
	"net"
	"testing"

	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/pkg/pbstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShellLauncher(t *testing.T) {
	t.Run("spawn shells in response to network requests", func(t *testing.T) {
		Setup(t, func(sd *SetupData) {
			var sl ShellLauncher

			sl.L = sd.Logger
			sl.cm = sd.Containers
			sl.sm = sd.Shells

			l, err := net.Listen("tcp", "")
			require.NoError(t, err)

			go sl.Listen(sd.ResourceContext, l)

			addr := l.Addr().String()

			for i := 1; i <= 2; i++ {
				c, err := net.Dial("tcp", addr)
				require.NoError(t, err)

				packet := &guestapi.SessionStart{
					Name:  "testsess",
					Image: "alpine",
					Args:  []string{"sh", "-c", fmt.Sprintf("echo 'hello tests %d'", i)},
					Pty:   &guestapi.SessionStart_PTYRequest{},
				}

				rs, err := pbstream.Open(sl.L, c)
				require.NoError(t, err)

				err = rs.Send(packet)
				require.NoError(t, err)

				var sc guestapi.SessionContinue

				err = rs.Recv(&sc)
				require.NoError(t, err)

				t.Logf("pid: %d", sc.Pid)

				var pkt guestapi.Packet

				err = rs.Recv(&pkt)
				require.NoError(t, err)

				assert.Equal(t, guestapi.Packet_STDOUT, pkt.Channel)
				assert.Equal(t, []byte(fmt.Sprintf("hello tests %d\r\n", i)), pkt.Data)

				err = rs.Recv(&pkt)
				require.NoError(t, err)

				assert.Empty(t, pkt.Data)

				require.NotNil(t, pkt.Exit)
				assert.Equal(t, int32(0), pkt.Exit.Code)

				// check that there is the shell session as a resource around
				res, err := sd.Shells.Lookup(sd.ResourceContext, "testsess")
				require.NoError(t, err)

				assert.NotNil(t, res)
			}

			// Perform a non-terminal session
			i := 3
			c, err := net.Dial("tcp", addr)
			require.NoError(t, err)

			packet := &guestapi.SessionStart{
				Name:  "testsess",
				Image: "alpine",
				Args:  []string{"sh", "-c", fmt.Sprintf("echo 'hello tests %d'", i)},
			}

			rs, err := pbstream.Open(sl.L, c)
			require.NoError(t, err)

			err = rs.Send(packet)
			require.NoError(t, err)

			var sc guestapi.SessionContinue

			err = rs.Recv(&sc)
			require.NoError(t, err)

			t.Logf("pid: %d", sc.Pid)

			var pkt guestapi.Packet

			err = rs.Recv(&pkt)
			require.NoError(t, err)

			assert.Equal(t, guestapi.Packet_STDOUT, pkt.Channel)
			assert.Equal(t, []byte(fmt.Sprintf("hello tests %d\n", i)), pkt.Data)

			err = rs.Recv(&pkt)
			require.NoError(t, err)

			assert.Empty(t, pkt.Data)

			require.NotNil(t, pkt.Exit)
			assert.Equal(t, int32(0), pkt.Exit.Code)

			// check that there is the shell session as a resource around
			res, err := sd.Shells.Lookup(sd.ResourceContext, "testsess")
			require.NoError(t, err)

			assert.NotNil(t, res)
		})
	})
}
