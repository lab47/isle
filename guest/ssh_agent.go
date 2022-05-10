package guest

import (
	"context"
	"io"
	"net"
	"os"

	"github.com/fxamacker/cbor/v2"
	"github.com/lab47/isle/types"
)

func (m *ContainerManager) StartSSHAgent(ctx context.Context) error {
	os.Remove(m.sshAgentPath)

	agentListener, err := net.Listen("unix", m.sshAgentPath)
	if err != nil {
		m.L.Error("error listening on ssh agent path", "error", err, "path", m.sshAgentPath)
		return err
	}

	go m.forwardSSHAgent(ctx, agentListener)
	return nil
}

func (m *ContainerManager) forwardSSHAgent(ctx context.Context, l net.Listener) {
	defer l.Close()

	os.Chmod(m.sshAgentPath, 0777)

	for {
		c, err := l.Accept()
		if err != nil {
			return
		}

		host, err := m.currentSession.Open()
		if err != nil {
			c.Close()
			m.L.Error("error opening channel to host", "error", err)
			continue
		}

		host.Write([]byte{types.ProtocolByte})
		enc := cbor.NewEncoder(host)
		enc.Encode(types.HeaderMessage{Kind: "ssh-agent"})

		dec := cbor.NewDecoder(host)

		var resp types.ResponseMessage

		dec.Decode(&resp)

		if resp.Code != types.OK {
			m.L.Error("error establishing agent connection", "remote-error", types.Error)
			c.Close()
			continue
		}

		m.L.Debug("established connection to ssh-agent")

		go func() {
			defer c.Close()
			defer host.Close()

			io.Copy(host, c)
		}()

		go func() {
			defer c.Close()
			defer host.Close()

			io.Copy(c, host)
		}()
	}
}
