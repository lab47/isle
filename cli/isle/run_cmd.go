package isle

import (
	"context"
	"fmt"
	"io/ioutil"
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
	Connect  string `long:"connect" description:"socket to connect to"`
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

func (c *RunCmd) probeBackend() bool {
	pidPath := filepath.Join(c.stateDir, "vm.pid")

	data, err := ioutil.ReadFile(pidPath)
	if err == nil {
		var pid int

		fmt.Sscanf(string(data), "%d", &pid)

		err = unix.Kill(pid, 0)
		if err == nil {
			return true
		}
	}

	return false
}

func (c *RunCmd) Execute(args []string) error {
	var status client.Statuser
	if isatty.IsTerminal(os.Stdin.Fd()) {
		status = bannerStatus{}
	}

	log := hclog.New(&hclog.LoggerOptions{
		Name:  "isle",
		Level: ComputeLevel(c.Verbose),
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

	if !c.probeBackend() {
		log.Info("automatically starting backend")
		updateBanner("Starting backend...")
		err = c.osStartBackground()
		if err != nil {
			return err
		}
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
		var conn *client.Connection

		if c.Connect != "" {
			conn, err = connector.Connect("unix", c.Connect)
		} else {
			conn, err = connector.Connect("unix", filepath.Join(c.stateDir, "isle.sock"))
		}

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

func (c *RunCmd) OpenViaSession(log hclog.Logger, addr string, connector *client.Connector) (*client.Connection, error) {
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

	return connector.ConnectIO(f)
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
