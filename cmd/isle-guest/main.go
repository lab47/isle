package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/yamux"
	"github.com/lab47/isle/guest"
	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/helper"
	"github.com/lab47/isle/pkg/kcmdline"
	"github.com/lab47/isle/pkg/pbstream"
	"github.com/lab47/isle/pkg/timesync"
	"github.com/mdlayher/vsock"
	"github.com/spf13/pflag"
	"go.etcd.io/bbolt"
	"golang.org/x/sys/unix"

	"net/http"
	_ "net/http/pprof"
)

var (
	fHosted = pflag.BoolP("hosted", "H", false, "run guest inside existing linux env")
	// fClusterId  = pflag.String("cluster-id", "", "cluster id used to assign envs under")
	fListenAddr = pflag.String("listen-addr", "", "address to listen on")
	fDataDir    = pflag.String("data-dir", "/var/lib/isle", "directory to store data in")
	fHostBinds  = pflag.StringToString("host-binds", nil, "paths to bind in all sessions. host=cont pattern")
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "--helper" {
		helper.Main(os.Args[2:])
		return
	}

	pflag.Parse()

	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "guest must run as root\n")
		os.Exit(1)
	}

	go func() {
		fmt.Println(http.ListenAndServe("0.0.0.0:6060", nil))
	}()

	if *fHosted {
		runHosted()
	} else {
		runNative()
	}
}

type runtimeData struct {
	ctx *guest.ResourceContext
	sm  *guest.ShellManager
	cm  *guest.ContainerManager
}

func setupData(ctx context.Context, log hclog.Logger, dataDir string) (*runtimeData, error) {
	os.MkdirAll(dataDir, 0755)

	// defer os.RemoveAll(dataDir)

	dbPath := filepath.Join(dataDir, "data.db")

	db, err := bbolt.Open(dbPath, 0644, bbolt.DefaultOptions)
	if err != nil {
		return nil, err
	}

	var r guest.ResourceStorage
	err = r.Init(log, db)
	if err != nil {
		return nil, err
	}

	rctx := &guest.ResourceContext{
		Context:         ctx,
		ResourceStorage: &r,
	}

	runRoot := filepath.Join(dataDir, "runc")
	err = os.MkdirAll(runRoot, 0755)
	if err != nil {
		return nil, err
	}

	var ipm guest.IPNetworkManager

	netId, err := ipm.Bootstrap(rctx)
	if err != nil {
		return nil, err
	}

	var cm guest.ContainerManager

	id := guest.NewUniqueId()

	user, err := user.Current()
	if err != nil {
		return nil, err
	}

	os.MkdirAll("/run/isle", 0755)

	err = cm.Init(rctx, &guest.ContainerConfig{
		Logger:   log,
		BaseDir:  filepath.Join(dataDir, "containers"),
		HomeDir:  filepath.Join(dataDir, "volumes"),
		NodeId:   id,
		User:     user.Username,
		RuncRoot: filepath.Join(dataDir, "runc"),
		RunDir:   "/run/isle",

		BridgeID: 0,

		HelperPath:     "/usr/bin/isle",
		NetworkManager: &ipm,
	})
	if err != nil {
		return nil, err
	}

	go cm.StartSSHAgent(ctx)

	var sm guest.ShellManager
	sm.L = log
	sm.Containers = &cm
	sm.Network = netId

	err = sm.Init(rctx)
	if err != nil {
		return nil, err
	}

	return &runtimeData{
		ctx: rctx,
		sm:  &sm,
		cm:  &cm,
	}, nil
}

func probIP(log hclog.Logger) string {
	iface, err := net.InterfaceByName("eth0")
	if err != nil {
		log.Error("error getting iface by name", "error", err)
		return ""
	}

	addrs, err := iface.Addrs()
	if err != nil {
		log.Error("error getting iface addrs", "error", err)
		return ""
	}
	for _, a := range addrs {
		log.Info("considering addr", "addr", a)

		switch a := a.(type) {
		case *net.IPNet:
			if a.IP.IsGlobalUnicast() {
				return a.IP.String()
			}
		}
	}

	return ""
}

func setTZFile(log hclog.Logger, name string) {
	path := fmt.Sprintf("/usr/share/zoneinfo/%s", name)

	w, err := os.Open(path)
	if err != nil {
		log.Error("unable to find tz path", "path", path)
		return
	}

	defer w.Close()

	f, err := os.Create("/etc/localtime")
	if err != nil {
		return
	}

	defer f.Close()
	io.Copy(f, w)
}

var (
	ADJ_OFFSET    = 0x0001 /* time offset */
	ADJ_FREQUENCY = 0x0002 /* frequency offset */
	ADJ_MAXERROR  = 0x0004 /* maximum time error */
	ADJ_ESTERROR  = 0x0008 /* estimated time error */
	ADJ_STATUS    = 0x0010 /* clock status */
	ADJ_TIMECONST = 0x0020 /* pll time constant */
	ADJ_TAI       = 0x0080 /* set TAI offset */
	ADJ_SETOFFSET = 0x0100 /* add 'time' to current time */
	ADJ_MICRO     = 0x1000 /* select microsecond resolution */
	ADJ_NANO      = 0x2000 /* select nanosecond resolution */
	ADJ_TICK      = 0x4000 /* tick value */
)

func runNative() {
	// Be sure that when we turn on forwarding via cni, we don't also
	// break the ipv6 info we get from the hypervisor
	ioutil.WriteFile("/proc/sys/net/ipv6/conf/eth0/accept_ra", []byte("2"), 0755)

	vars := kcmdline.CommandLine()

	ctx, cancel := signal.NotifyContext(context.Background(),
		unix.SIGTERM, unix.SIGQUIT, unix.SIGINT,
	)
	defer cancel()

	log := hclog.New(&hclog.LoggerOptions{
		Name:  "guest",
		Level: hclog.Trace,
	})

	dataDir, err := filepath.Abs(*fDataDir)
	if err != nil {
		log.Error("error computing data dir", "error", err)
		os.Exit(1)
	}

	// Make sure our data directory exists
	err = os.MkdirAll(dataDir, 0755)
	if err != nil {
		log.Error("error computing data dir", "error", err)
		os.Exit(1)
	}

	vcfg := &vsock.Config{}

	opener := pbstream.StreamOpenFunc(func() (*pbstream.Stream, error) {
		conn, err := vsock.Dial(vsock.Host, 48, vcfg)
		if err != nil {
			log.Error("error dialing host", "error", err)
			return nil, err
		}
		return pbstream.Open(log, conn)
	})

	startClient := guestapi.PBSNewStartupAPIClient(opener)

	resp, err := startClient.VMRunning(ctx, pbstream.NewRequest(&guestapi.RunningReq{
		Ip: probIP(log),
	}))
	if err != nil {
		log.Error("error making startup call to host", "error", err)
		os.Exit(1)
	}

	log.Info("sync'd with host", "timezone", resp.Value.Timezone)

	setTZFile(log, resp.Value.Timezone)

	go func() {
		log.Info("starting background timesync")

		tsc := timesync.NewClient(
			log,
			timesync.PBSNewTimeSyncClient(opener),
		)

		tsc.Sync(ctx, timesync.SetSystemTime)
	}()

	l, err := vsock.Listen(47, vcfg)
	if err != nil {
		log.Error("error listening on vsock", "error", err)
		os.Exit(1)
	}

	err = guest.Run(ctx, &guest.RunConfig{
		Logger:    log,
		DataDir:   dataDir,
		Listener:  l,
		HostBinds: *fHostBinds,
		ClusterId: vars["cluster_id"],
	})
	if err != nil {
		log.Error("error running guest", "error", err)
	}
}

func runNativeOld() {
	// Be sure that when we turn on forwarding via cni, we don't also
	// break the ipv6 info we get from the hypervisor
	ioutil.WriteFile("/proc/sys/net/ipv6/conf/eth0/accept_ra", []byte("2"), 0755)

	vars := kcmdline.CommandLine()

	g := &guest.Guest{
		L: hclog.New(&hclog.LoggerOptions{
			Name:  "guest",
			Level: hclog.Trace,
		}),
		User:      vars["user_name"],
		ClusterId: vars["cluster_id"],
	}

	ctx, cancel := signal.NotifyContext(context.Background(),
		unix.SIGTERM, unix.SIGQUIT, unix.SIGINT,
	)
	defer cancel()

	err := g.Init(ctx)
	if err != nil {
		g.L.Error("error initializing guest", "error", err)
		os.Exit(1)
	}

	g.L.Info("detected user name", "name", vars["user_name"])

	for {
		g.L.Info("connecting to host")

		vcfg := &vsock.Config{}

		c, err := vsock.Dial(vsock.Host, 47, vcfg)
		if err != nil {
			g.L.Error("unable to connect to hypervisor", "error", err)
			time.Sleep(time.Second)
			continue
		}

		g.L.Info("connected to host")
		if !handleConn(ctx, g, c) {
			break
		}
	}

	g.L.Info("cleaning up containers")

	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	g.Cleanup(ctx)
}

func handleConn(ctx context.Context, g *guest.Guest, c net.Conn) bool {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	cfg := yamux.DefaultConfig()

	sess, err := yamux.Server(c, cfg)
	if err != nil {
		g.L.Info("error negotiating yamux server", "error", err)
		return false
	}

	err = g.Run(ctx, sess)

	g.L.Info("run has stopped", "error", err)

	return errors.Is(err, context.Canceled)
}

func runHosted() {
	log := hclog.New(&hclog.LoggerOptions{
		Name:  "guest",
		Level: hclog.Trace,
	})

	ctx, cancel := signal.NotifyContext(context.Background(),
		unix.SIGTERM, unix.SIGQUIT, unix.SIGINT,
	)
	defer cancel()

	var (
		l   net.Listener
		err error
	)

	addr := *fListenAddr

	if len(addr) >= 2 && addr[0] == '/' {
		l, err = net.Listen("unix", addr)
		if err != nil {
			log.Error("error listening on specified path", "error", err, "path", addr)
			os.Exit(1)
		}
	} else {
		l, err = net.Listen("tcp", addr)
		if err != nil {
			log.Error("error listening on specified port", "error", err, "path", addr)
			os.Exit(1)
		}
	}

	dataDir, err := filepath.Abs(*fDataDir)
	if err != nil {
		log.Error("error computing data dir", "error", err)
		os.Exit(1)
	}

	err = guest.Run(ctx, &guest.RunConfig{
		Logger:    log,
		DataDir:   dataDir,
		Listener:  l,
		HostBinds: *fHostBinds,
	})
	if err != nil {
		log.Error("error running guest", "error", err)
	}
}

/*
func runHosted2() {
	log := hclog.New(&hclog.LoggerOptions{
		Name:  "guest",
		Level: hclog.Trace,
	})

	ctx, cancel := signal.NotifyContext(context.Background(),
		unix.SIGTERM, unix.SIGQUIT, unix.SIGINT,
	)
	defer cancel()

	var (
		l   net.Listener
		err error
	)

	addr := *fListenAddr

	if len(addr) >= 2 && addr[0] == '/' {
		l, err = net.Listen("unix", addr)
		if err != nil {
			log.Error("error listening on specified path", "error", err, "path", addr)
			os.Exit(1)
		}
	} else {
		l, err = net.Listen("tcp", addr)
		if err != nil {
			log.Error("error listening on specified port", "error", err, "path", addr)
			os.Exit(1)
		}
	}

	dataDir, err := filepath.Abs(*fDataDir)
	if err != nil {
		log.Error("error computing data dir", "error", err)
		os.Exit(1)
	}

	rdata, err := setupData(ctx, log, dataDir)
	if err != nil {
		log.Error("error initializing data", "error", err)
		os.Exit(1)
	}

	defer rdata.cm.Close()

	launcher, err := guest.NewShellLauncher(log, rdata.sm, rdata.cm)
	if err != nil {
		log.Error("error creating shell launcher", "error", err)
		rdata.cm.Close()
		os.Exit(1)
	}

	go func() {
		<-ctx.Done()
		l.Close()
	}()

	defer log.Info("shutting down")

	launcher.Listen(rdata.ctx, l)
}
*/
