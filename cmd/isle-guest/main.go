package main

import (
	"context"
	"errors"
	"io/ioutil"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/containerd/containerd/namespaces"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/yamux"
	"github.com/lab47/isle/guest"
	"github.com/lab47/isle/helper"
	"github.com/lab47/isle/pkg/kcmdline"
	"github.com/mdlayher/vsock"
	"golang.org/x/sys/unix"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "--helper" {
		helper.Main(os.Args[2:])
		return
	}

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

	ctx = namespaces.WithNamespace(ctx, "isle")

	err := g.Init(ctx)
	if err != nil {
		g.L.Error("error initializing guest", "error", err)
		os.Exit(1)
	}

	/*
		ref, err := name.ParseReference("ubuntu")
		if err != nil {
			panic(err)
		}

		err = g.StartContainer(ctx, "ubuntu", ref)

		g.L.Info("started container", "error", err)
		os.Exit(0)
	*/

	g.L.Info("detected user name", "name", vars["user_name"])

	vcfg := &vsock.Config{}

	vl, err := vsock.Listen(48, nil)
	if err != nil {
		g.L.Error("error listening on vsock 48", "error", err)
		os.Exit(1)
	}

	go handleListen(ctx, g, vl)

	for {
		g.L.Info("connecting to host")

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

func handleListen(ctx context.Context, g *guest.Guest, l net.Listener) {
	for {
		c, err := l.Accept()
		if err != nil {
			g.L.Error("error listening for vsock connections", "error", err)
			return
		}
		g.L.Info("accepted vsock")

		go g.HandleSSH(ctx, c)
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
