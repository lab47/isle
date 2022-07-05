package isle

import (
	"context"
	"net"
	"os"
	"path/filepath"

	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/client"
	"github.com/mattn/go-isatty"
	"github.com/samber/do"
	"golang.org/x/sys/unix"
)

type RunCmd struct {
	Image    string `short:"i" long:"image" default:"ubuntu" description:"OCI image to run"`
	Name     string `short:"n" long:"name" default:"default" description:"name of instance to run in"`
	StateDir string `long:"state-dir" env:"ISLE_STATE_DIR" description:"directory runtime data is in"`
	Verbose  []bool `short:"V" description:"be more verbose"`

	stateDir     string
	forceSession bool
}

func ComputeStateDir(dir string) (string, error) {
	var err error

	if dir != "" {
		dir, err = filepath.Abs(dir)
		if err != nil {
			return "", err
		}
	} else {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}

		dir = filepath.Join(configDir, "isle")
	}

	return dir, nil
}

func (c *RunCmd) Execute(args []string) error {
	var status client.Statuser
	if isatty.IsTerminal(os.Stdin.Fd()) {
		status = bannerStatus{}
	}

	level := hclog.Info

	switch len(c.Verbose) {
	case 0:
		// nothing
	case 1:
		level = hclog.Debug
	default:
		level = hclog.Trace
	}

	log := hclog.New(&hclog.LoggerOptions{
		Name:  "isle",
		Level: level,
	})

	dir, err := ComputeStateDir(c.StateDir)
	if err != nil {
		return err
	}

	c.stateDir = dir

	err = c.osSetup()
	if err != nil {
		return err
	}

	inj := do.New()
	do.ProvideValue(inj, log)
	do.Provide(inj, client.NewConnector)

	connector, err := do.Invoke[*client.Connector](inj)
	if err != nil {
		return err
	}

	if c.forceSession {
		s, err := c.UseSession(log, filepath.Join(c.stateDir, "session.sock"), connector)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		info := &client.SessionInfo{
			Name:  c.Name,
			Image: c.Image,
			Args:  args,
		}

		err = s.StartSession(ctx, info, status)
		if err != nil {
			return err
		}

	} else {
		conn, err := connector.Connect("unix", filepath.Join(c.stateDir, "isle.sock"))
		if err != nil {
			return err
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		info := &client.SessionInfo{
			Name:  c.Name,
			Image: c.Image,
			Args:  args,
		}

		err = conn.StartSession(ctx, info, status)
		if err != nil {
			return err
		}

	}

	return nil

}

func (c *RunCmd) UseSession(log hclog.Logger, addr string, connector *client.Connector) (*client.Single, error) {
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: addr, Net: "unix"})
	if err != nil {
		log.Error("error connecting to multiplexer")
		return nil, err
	}

	log.Trace("connect to multiplexer", "addr", addr)

	connRaw, err := conn.SyscallConn()
	if err != nil {
		return nil, err
	}

	var f *os.File

	connRaw.Read(func(fd uintptr) bool {
		// receive socket control message
		b := make([]byte, unix.CmsgSpace(4))
		_, _, _, _, err = unix.Recvmsg(int(fd), nil, b, 0)
		if err != nil {
			return false
		}

		// parse socket control message
		cmsgs, err := unix.ParseSocketControlMessage(b)
		if err != nil {
			panic(err)
		}
		fds, err := unix.ParseUnixRights(&cmsgs[0])
		if err != nil {
			panic(err)
		}

		remoteFd := fds[0]

		f = os.NewFile(uintptr(remoteFd), "listener")

		return true
	})

	if f == nil {
		panic("couldn't get a descriptor")
	}

	return client.OpenSingle(log, f)
}
