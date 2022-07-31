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
	"path/filepath"

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
	fHelperPath = pflag.String("helper-path", "", "path to isle-helper")
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

	eventCh := make(chan guest.RunEvent, 1)

	go func() {
		select {
		case <-ctx.Done():
			return
		case ev := <-eventCh:
			if ev.Type == guest.Running {
				log.Info("signaling to host that we're ready")
				resp, err := startClient.VMRunning(ctx, pbstream.NewRequest(&guestapi.RunningReq{
					Ip: probIP(log),
				}))
				if err != nil {
					log.Error("error making startup call to host", "error", err)
				}
				log.Info("sync'd with host", "timezone", resp.Value.Timezone)

				setTZFile(log, resp.Value.Timezone)
			}
		}
	}()

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

	helperPath := *fHelperPath
	if helperPath != "" {
		helperPath, err = filepath.Abs(helperPath)
		if err != nil {
			log.Error("invalid helper path", "error", err)
			os.Exit(1)
		}
	}

	err = guest.Run(ctx, &guest.RunConfig{
		Logger:     log,
		DataDir:    dataDir,
		Listener:   l,
		HostBinds:  *fHostBinds,
		ClusterId:  vars["cluster_id"],
		EventsCh:   eventCh,
		HelperPath: helperPath,
	})
	if err != nil {
		log.Error("error running guest", "error", err)
	}
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
		os.RemoveAll(addr)
		l, err = net.Listen("unix", addr)
		if err != nil {
			log.Error("error listening on specified path", "error", err, "path", addr)
			os.Exit(1)
		}
		os.Chmod(addr, 0777)
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

	helperPath := *fHelperPath
	if helperPath != "" {
		helperPath, err = filepath.Abs(helperPath)
		if err != nil {
			log.Error("invalid helper path", "error", err)
			os.Exit(1)
		}
	}

	err = guest.Run(ctx, &guest.RunConfig{
		Logger:     log,
		DataDir:    dataDir,
		Listener:   l,
		HostBinds:  *fHostBinds,
		HelperPath: helperPath,
	})
	if err != nil {
		log.Error("error running guest", "error", err)
	}
}
