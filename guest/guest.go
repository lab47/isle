package guest

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/containerd/console"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/go-cni"
	"github.com/fxamacker/cbor/v2"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/yamux"
	"github.com/lab47/yalr4m/pkg/netutil"
	"github.com/lab47/yalr4m/pkg/reaper"
	"github.com/lab47/yalr4m/pkg/runc"
	"github.com/lab47/yalr4m/pkg/ssh"
	"github.com/lab47/yalr4m/pkg/timesync"
	"github.com/lab47/yalr4m/types"
	"github.com/opencontainers/image-spec/identity"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/xid"
	"golang.org/x/sys/unix"

	gossh "github.com/lab47/yalr4m/pkg/crypto/ssh"
)

type runningContainer struct {
	exit   <-chan containerd.ExitStatus
	pid    int
	id     string
	cancel func()
	doneCh chan error
}

type Guest struct {
	L hclog.Logger
	C *containerd.Client

	User string

	cni       cni.CNI
	netconfig *netutil.NetworkConfigList

	wg sync.WaitGroup

	bgCtx    context.Context
	bgCancel func()

	hostAddr string

	mu sync.Mutex

	running map[string]*runningContainer
	reaper  *reaper.Monitor

	sshAgentPath string
}

func (g *Guest) Init(ctx context.Context) error {
	g.running = make(map[string]*runningContainer)

	ce := &netutil.CNIEnv{
		Path:        "/usr/libexec/cni",
		NetconfPath: "/etc/cni/net.d",
	}

	ll, err := netutil.DefaultConfigList(ce)
	if err != nil {
		return err
	}

	g.netconfig = ll

	gc, err := cni.New(
		cni.WithPluginDir([]string{"/usr/libexec/cni"}),
		cni.WithConfListBytes(ll.Bytes),
	)
	if err != nil {
		return err
	}

	g.hostAddr = g.netconfig.Gateway
	g.cni = gc

	ctx, cancel := context.WithCancel(ctx)
	g.bgCtx = ctx
	g.bgCancel = cancel

	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		StartDNS(ctx, g.L)
	}()

	err = reaper.SetSubreaper(1)
	if err != nil {
		return err
	}

	g.reaper = reaper.Default

	sigc := make(chan os.Signal, 128)
	signal.Notify(sigc, unix.SIGCHLD)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-sigc:
				err := reaper.Reap()
				if err != nil {
					g.L.Error("error reaping child processes", "error", err)
				}
			}
		}
	}()

	g.sshAgentPath = "/run/ssh-agent-host.sock"

	return nil
}

func (g *Guest) Cleanup(ctx context.Context) {
	return
	ctx = namespaces.WithNamespace(ctx, "msl")

	snap := g.C.SnapshotService("")
	defer snap.Close()

	containers, err := g.C.ContainerService().List(ctx)
	if err != nil {
		g.L.Error("error listing containers for cleanup", "error", err)
		return
	}

	for _, ci := range containers {
		c, err := g.C.LoadContainer(ctx, ci.ID)
		if err != nil {
			continue
		}

		task, err := c.Task(ctx, nil)
		if err != nil {
			continue
		}

		if rc, ok := g.running[ci.ID]; ok {
			g.L.Info("removing CNI")
			err := g.cni.Remove(ctx, ci.ID, fmt.Sprintf("/proc/%d/ns/net", rc.pid))
			if err != nil {
				g.L.Error("error removing cni", "id", ci.ID, "error", err)
			}
		}

		ch, err := task.Wait(ctx)
		if err != nil {
			g.L.Error("error setting up task wait", "error", err)
			continue
		}

		err = task.Kill(ctx, unix.SIGTERM)
		if err != nil {
			g.L.Error("error killing task", "error", err)
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-ch:
			// ok
		}

		_, err = task.Delete(ctx)
		if err != nil {
			g.L.Error("error deleting task", "error", err)
			continue
		}

		id := xid.New().String()

		labels := map[string]string{"env": ci.ID}

		err = snap.Commit(ctx, id, ci.SnapshotKey, snapshots.WithLabels(labels))
		if err != nil {
			g.L.Error("error commiting filesystem", "error", err)
		}

		g.L.Info("commited snapshot", "snap-id", id, "previous", ci.SnapshotKey)

		err = c.Delete(ctx)
		if err != nil {
			g.L.Error("error deleting container", "error", err)
			continue
		}
	}

	g.bgCancel()
	g.wg.Wait()
}

func (g *Guest) handleSSH(ctx context.Context, s ssh.Session, l *yamux.Session) {
	g.L.Info("handling ssh")

	cmdline := s.Command()

	if len(cmdline) == 0 {
		cmdline = []string{"bash", "-l"}
	}

	g.L.Info("start ssh session", "command", cmdline)

	var info types.MSLInfo

	sp := &specs.Process{
		Env: []string{
			"PATH=/bin:/usr/bin:/sbin:/usr/sbin",
			"SSH_AUTH_SOCK=/tmp/ssh-agent.sock",
		},
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

	volHome := "/vol/user/home/" + g.User

	os.MkdirAll(filepath.Dir(volHome), 0755)

	imgref, err := name.ParseReference(info.Image)
	if err != nil {
		g.L.Error("error parsing image reference", "error", err)
		fmt.Fprintf(s, "error parsing image reference: %s\n", err)
		s.Exit(1)
		return
	}

	ptyReq, winCh, isPty := s.Pty()
	if isPty {
		sp.Terminal = true
		sp.Env = append(sp.Env, fmt.Sprintf("TERM=%s", ptyReq.Term))
	}

	var (
		width int
		setup io.Writer = ioutil.Discard
	)

	if isPty {
		width = ptyReq.Window.Width
		setup = s.Extended(2)
	}

	cinfo := ContainerInfo{
		Name:    info.Name,
		Img:     imgref,
		Status:  setup,
		Width:   width,
		Session: l,
	}

	id, err := g.Container(ctx, cinfo)
	if err != nil {
		g.L.Error("error establishing container", "error", err)
		fmt.Fprintf(s, "error establishing container: %s\n", err)
		s.Exit(1)
		return
	}

	sp.Args = cmdline
	if info.Dir != "" {
		sp.Cwd = info.Dir
	}

	if !info.AsRoot {
		sp.User.UID = 501
		sp.User.GID = 1000
		sp.User.Username = g.User

		if sp.Cwd == "" {
			sp.Cwd = "/home/" + g.User
		}

	} else {
		if sp.Cwd == "" {
			sp.Cwd = "/root"
		}
	}

	r := runc.Runc{
		Debug: true,
	}

	consock, err := runc.NewTempConsoleSocket()
	if err != nil {
		g.L.Error("error establishing console socket", "error", err)
		fmt.Fprintf(s, "error establishing console socket: %s\n", err)
		s.Exit(1)
		return
	}

	started := make(chan int, 1)

	tmpdir, err := ioutil.TempDir("", "yalrm4")
	if err != nil {
		g.L.Error("error creating temp dir", "error", err)
		fmt.Fprintf(s, "error creating temp dir: %s\n", err)
		s.Exit(1)
	}

	defer os.RemoveAll(tmpdir)

	pidPath := filepath.Join(tmpdir, "pid")

	pio, _ := runc.NewSTDIO()

	err = r.Exec(ctx, id, *sp, &runc.ExecOpts{
		IO:            pio,
		ConsoleSocket: consock,
		Detach:        true,
		Started:       started,
		PidFile:       pidPath,
		Terminal:      true,
	})
	if err != nil {
		g.L.Error("error executing command in container", "error", err)
		fmt.Fprintf(s, "error executing command in container: %s\n", err)
		s.Exit(1)
		return
	}

	select {
	case <-ctx.Done():
		err = ctx.Err()
		g.L.Error("error waiting for exec to start", "error", err)
		fmt.Fprintf(s, "error waiting for exec to start: %s\n", err)
		s.Exit(1)
		return
	case <-started:
		// ok
	}

	g.L.Info("reading process tty")

	con, err := consock.ReceiveMaster()
	if err != nil {
		g.L.Error("error setting up terminal", "error", err)
		fmt.Fprintf(s, "error setting up terminal: %s\n", err)
		s.Exit(1)
		return
	}

	defer con.Close()

	con.Resize(console.WinSize{
		Height: uint16(ptyReq.Window.Height),
		Width:  uint16(ptyReq.Window.Width),
	})

	if isPty {
		go func() {
			for win := range winCh {
				con.Resize(console.WinSize{
					Height: uint16(win.Height),
					Width:  uint16(win.Width),
				})
			}
		}()
	}

	g.L.Info("reading pid file")

	data, err := ioutil.ReadFile(pidPath)
	if err != nil {
		g.L.Error("error reading pid file", "error", err)
		fmt.Fprintf(s, "error reading pid file: %s\n", err)
		s.Exit(1)
		return
	}

	processPid, err := strconv.Atoi(string(data))
	if err != nil {
		g.L.Error("error parsing pid file", "error", err)
		fmt.Fprintf(s, "error parsing pid file: %s\n", err)
		s.Exit(1)
		return
	}

	ch := g.reaper.Subscribe()
	defer g.reaper.Unsubscribe(ch)

	go func() {
		defer con.Close()
		io.Copy(s, con)
	}()

	go func() {
		defer con.Close()
		io.Copy(con, s)
	}()

	var exitStatus runc.Exit

	g.L.Info("waiting on exit status")

loop:
	for {
		select {
		case <-ctx.Done():
			return
		case exit := <-ch:
			g.L.Info("exit detected", "pid", exit.Pid, "pidfile", processPid)

			exitStatus = exit
			break loop
		}
	}

	code := exitStatus.Status

	g.L.Info("session has exitted", "code", code)

	s.Exit(int(code))
}

func (g *Guest) setupSnapshot(ctx context.Context, id string, i containerd.Image) (string, error) {
	s := g.C.SnapshotService("")
	defer s.Close()

	var (
		snapId    string
		updatedAt time.Time
	)

	s.Walk(ctx, func(c context.Context, i snapshots.Info) error {
		if i.Updated.After(updatedAt) {
			snapId = i.Name
			updatedAt = i.Updated
		}
		return nil
	}, "labels.env="+id)

	if snapId != "" {
		return snapId, nil
	}

	snapId = xid.New().String()

	diffIDs, err := i.RootFS(ctx)
	if err != nil {
		return "", err
	}

	parent := identity.ChainID(diffIDs).String()

	_, err = s.Prepare(ctx, snapId, parent, snapshots.WithLabels(map[string]string{"env": id}))
	if err != nil {
		return "", err
	}

	return snapId, nil
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

	os.Remove(g.sshAgentPath)

	go g.forwardSSHAgent(ctx, l)

	go func() {
		<-ctx.Done()
		l.Close()
	}()

	g.L.Info("starting connection handler")
	for {
		stream, err := l.AcceptStream()
		if err != nil {
			return err
		}

		go g.HandleConn(stream, sshServ)
	}
}

func (g *Guest) forwardSSHAgent(ctx context.Context, sess *yamux.Session) {
	l, err := net.Listen("unix", g.sshAgentPath)
	if err != nil {
		g.L.Error("error listening on ssh agent path", "error", err, "path", g.sshAgentPath)
		return
	}

	defer l.Close()

	os.Chmod(g.sshAgentPath, 0777)

	for {
		c, err := l.Accept()
		if err != nil {
			return
		}

		host, err := sess.Open()
		if err != nil {
			c.Close()
			g.L.Error("error opening channel to host", "error", err)
			continue
		}

		enc := cbor.NewEncoder(host)
		enc.Encode(types.HeaderMessage{Kind: "ssh-agent"})

		dec := cbor.NewDecoder(host)

		var resp types.ResponseMessage

		dec.Decode(&resp)

		if resp.Code != types.OK {
			g.L.Error("error establishing agent connection", "remote-error", types.Error)
			c.Close()
			continue
		}

		g.L.Info("established connection to ssh-agent")

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

		c, err := net.Dial("tcp", fmt.Sprintf("%s:%d", pf.Key, pf.Port))
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

	case "timesync":
		enc.Encode(types.ResponseMessage{
			Code: types.OK,
		})

		timesync.Guest(g.bgCtx, g.L, conn)
	}
}

func (g *Guest) monitorPorts(ctx context.Context, sess *yamux.Session, target string, path string) {
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
			g.readPorts(sess, target, path, ports)
		case <-sigCh:
			g.readPorts(sess, target, path, ports)
		}
	}
}

func (g *Guest) readPorts(sess *yamux.Session, target string, path string, ports map[int64]struct{}) {
	f, err := os.Open(path)
	if err != nil {
		g.L.Error("error reading tcp listing", "error", err, "path", path)
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

		err = enc.Encode(types.PortForwardMessage{
			Port: int(numPort),
			Key:  target,
		})
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

			err = enc.Encode(types.PortForwardMessage{
				Port: int(p),
				Key:  target,
			})
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
