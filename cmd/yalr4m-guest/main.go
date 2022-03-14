package main

import (
	"context"
	"net"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/yalr4m/guest"
	"github.com/hashicorp/yamux"
	"github.com/mdlayher/vsock"
)

func main() {
	g := &guest.Guest{
		L: hclog.New(&hclog.LoggerOptions{
			Name:  "guest",
			Level: hclog.Trace,
		}),
	}

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
		handleConn(g, c)
	}
}

func handleConn(g *guest.Guest, c net.Conn) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := yamux.DefaultConfig()

	sess, err := yamux.Server(c, cfg)
	if err != nil {
		g.L.Info("error negotiating yamux server", "error", err)
		return
	}

	err = g.Run(ctx, sess)

	g.L.Info("run has stopped", "error", err)
}
