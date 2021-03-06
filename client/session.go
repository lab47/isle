package client

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"os/user"
	"sync"

	"github.com/creack/pty"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/host"
	"github.com/lab47/isle/pkg/labels"
	"github.com/lab47/isle/pkg/pbstream"
	islesignal "github.com/lab47/isle/signal"
	"github.com/mattn/go-isatty"
	"github.com/pkg/errors"
	"github.com/samber/do"
	"golang.org/x/crypto/ssh/terminal"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
)

type Connector struct {
	log hclog.Logger
}

func NewConnector(inj *do.Injector) (*Connector, error) {
	log, err := do.Invoke[hclog.Logger](inj)
	if err != nil {
		return nil, err
	}

	cl := &Connector{log: log}

	return cl, err
}

type Connection struct {
	log hclog.Logger
	hc  *host.Connection

	user *user.User

	handler pbstream.Handler
}

func (c *Connector) ConnectIO(rwc io.ReadWriteCloser) (*Connection, error) {
	hc, err := host.ConnectIO(c.log, rwc)
	if err != nil {
		return nil, err
	}

	return c.connectHost(hc)
}

func (c *Connector) Connect(proto, addr string) (*Connection, error) {
	hc, err := host.Connect(c.log, proto, addr)
	if err != nil {
		return nil, err
	}

	return c.connectHost(hc)
}

func (c *Connector) connectHost(hc *host.Connection) (*Connection, error) {
	u, err := user.Current()
	if err != nil {
		return nil, errors.Wrapf(err, "error reading current user")
	}

	conn := &Connection{log: c.log, hc: hc, user: u}
	_, mux := guestapi.PBSNewHostAPIHandler(&connApi{
		log: c.log, conn: conn,
	})

	conn.handler = mux

	return conn, nil
}

type ExitError struct {
	Code int
}

func (ee *ExitError) Error() string {
	return fmt.Sprintf("exited with code: %d", ee.Code)
}

type SessionInfo struct {
	Name  string   `json:"name"`
	Image string   `json:"image"`
	Args  []string `json:"args"`
}

func (s *SessionInfo) Validate() error {
	if len(s.Args) == 0 {
		s.Args = []string{"/bin/bash"}
	}

	img, err := name.ParseReference(s.Image)
	if err != nil {
		return err
	}

	if s.Name == "" {
		s.Name = img.Context().RegistryStr()
	}

	return nil
}

func (c *Connection) StartRPCAgent(ctx context.Context) error {
	sel := labels.New("rpc", c.user.Username)

	c.log.Info("starting rpc listener", "selector", sel.String())

	l, err := c.hc.Listen(sel)
	if err != nil {
		return err
	}

	for {
		rs, _, err := l.Accept(ctx)
		if err != nil {
			return err
		}

		c.log.Debug("received new connection")

		go func() {
			err := c.handler.HandleRPC(ctx, rs)
			if err != nil {
				c.log.Error("error handling rpc", "error", err)
			}
		}()
	}
}

func (c *Connection) StartSSHAgent(ctx context.Context) error {
	path := os.Getenv("SSH_AUTH_SOCK")
	if path == "" {
		c.log.Debug("no SSH_AUTH_SOCK function, not running agent forwarding")
		return nil
	}

	sel := labels.New("ssh-agent", c.user.Username)

	c.log.Info("starting ssh agent forwarding", "selector", sel.String())

	l, err := c.hc.Listen(sel)
	if err != nil {
		c.log.Error("error setting up isle listener", "error", err)
		return err
	}

	for {
		rs, conn, err := l.Accept(ctx)
		if err != nil {
			return err
		}

		go func() {
			defer conn.Close()

			remote, err := rs.Hijack(conn)
			if err != nil {
				c.log.Error("error hijacking pbstream", "error", err)
				return
			}

			local, err := net.Dial("unix", path)
			if err != nil {
				c.log.Error("error connecting to host ssh-agent", "error", err)
				return
			}

			defer local.Close()

			c.log.Debug("connected to local ssh-agent")

			go func() {
				defer local.Close()
				defer remote.Close()

				io.Copy(remote, local)
			}()

			defer local.Close()
			defer remote.Close()

			io.Copy(local, remote)
		}()
	}
}

type Statuser interface {
	UpdateStatus(status string)
	ClearStatus()
}

func (c *Connection) StartSession(ctx context.Context, info *SessionInfo, status Statuser) error {
	err := info.Validate()
	if err != nil {
		return err
	}

	u, err := user.Current()
	if err != nil {
		return errors.Wrapf(err, "error reading current user")
	}

	start := &guestapi.SessionStart{
		Name:  info.Name,
		Image: info.Image,
		Args:  info.Args,
		User: &guestapi.User{
			Username: u.Username,
			Uid:      int32(os.Getuid()),
			Gid:      int32(os.Getgid()),
		},
		Home:        u.HomeDir,
		PortForward: guestapi.FromSet(labels.New("rpc", c.user.Username)),
	}

	var useTerm bool

	if isatty.IsTerminal(os.Stdin.Fd()) {
		useTerm = true

		winsz, err := pty.GetsizeFull(os.Stdin)
		if err != nil {
			return err
		}

		start.Pty = &guestapi.SessionStart_PTYRequest{
			Term: os.Getenv("TERM"),
			WindowSize: &guestapi.Packet_WindowSize{
				Width:  int32(winsz.Cols),
				Height: int32(winsz.Rows),
			},
		}
	}

	grp, ctx := errgroup.WithContext(ctx)
	defer grp.Wait()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	grp.Go(func() error {
		return c.StartSSHAgent(ctx)
	})

	grp.Go(func() error {
		return c.StartRPCAgent(ctx)
	})

	rs, _, err := c.hc.Session(start)
	if err != nil {
		return errors.Wrapf(err, "sending session start message")
	}

	if status != nil {
		status.UpdateStatus("Starting " + start.Name)
	}

	return handleSession(ctx, rs, status, useTerm)
}

func (c *Connection) Open(sel labels.Set) (*pbstream.Stream, net.Conn, error) {
	return c.hc.Open(sel)
}

func (c *Connection) Opener(sel labels.Set) pbstream.StreamOpener {
	return pbstream.StreamOpenFunc(func() (*pbstream.Stream, error) {
		rs, _, err := c.hc.Open(sel)
		return rs, err
	})
}

type Single struct {
	rwc io.ReadWriteCloser
	log hclog.Logger
}

func OpenSingle(log hclog.Logger, rwc io.ReadWriteCloser) (*Single, error) {
	s := &Single{
		rwc: rwc,
		log: log,
	}

	err := host.ConnectSingle(rwc)
	if err != nil {
		return nil, err
	}

	return s, nil
}

func (s *Single) StartSession(ctx context.Context, info *SessionInfo, status Statuser) error {
	err := info.Validate()
	if err != nil {
		return err
	}

	u, err := user.Current()
	if err != nil {
		return errors.Wrapf(err, "error reading current user")
	}

	start := &guestapi.SessionStart{
		Name:  info.Name,
		Image: info.Image,
		Args:  info.Args,
		User: &guestapi.User{
			Username: u.Username,
			Uid:      int32(os.Getuid()),
			Gid:      int32(os.Getgid()),
		},
		Home: u.HomeDir,
	}

	var useTerm bool

	if isatty.IsTerminal(os.Stdin.Fd()) {
		useTerm = true

		winsz, err := pty.GetsizeFull(os.Stdin)
		if err != nil {
			return err
		}

		start.Pty = &guestapi.SessionStart_PTYRequest{
			Term: os.Getenv("TERM"),
			WindowSize: &guestapi.Packet_WindowSize{
				Width:  int32(winsz.Cols),
				Height: int32(winsz.Rows),
			},
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	rs, err := host.StartSession(s.log, s.rwc, start)
	if err != nil {
		return errors.Wrapf(err, "sending session start message")
	}

	if status != nil {
		status.UpdateStatus("Starting " + start.Name)
	}

	return handleSession(ctx, rs, status, useTerm)
}

func handleSession(ctx context.Context, rs *pbstream.Stream, status Statuser, useTerm bool) error {
	var sc *guestapi.SessionContinue
	for sc == nil {
		var pkt guestapi.Packet

		err := rs.Recv(&pkt)
		if err != nil {
			return errors.Wrapf(err, "error receiving session continue")
		}

		if pkt.Status != "" {
			if status != nil {
				status.UpdateStatus(pkt.Status)
			}
		}

		if pkt.Continue != nil {
			sc = pkt.Continue
		}
	}

	if status != nil {
		status.ClearStatus()
	}

	if sc.Error != "" {
		return fmt.Errorf("remote error: %s", sc.Error)
	}

	if useTerm {
		state, err := terminal.MakeRaw(int(os.Stdout.Fd()))
		if err == nil {
			defer terminal.Restore(int(os.Stdout.Fd()), state)
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, islesignal.CompleteSet...)

	defer signal.Stop(sigCh)

	winCh := make(chan os.Signal, 1)
	signal.Notify(winCh, unix.SIGWINCH)

	defer signal.Stop(winCh)

	var mu sync.Mutex

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case sig := <-sigCh:
				tSig, ok := islesignal.OSToTransport[sig]
				if ok {
					var pkt guestapi.Packet
					pkt.Signal = &guestapi.Packet_Signal{
						Signal: tSig,
					}

					mu.Lock()
					rs.Send(&pkt)
					mu.Unlock()
				}
			case <-winCh:
				winsz, err := pty.GetsizeFull(os.Stdin)
				if err != nil {
					continue
				}

				var pkt guestapi.Packet
				pkt.WindowChange = &guestapi.Packet_WindowSize{
					Width:  int32(winsz.Cols),
					Height: int32(winsz.Rows),
				}

				mu.Lock()
				rs.Send(&pkt)
				mu.Unlock()
			}
		}
	}()

	go func() {
		var pkt guestapi.Packet
		pkt.Channel = guestapi.Packet_STDIN

		buf := make([]byte, 4096)
		mbuf := make([]byte, 4096+48)

		for {
			pkt.Data = nil

			n, err := os.Stdin.Read(buf)
			if err != nil {
				pkt.ChannelClose = true

				mu.Lock()
				rs.Send(&pkt)
				mu.Unlock()
				return
			}

			pkt.Data = buf[:n]

			data := pkt.FastDataOnlyMarshal(mbuf[:0])

			mu.Lock()
			err = rs.SendPremarshaled(data)
			mu.Unlock()

			if err != nil {
				return
			}
		}
	}()

	var pkt guestapi.Packet

	for {
		err := rs.Recv(&pkt)
		if err != nil {
			return errors.Wrapf(err, "error recieving data packet")
		}

		if pkt.Exit != nil {
			break
		}

		switch pkt.Channel {
		case guestapi.Packet_STDOUT:
			if pkt.ChannelClose {
				os.Stdout.Close()
			} else {
				os.Stdout.Write(pkt.Data)
			}
		case guestapi.Packet_STDERR:
			os.Stderr.Write(pkt.Data)
		}
	}

	if pkt.Exit.Code == 0 {
		return nil
	}

	return &ExitError{Code: int(pkt.Exit.Code)}
}
