package isle

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/user"
	"strings"

	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/client"
	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/host"
	"github.com/lab47/isle/pkg/crypto/ssh/terminal"
	"github.com/lab47/isle/pkg/values"
	"github.com/morikuni/aec"
	"github.com/pkg/errors"
	"github.com/samber/do"
	"github.com/spf13/pflag"
	"golang.org/x/sys/unix"
)

var banner = strings.ReplaceAll(`
             ___
 __         /\_ \
/\_\    ____\//\ \      __      %s
\/\ \  /',__\ \ \ \   /'__B\    %s
 \ \ \/\__, B\ \_\ \_/\  __/    %s
  \ \_\/\____/ /\____\ \____\
   \/_/\/___/  \/____/\/____/

`, "B", "`")

type CLI struct {
	*values.Universe

	log hclog.Logger
}

var (
	bannerOnScreen bool
	bannerLines    int = strings.Count(banner, "\n")
)

func updateBanner(one string) {
	if bannerOnScreen {
		clearBanner()
	}

	bannerOnScreen = true
	fmt.Printf("+ %s", one)
}

func clearBanner() {
	if !bannerOnScreen {
		return
	}

	fmt.Print(aec.Apply("",
		aec.Column(0),
		aec.EraseLine(aec.EraseModes.All),
	))

	/*
		var parts []aec.ANSI

		for i := 0; i <= bannerLines; i++ {
			parts = append(parts,
				aec.EraseLine(aec.EraseModes.All),
				aec.PreviousLine(1),
			)
		}

		// parts = append(parts, aec.EraseLine(aec.EraseModes.All))

		fmt.Print(aec.Apply("", parts...))
	*/
	bannerOnScreen = false
}

type bannerStatus struct{}

func (bannerStatus) UpdateStatus(status string) {
	updateBanner(status)
}

func (bannerStatus) ClearStatus() {
	clearBanner()
}

func NewCLI(log hclog.Logger) (*CLI, error) {
	u := values.NewUniverse(log)

	u.Provide(log)

	c := &CLI{
		Universe: u,
		log:      log,
	}

	err := c.Seed()
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (c *CLI) Seed() error {
	return nil
}

type ExitError struct {
	Code int
}

func (ee *ExitError) Error() string {
	return fmt.Sprintf("exited with code: %d", ee.Code)
}

func (c *CLI) Run2(args []string) error {
	updateBanner("System booting...")

	fs := pflag.NewFlagSet("isle", pflag.ExitOnError)

	addr := fs.StringP("addr", "c", "localhost:20047", "address of isle-guest")

	fs.Parse(args)

	log := hclog.New(&hclog.LoggerOptions{
		Name:  "isle",
		Level: hclog.Trace,
	})

	hc, err := host.Connect(log, "tcp", *addr)
	if err != nil {
		updateBanner("Error connecting: " + err.Error())
		return errors.Wrapf(err, "error connecting to isle-guest: %s", *addr)
	}

	// conn, err := net.Dial("tcp", *addr)
	// if err != nil {
	// updateBanner("Error connecting: " + err.Error())
	// return errors.Wrapf(err, "error connecting to isle-guest: %s", *addr)
	// }

	// rs, err := pbstream.Open(c.log, conn)
	// if err != nil {
	// return errors.Wrapf(err, "starting pbstream")
	// }

	u, err := user.Current()
	if err != nil {
		return errors.Wrapf(err, "error reading current user")
	}

	start := &guestapi.SessionStart{
		Name:  "default",
		Image: "ubuntu",
		Pty:   &guestapi.SessionStart_PTYRequest{},
		Args:  []string{"/bin/bash"},
		User: &guestapi.User{
			Username: u.Username,
			Uid:      int32(os.Getuid()),
			Gid:      int32(os.Getgid()),
		},
		Home: u.HomeDir,
	}

	rs, _, err := hc.Session(start)
	if err != nil {
		return errors.Wrapf(err, "sending session start message")
	}

	updateBanner("Starting " + start.Name)

	var sc *guestapi.SessionContinue
	for sc == nil {
		var pkt guestapi.Packet

		err = rs.Recv(&pkt)
		if err != nil {
			return errors.Wrapf(err, "error receiving session continue")
		}

		if pkt.Status != "" {
			updateBanner(pkt.Status)
		}

		if pkt.Continue != nil {
			sc = pkt.Continue
		}
	}

	clearBanner()

	if sc.Error != "" {
		return fmt.Errorf("remote error: %s", sc.Error)
	}

	state, err := terminal.MakeRaw(int(os.Stdout.Fd()))
	if err == nil {
		defer terminal.Restore(int(os.Stdout.Fd()), state)
	}

	go func() {
		var pkt guestapi.Packet

		buf := make([]byte, 1024)

		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}

			pkt.Data = buf[:n]
			pkt.Channel = guestapi.Packet_STDIN

			err = rs.Send(&pkt)
			if err != nil {
				return
			}
		}
	}()

	var pkt guestapi.Packet

	for {
		err = rs.Recv(&pkt)
		if err != nil {
			return errors.Wrapf(err, "error recieving data packet")
		}

		if pkt.Exit != nil {
			break
		}

		switch pkt.Channel {
		case guestapi.Packet_STDOUT:
			os.Stdout.Write(pkt.Data)
		case guestapi.Packet_STDERR:
			os.Stderr.Write(pkt.Data)
		}
	}

	if pkt.Exit.Code == 0 {
		return nil
	}

	return &ExitError{Code: int(pkt.Exit.Code)}
}

func (c *CLI) StartSession(log hclog.Logger, addr, targetAddr string) error {
	log.Info("starting multiplex session", "addr", addr)

	os.Remove(addr)

	uaddr := &net.UnixAddr{Name: addr, Net: "unix"}

	l, err := net.ListenUnix("unix", uaddr)
	if err != nil {
		return err
	}

	for {
		local, err := l.AcceptUnix()
		if err != nil {
			return err
		}

		log.Info("connection to multiplexer")

		err = func() error {
			defer local.Close()

			remote, err := net.Dial("tcp", targetAddr)
			if err != nil {
				return errors.Wrapf(err, "error connecting to guest: %s", targetAddr)
			}

			localRaw, err := local.SyscallConn()
			if err != nil {
				return err
			}

			remoteRaw, err := remote.(*net.TCPConn).SyscallConn()
			if err != nil {
				return err
			}

			var remoteFd uintptr

			remoteRaw.Control(func(fd uintptr) {
				remoteFd = fd
			})

			rights := unix.UnixRights(int(remoteFd))

			localRaw.Control(func(fd uintptr) {
				err := unix.Sendmsg(int(fd), nil, rights, nil, 0)
				if err != nil {
					log.Error("error passing fd", "error", err)
				}
			})

			return nil
		}()

		if err != nil {
			return err
		}
	}
}

func (c *CLI) UseSession(log hclog.Logger, addr string, connector *client.Connector) (*client.Connection, error) {
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: addr, Net: "unix"})
	if err != nil {
		log.Error("error connecting to multiplexer")
		return nil, err
	}

	log.Info("connect to multiplexer", "addr", addr)

	connRaw, err := conn.SyscallConn()
	if err != nil {
		return nil, err
	}

	var f *os.File

	log.Info("recieving fd")

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

func (c *CLI) runDarwin(args []string) error {
	return nil
}

func (c *CLI) Run(args []string) error {
	updateBanner("System booting...")

	fs := pflag.NewFlagSet("isle", pflag.ExitOnError)

	addr := fs.StringP("addr", "c", "localhost:20047", "address of isle-guest")
	session := fs.StringP("session", "s", os.Getenv("ISLE_SESSION_SOCK"), "address of session to use")
	startSession := fs.String("start-session", "", "address to host control session on")

	err := fs.Parse(args)
	if err != nil {
		return err
	}

	log := hclog.New(&hclog.LoggerOptions{
		Name:  "isle",
		Level: hclog.Trace,
	})

	inj := do.New()
	do.ProvideValue(inj, log)
	do.Provide(inj, client.NewConnector)

	connector, err := do.Invoke[*client.Connector](inj)
	if err != nil {
		return err
	}

	if *startSession != "" {
		return c.StartSession(log, *startSession, *addr)
	}

	var conn *client.Connection

	if *session != "" {
		log.Info("using multplexer...")
		conn, err = c.UseSession(log, *session, connector)
	} else {
		conn, err = connector.Connect("tcp", *addr)
	}

	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	info := &client.SessionInfo{
		Name:  "default",
		Image: "ubuntu",
		Args:  fs.Args()[1:],
	}

	err = conn.StartSession(ctx, info, bannerStatus{})
	if err != nil {
		return err
	}

	return nil
}
