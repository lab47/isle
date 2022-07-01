package guest

import (
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"

	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/pkg/pbstream"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/samber/do"
	"golang.org/x/exp/slices"
	"golang.org/x/sys/unix"
)

type ShellLauncher struct {
	L hclog.Logger

	sm *ShellManager
	cm *ContainerManager
}

func NewShellLauncher(inj *do.Injector) (*ShellLauncher, error) {
	log, err := do.Invoke[hclog.Logger](inj)
	if err != nil {
		return nil, err
	}

	cm, err := do.Invoke[*ContainerManager](inj)
	if err != nil {
		return nil, err
	}

	sm, err := do.Invoke[*ShellManager](inj)
	if err != nil {
		return nil, err
	}

	sl := &ShellLauncher{
		L:  log,
		sm: sm,
		cm: cm,
	}

	return sl, nil
}

func (sl *ShellLauncher) Listen(ctx *ResourceContext, l net.Listener) error {
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}

		go sl.handle(ctx, c)
	}
}

func (sl *ShellLauncher) handle(ctx *ResourceContext, c net.Conn) {
	defer c.Close()

	rs, err := pbstream.Open(sl.L, c)
	if err != nil {
		sl.L.Error("error starting pbstream", "error", err)
		return
	}

	var ss guestapi.SessionStart

	err = rs.Recv(&ss)
	if err != nil {
		sl.L.Error("error processing session start", "error", err)
		return
	}

	sl.StartSession(ctx, rs, &ss)
}

func (sl *ShellLauncher) StartSession(ctx *ResourceContext, rs *pbstream.Stream, ss *guestapi.SessionStart) {
	sl.L.Debug("looking up shell session", "name", ss.Name)

	res, err := sl.sm.Lookup(ctx, ss.Name)
	if err != nil {
		sl.L.Error("error looking up session", "error", err)

		rs.Send(&guestapi.Packet{
			Continue: &guestapi.SessionContinue{
				Error: err.Error(),
			},
		})

		return
	}

	if res == nil {
		sl.L.Debug("no shell session found, spawning one", "name", ss.Name, "image", ss.Image)

		res, err = sl.sm.Create(ctx, &guestapi.ShellSession{
			Name:  ss.Name,
			Image: ss.Image,
			User:  ss.User,
			Home:  ss.Home,

			PortForward: ss.PortForward,
		})
		if err != nil {
			rs.Send(&guestapi.Packet{
				Continue: &guestapi.SessionContinue{
					Error: err.Error(),
				},
			})

			sl.L.Error("error creating new session", "error", err)
			return
		}
	} else {
		sl.L.Debug("found existing shell session", "id", res.Id.Short())
	}

	sig := ctx.ProvisionChangeSelector()

	ch := sig.Register(res.Id.Short())
	defer sig.Unregister(ch)

	var provStatus *guestapi.ProvisionStatus

	if res.ProvisionStatus.Status == guestapi.ProvisionStatus_RUNNING {
		provStatus = res.ProvisionStatus
	} else {
	loop:
		for {
			select {
			case <-ctx.Done():
				return
			case change := <-ch:
				sl.L.Debug("session provision status change", "status", change.Status.Status)

				if change.Status.StatusDetails != "" {
					rs.Send(&guestapi.Packet{
						Status: change.Status.StatusDetails,
					})
				}

				switch change.Status.Status {
				case guestapi.ProvisionStatus_ADDING:
					// ok
				case guestapi.ProvisionStatus_DEAD:
					// Delete the session because it didn't start
					ctx.Delete(ctx, res.Id)

					lerr := change.Status.LastError

					if lerr == "" {
						lerr = "container creation error occured"
					}

					rs.Send(&guestapi.Packet{
						Continue: &guestapi.SessionContinue{
							Error: lerr,
						},
					})

					sl.L.Error("session died before starting", "error", lerr)
					return
				case guestapi.ProvisionStatus_RUNNING:
					provStatus = change.Status
					break loop
				}
			}
		}
	}

	if provStatus.ContainerRef == nil {
		msg := "session did not register container"
		rs.Send(&guestapi.Packet{
			Continue: &guestapi.SessionContinue{
				Error: msg,
			},
		})

		sl.L.Error("session failed to register a container")
		return
	}

	contRes, err := sl.cm.Read(ctx, provStatus.ContainerRef)
	if err != nil {
		rs.Send(&guestapi.Packet{
			Continue: &guestapi.SessionContinue{
				Error: err.Error(),
			},
		})

		sl.L.Error("error reading container resource", "error", err)
		return
	}

	as := &activeSession{
		sl:  sl,
		ss:  ss,
		res: contRes,
		rs:  rs,
		// conn: c,
	}

	as.runCommand(ctx)
}

var Path = []string{
	"/bin", "/usr/bin", "/sbin", "/usr/sbin",
	"/usr/local/bin", "/usr/local/sbin",
	"/run/share/bin", "/run/share/sbin",
	"/opt/isle/bin",
}

type activeSession struct {
	sl   *ShellLauncher
	ss   *guestapi.SessionStart
	res  *guestapi.Resource
	rs   *pbstream.Stream
	conn net.Conn

	proc specs.Process

	sshPath string
}

func (s *activeSession) setupProc() {
	s.proc = specs.Process{
		Args: s.ss.Args,
		Cwd:  "/",
		Env: []string{
			"SSH_AUTH_SOCK=/run/services/ssh-agent.sock",
		},
	}

	if s.ss.User != nil {
		s.proc.User = specs.User{
			UID: uint32(s.ss.User.Uid),
			GID: uint32(s.ss.User.Gid),
		}

		s.proc.Cwd = fmt.Sprintf("/home/%s", s.ss.User.Username)
	}

	if s.ss.Pty != nil {
		s.proc.Terminal = true
		if s.ss.Pty.Term != "" {
			s.proc.Env = append(s.proc.Env, "TERM="+s.ss.Pty.Term)
		}

		if ws := s.ss.Pty.WindowSize; ws != nil {
			s.proc.ConsoleSize = &specs.Box{
				Height: uint(ws.Height),
				Width:  uint(ws.Width),
			}
		}
	}

	basePath := slices.Clone(Path)

	// We give first preference to any paths that the user sent it, and then
	// add our own.
	var strPath string

	for _, ev := range s.ss.Env {
		if ev.Key == "PATH" {
			for _, part := range filepath.SplitList(ev.Value) {
				// Remove it from our fixed end path
				if idx := slices.Index(basePath, part); idx != -1 {
					basePath = slices.Delete(basePath, idx, idx+1)
				}
			}
			strPath = ev.Value
		} else {
			s.proc.Env = append(s.proc.Env, ev.Key+"="+ev.Value)
		}
	}

	if len(strPath) > 0 {
		strPath += ":" + strings.Join(basePath, ":")
	} else {
		strPath = strings.Join(basePath, ":")
	}

	s.proc.Env = append(s.proc.Env, "PATH="+strPath)
}

func (s *activeSession) runCommand(ctx *ResourceContext) {
	s.setupProc()

	s.sl.L.Debug("launching command in container", "id", s.res.Id.Short())

	if s.ss.Pty != nil {
		s.runInTerminal(ctx)
	} else {
		s.runWithoutTerminal(ctx)
	}
}

func (s *activeSession) runWithoutTerminal(ctx *ResourceContext) {
	exitCh := s.sl.cm.reaper.Subscribe()
	defer s.sl.cm.reaper.Unsubscribe(exitCh)

	es, err := s.sl.cm.ExecToStream(ctx, s.res, s.proc)
	if err != nil {
		s.rs.Send(&guestapi.Packet{
			Continue: &guestapi.SessionContinue{
				Error: err.Error(),
			},
		})

		s.sl.L.Error("error spawning session within container", "error", err)
		return
	}

	err = s.rs.Send(&guestapi.Packet{
		Continue: &guestapi.SessionContinue{
			Pid: int32(es.Pid),
		},
	})

	if err != nil {
		s.sl.L.Error("error sending session continue", "error", err)
		return
	}

	go func() {
		var writePkt guestapi.Packet

		for {
			err = s.rs.Recv(&writePkt)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					s.sl.L.Error("error recieving write packet", "error", err)
				}
				return
			}

			_, err = es.Stdin.Write(writePkt.Data)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					s.sl.L.Error("error writing data to terminal", "error", err)
				}
				return
			}
		}
	}()

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)

		readBuf := make([]byte, 1024)
		var readPkt guestapi.Packet

		for {
			n, err := es.Stdout.Read(readBuf)
			if err != nil {
				return
			}

			readPkt.Channel = guestapi.Packet_STDOUT
			readPkt.Data = readBuf[:n]

			// s.sl.L.Trace("transmitting data", "packet", spew.Sdump(&readPkt))

			err = s.rs.Send(&readPkt)
			if err != nil {
				s.sl.L.Error("error sending stdout data", "error", err)
				return
			}
		}
	}()

	var exitCode int

loop:
	for {
		select {
		case <-ctx.Done():
			exitCode = -1
			unix.Kill(es.Pid, unix.SIGKILL)
			break loop
		case exit := <-exitCh:
			if exit.Pid == es.Pid {
				exitCode = exit.Status
				break loop
			}
		}
	}

	select {
	case <-ctx.Done():
		return
	case <-readDone:
		// ok
	}

	s.sl.L.Debug("exec finished", "code", exitCode)

	err = s.rs.Send(&guestapi.Packet{
		Exit: &guestapi.Packet_Exit{
			Code: int32(exitCode),
		},
	})
	if err != nil {
		s.sl.L.Error("error transmitting exit", "error", err)
	}
}

func (s *activeSession) runInTerminal(ctx *ResourceContext) {
	exitCh := s.sl.cm.reaper.Subscribe()
	defer s.sl.cm.reaper.Unsubscribe(exitCh)

	ts, err := s.sl.cm.ExecInTerminal(ctx, s.res, s.proc)
	if err != nil {
		s.rs.Send(&guestapi.Packet{
			Continue: &guestapi.SessionContinue{
				Error: err.Error(),
			},
		})

		s.sl.L.Error("error spawning session within container", "error", err)
		return
	}

	err = s.rs.Send(&guestapi.Packet{
		Continue: &guestapi.SessionContinue{
			Pid: int32(ts.Pid),
		},
	})
	if err != nil {
		s.sl.L.Error("error sending session continue", "error", err)
		return
	}

	go func() {
		var writePkt guestapi.Packet

		for {
			err = s.rs.Recv(&writePkt)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					s.sl.L.Error("error recieving write packet", "error", err)
				}
				return
			}

			_, err = ts.Write(writePkt.Data)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					s.sl.L.Error("error writing data to terminal", "error", err)
				}
				return
			}
		}
	}()

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)

		readBuf := make([]byte, 1024)
		var readPkt guestapi.Packet

		for {
			n, err := ts.Read(readBuf)
			if err != nil {
				return
			}

			readPkt.Channel = guestapi.Packet_STDOUT
			readPkt.Data = readBuf[:n]

			// s.sl.L.Trace("transmitting data", "packet", spew.Sdump(&readPkt))

			err = s.rs.Send(&readPkt)
			if err != nil {
				s.sl.L.Error("error sending stdout data", "error", err)
				return
			}
		}
	}()

	var exitCode int

loop:
	for {
		select {
		case <-ctx.Done():
			exitCode = -1
			unix.Kill(ts.Pid, unix.SIGKILL)
			break loop
		case exit := <-exitCh:
			if exit.Pid == ts.Pid {
				exitCode = exit.Status
				break loop
			}
		}
	}

	select {
	case <-ctx.Done():
		return
	case <-readDone:
		// ok
	}

	s.sl.L.Debug("exec finished", "code", exitCode)

	err = s.rs.Send(&guestapi.Packet{
		Exit: &guestapi.Packet_Exit{
			Code: int32(exitCode),
		},
	})
	if err != nil {
		s.sl.L.Error("error transmitting exit", "error", err)
	}
}

func (sl *ShellLauncher) forwardContainer(
	ctx *ResourceContext,
	ss *guestapi.SessionStart,
	res *guestapi.Resource,
	rs *pbstream.Stream,
	conn net.Conn,
) {

}
