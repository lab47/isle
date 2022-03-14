package guest

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	"github.com/fxamacker/cbor/v2"
	"github.com/gliderlabs/ssh"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/yalr4m/types"
	"github.com/hashicorp/yamux"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/xid"
	"golang.org/x/sys/unix"

	gossh "golang.org/x/crypto/ssh"
)

type Guest struct {
	L hclog.Logger
	C *containerd.Client

	mu sync.Mutex
}

func (g *Guest) handleSSH(ctx context.Context, s ssh.Session, l *yamux.Session) {
	g.L.Info("handling ssh")

	x := s.Command()

	if len(x) == 0 {
		x = []string{"bash", "-l"}
	}

	g.L.Info("start ssh session", "command", x)

	var info types.MSLInfo

	sp := &specs.Process{
		Env: []string{"PATH=/bin:/usr/bin:/sbin:/usr/sbin"},
	}

	for _, str := range s.Environ() {
		idx := strings.IndexByte(str, '=')
		if idx != -1 {
			key := str[:idx]
			val := str[idx+1:]

			switch key {
			case "_MSL_INFO":
				json.Unmarshal([]byte(val), &info)
				continue
			}
		}

		sp.Env = append(sp.Env, str)
	}

	if info.Image == "" {
		g.L.Warn("no image set, defaulting to ubuntu")
		info.Image = "docker.io/library/ubuntu:latest"

		if info.Name == "" {
			info.Name = "ubuntu"
		}
	}

	if info.Name == "" {
		g.L.Warn("name not set, defaulting to msl")
		info.Name = "msl"
	}

	var task containerd.Task

	g.mu.Lock()
	if g.C == nil {
		client, err := containerd.New("/run/containerd/containerd.sock")
		if err != nil {
			g.mu.Unlock()

			g.L.Error("error connecting to containerd", "error", err)
			fmt.Fprintf(s, "error connecting to containerd: %s\n", err)
			s.Exit(1)
			return
		}

		g.C = client
	}
	g.mu.Unlock()

	container, err := g.C.LoadContainer(ctx, info.Name)
	if err != nil {
		if !errors.Is(err, errdefs.ErrNotFound) {
			g.L.Error("error loading container", "error", err)
			fmt.Fprintf(s, "error loading container: %s\n", err)
			s.Exit(1)
			return
		}

		image, err := g.C.Pull(ctx, info.Image, containerd.WithPullUnpack)
		if err != nil {
			g.L.Error("error pulling image", "error", err)
			fmt.Fprintf(s, "error pulling image: %s\n", err)
			s.Exit(1)
			return
		}

		specopts := oci.WithImageConfig(image)

		container, err = g.C.NewContainer(ctx,
			info.Name,
			containerd.WithImage(image),
			containerd.WithNewSnapshot(info.Name, image),
			containerd.WithNewSpec(specopts,
				oci.WithMounts([]specs.Mount{
					{
						Destination: "/share",
						Type:        "bind",
						Source:      "/share",
						Options:     []string{"rbind", "rshared", "rw"},
					},
					{
						Destination: "/vol",
						Type:        "bind",
						Source:      "/vol",
						Options:     []string{"rbind", "rshared", "rw"},
					},
					{
						Destination: "/run/containerd/containerd.sock",
						Type:        "bind",
						Source:      "/run/containerd/containerd.sock",
						Options:     []string{"rbind", "rshared", "rw"},
					},
					{
						Destination: "/var/run/docker.sock",
						Type:        "bind",
						Source:      "/var/run/docker.sock",
						Options:     []string{"rbind", "rshared", "rw"},
					},
				}),
				oci.WithHostname(info.Name),
				oci.WithHostResolvconf,
				oci.WithHostNamespace(specs.NetworkNamespace),
				oci.WithPrivileged,
				oci.WithAllDevicesAllowed,
				oci.WithHostDevices,
				oci.WithNewPrivileges,
			),
		)

		if err != nil {
			g.L.Error("error creating container", "error", err)
			fmt.Fprintf(s, "error creating container: %s\n", err)
			s.Exit(1)
			return
		}

		g.L.Info("performing setup of container")
		task, err = container.NewTask(ctx, cio.NullIO)
		if err != nil {
			g.L.Error("error creating task", "error", err)
			fmt.Fprintf(s, "error creating task: %s\n", err)
			s.Exit(1)
			return
		}

		var setupSp specs.Process
		setupSp.Args = []string{"/bin/sh", "-c", "mkdir -p /etc/sudoers.d; echo 'evan ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/00-evan; echo root:root | chpasswd; useradd -u 501 -m evan || adduser -u 501 -h /home/evan evan; echo evan:evan | chpasswd"}
		setupSp.Env = []string{"PATH=/bin:/sbin:/usr/bin:/usr/sbin"}
		setupSp.Cwd = "/"

		proc, err := task.Exec(ctx, "setup", &setupSp, cio.LogFile("/var/log/setup"))
		if err != nil {
			g.L.Error("error creating task", "error", err)
			fmt.Fprintf(s, "error creating task: %s\n", err)
			s.Exit(1)
			return
		}

		setupStatus, err := proc.Wait(ctx)
		if err != nil {
			g.L.Error("error creating task", "error", err)
			fmt.Fprintf(s, "error creating task: %s\n", err)
			s.Exit(1)
			return
		}

		err = proc.Start(ctx)
		if err != nil {
			g.L.Error("error creating task", "error", err)
			fmt.Fprintf(s, "error creating task: %s\n", err)
			s.Exit(1)
			return
		}

		exit := <-setupStatus

		err = exit.Error()
		if err != nil {
			g.L.Error("error performing container setup", "error", err)
			fmt.Fprintf(s, "error performing container setup: %s\n", err)
			s.Exit(int(exit.ExitCode()))
			return
		}

		task.CloseIO(ctx)
	}

	id := xid.New().String()

	task, err = container.Task(ctx, nil)
	if err != nil {
		if !errors.Is(err, errdefs.ErrNotFound) {
			g.L.Error("error creating task", "error", err)
			fmt.Fprintf(s, "error creating task: %s\n", err)
			s.Exit(1)
			return
		}

		g.L.Info("creating new task for container")
		task, err = container.NewTask(ctx, cio.NullIO)
		if err != nil {
			g.L.Error("error creating task", "error", err)
			fmt.Fprintf(s, "error creating task: %s\n", err)
			s.Exit(1)
			return
		}
	}

	/*
		defer func() {
			task.Kill(ctx, syscall.SIGTERM)
			task.Delete(ctx)
		}()
	*/

	var taskIO cio.Creator

	ptyReq, winCh, isPty := s.Pty()
	if isPty {
		sp.Terminal = true
		taskIO = cio.NewCreator(cio.WithStreams(s, s, nil), cio.WithTerminal)
		sp.Env = append(sp.Env, fmt.Sprintf("TERM=%s", ptyReq.Term))
	} else {
		taskIO = cio.NewCreator(cio.WithStreams(s, s, s))
	}

	sp.Args = x
	if info.Dir != "" {
		sp.Cwd = info.Dir
	}

	if !info.AsRoot {
		sp.User.UID = 501
		sp.User.GID = 1000
		sp.User.Username = "evan"

		if sp.Cwd == "" {
			sp.Cwd = "/home/evan"
		}
	} else {
		if sp.Cwd == "" {
			sp.Cwd = "/root"
		}
	}

	process, err := task.Exec(ctx, id, sp, taskIO)
	if err != nil {
		g.L.Error("error execing task", "error", err)
		fmt.Fprintf(s, "error execing task: %s\n", err)
		s.Exit(1)
		return
	}

	task.Resize(ctx, uint32(ptyReq.Window.Width), uint32(ptyReq.Window.Height))

	statusC, err := process.Wait(ctx)
	if err != nil {
		g.L.Error("error waiting on process", "error", err)
		fmt.Fprintf(s, "error waiting on process: %s\n", err)
		s.Exit(1)
		return
	}

	if err := process.Start(ctx); err != nil {
		g.L.Error("error starting process", "error", err)
		fmt.Fprintf(s, "error staritng process: %s\n", err)
		s.Exit(1)
		return
	}

	if isPty {
		go func() {
			for win := range winCh {
				task.Resize(ctx, uint32(win.Width), uint32(win.Height))
			}
		}()
	} else {
		sigc := commands.ForwardAllSignals(ctx, process)
		defer commands.StopCatch(sigc)
	}

	status := <-statusC
	code, _, _ := status.Result()

	if code != 0 {
		s.Exit(int(code))
		return
	}

}

func (g *Guest) Run(ctx context.Context, l *yamux.Session) error {
	ctx = namespaces.WithNamespace(ctx, "msl")

	var key *rsa.PrivateKey

	f, err := os.Open("/data/ssh.key")
	if err == nil {
		data, err := io.ReadAll(f)
		if err != nil {
			return err
		}

		f.Close()

		key, _ = x509.ParsePKCS1PrivateKey(data)
	}

	if key == nil {
		key, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return err
		}

		f, err := os.Create("/data/ssh.key")
		if err == nil {
			f.Write(x509.MarshalPKCS1PrivateKey(key))
			f.Close()
		}
	}

	signer, err := gossh.NewSignerFromKey(key)
	if err != nil {
		return err
	}

	sshServ := &ssh.Server{
		HostSigners: []ssh.Signer{signer},
		Handler: func(s ssh.Session) {
			g.handleSSH(ctx, s, l)
		},
		ConnectionFailedCallback: func(conn net.Conn, err error) {
			g.L.Error("failed to negotation ssh", "error", err)
		},
	}

	sshServ.RequestHandlers = map[string]ssh.RequestHandler{}
	for k, v := range ssh.DefaultRequestHandlers {
		sshServ.RequestHandlers[k] = v
	}

	sshServ.ChannelHandlers = map[string]ssh.ChannelHandler{}
	for k, v := range ssh.DefaultChannelHandlers {
		sshServ.ChannelHandlers[k] = v
	}

	sshServ.SubsystemHandlers = map[string]ssh.SubsystemHandler{}
	for k, v := range ssh.DefaultSubsystemHandlers {
		sshServ.SubsystemHandlers[k] = v
	}

	go g.monitorPorts(ctx, l)

	g.L.Info("starting connection handler")
	for {
		stream, err := l.AcceptStream()
		if err != nil {
			return err
		}

		go g.HandleConn(stream, sshServ)
	}
}

type conn struct {
	io.Reader
	net.Conn
}

func (c *conn) Read(b []byte) (int, error) {
	return c.Reader.Read(b)
}

func (g *Guest) HandleConn(c net.Conn, serv *ssh.Server) {
	defer c.Close()

	r := bufio.NewReader(c)
	prefix, err := r.Peek(1)
	if err != nil {
		g.L.Error("error peeking new connection", "error", err)
		return
	}

	ic := &conn{Reader: r, Conn: c}

	if prefix[0] == types.ProtocolByte {
		r.Discard(1)
		g.L.Info("handling custom protocol")
		g.handleProtocol(ic)
	} else {
		g.L.Info("handling ssh protocol")
		serv.HandleConn(ic)
	}
}

func (g *Guest) handleProtocol(conn net.Conn) {
	dec := cbor.NewDecoder(conn)
	enc := cbor.NewEncoder(conn)

	var hdr types.HeaderMessage

	err := dec.Decode(&hdr)
	if err != nil {
		g.L.Error("error decoding header", "error", err)
		return
	}

	switch hdr.Kind {
	default:
		enc.Encode(types.ResponseMessage{
			Code:  types.Error,
			Error: fmt.Sprintf("unknown event type: %s", hdr.Kind),
		})
	case "port-forward":
		var pf types.PortForwardMessage

		err = dec.Decode(&pf)
		if err != nil {
			g.L.Error("error decoding port-forward message")
			return
		}

		c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", pf.Port))
		if err != nil {
			enc.Encode(types.ResponseMessage{
				Code:  types.Error,
				Error: err.Error(),
			})
			return
		}

		err = enc.Encode(types.ResponseMessage{
			Code: types.OK,
		})
		if err != nil {
			g.L.Error("error decoding port-forward message")
			return
		}

		g.L.Info("connection from host, forwarding", "port", pf.Port)

		go func() {
			defer conn.Close()
			defer c.Close()

			_, err = io.Copy(c, conn)
			if err != nil {
				g.L.Debug("copy to local ended", "error", err)
			}
		}()

		defer c.Close()
		defer conn.Close()

		_, err = io.Copy(conn, c)
		if err != nil {
			g.L.Debug("copy to guest ended", "error", err)
		}

		g.L.Info("connection from host has ended")

	case "shutdown":
		g.L.Info("executing shutdown according to control message")
		enc.Encode(types.ResponseMessage{
			Code: types.OK,
		})

		cmd := exec.Command("/sbin/halt")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		cmd.Run()
	}
}

func (g *Guest) monitorPorts(ctx context.Context, sess *yamux.Session) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	ports := map[int64]struct{}{}

	sigCh := make(chan os.Signal, 1)

	signal.Notify(sigCh, unix.SIGHUP)

	defer signal.Reset(unix.SIGHUP)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.readPorts(sess, ports)
		case <-sigCh:
			g.readPorts(sess, ports)
		}
	}
}

func (g *Guest) readPorts(sess *yamux.Session, ports map[int64]struct{}) {
	f, err := os.Open("/proc/net/tcp")
	if err != nil {
		g.L.Error("error reading /proc/net/tcp", "error", err)
		return
	}

	defer f.Close()

	br := bufio.NewReader(f)

	// discard header line
	br.ReadString('\n')

	curPorts := map[int64]struct{}{}

	for {
		line, err := br.ReadString('\n')
		if err != nil {
			break
		}

		parts := strings.Fields(line)

		local := parts[1]
		remote := parts[2]

		if remote != "00000000:0000" {
			continue
		}

		colon := strings.IndexByte(local, ':')
		if colon == -1 {
			continue
		}

		addr := local[:colon]
		port := local[colon+1:]

		if addr != "00000000" {
			continue
		}

		numPort, err := strconv.ParseInt(port, 16, 64)
		if err != nil {
			g.L.Error("error parsing port", "error", err, "port", port)
			continue
		}

		curPorts[numPort] = struct{}{}

		if _, ok := ports[numPort]; ok {
			continue
		}

		host, err := sess.Open()
		if err != nil {
			g.L.Error("error opening connection to host", "error", err)
			continue
		}

		enc := cbor.NewEncoder(host)
		dec := cbor.NewDecoder(host)

		g.L.Info("requesting port to be forwarded", "port", numPort)

		err = enc.Encode(types.HeaderMessage{Kind: "port-forward"})
		if err != nil {
			g.L.Error("error encoding message to host", "error", err)
			continue
		}

		err = enc.Encode(types.PortForwardMessage{Port: int(numPort)})
		if err != nil {
			g.L.Error("error encoding message to host", "error", err)
			continue
		}

		var resp types.ResponseMessage

		err = dec.Decode(&resp)
		if err != nil {
			g.L.Error("error decoding message to host", "error", err)
			continue
		}

		if resp.Code != types.OK {
			g.L.Error("host reported error listening on port", "error", resp.Error)
			continue
		}

		g.L.Info("confirmed port being forwarded", "port", numPort)
		ports[numPort] = struct{}{}
	}

	for p := range ports {
		if _, ok := curPorts[p]; !ok {
			// cancel the forwarder

			delete(ports, p)

			host, err := sess.Open()
			if err != nil {
				g.L.Error("error opening connection to host", "error", err)
				continue
			}

			enc := cbor.NewEncoder(host)
			dec := cbor.NewDecoder(host)

			g.L.Info("requesting port to be no longer be forwarded", "port", p)

			err = enc.Encode(types.HeaderMessage{Kind: "cancel-port-forward"})
			if err != nil {
				g.L.Error("error encoding message to host", "error", err)
				continue
			}

			err = enc.Encode(types.PortForwardMessage{Port: int(p)})
			if err != nil {
				g.L.Error("error encoding message to host", "error", err)
				continue
			}

			var resp types.ResponseMessage

			err = dec.Decode(&resp)
			if err != nil {
				g.L.Error("error decoding message to host", "error", err)
				continue
			}

			if resp.Code == types.OK {
				g.L.Info("canceled port forward with host")
			} else {
				g.L.Error("host reported error canceling port", "error", resp.Error)
				continue
			}
		}
	}
}
