package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/pkg/labels"
	"github.com/lab47/isle/pkg/pbstream"
)

type forwardedPort struct {
	port   int
	key    string
	list   net.Listener
	target labels.Set
}

type connApi struct {
	guestapi.PBSUnimplementedHostAPIHandler

	log  hclog.Logger
	conn *Connection

	mu    sync.Mutex
	ports map[string]*forwardedPort
}

func (c *connApi) RunOnMac(ctx context.Context, stream *pbstream.BidiStream[guestapi.RunInput, guestapi.RunOutput]) error {
	return nil
}

func (c *connApi) Running(context.Context, *pbstream.Request[guestapi.RunningReq]) (*pbstream.Response[guestapi.RunningResp], error) {
	return nil, nil
}

func (c *connApi) StartPortForward(ctx context.Context, req *pbstream.Request[guestapi.StartPortForwardReq]) (*pbstream.Response[guestapi.StartPortForwardResp], error) {
	c.log.Debug("got request to start port forwarding")

	l, err := net.Listen("tcp", fmt.Sprintf(":%d", req.Value.Port))
	if err != nil {
		c.log.Error("error listening on port for port forwarding", "error", err)
		return nil, err
	}

	c.log.Info("listening on port for port forwarding", "port", req.Value.Port)

	fp := &forwardedPort{
		port:   int(req.Value.Port),
		key:    req.Value.Key,
		list:   l,
		target: req.Value.Target.Set(),
	}

	go c.startForwarding(fp)

	c.mu.Lock()
	if c.ports == nil {
		c.ports = make(map[string]*forwardedPort)
	}

	c.ports[req.Value.Key] = fp
	defer c.mu.Unlock()

	return pbstream.NewResponse(&guestapi.StartPortForwardResp{}), nil
}

func (ca *connApi) startForwarding(fp *forwardedPort) {
	defer func() {
		ca.mu.Lock()
		defer ca.mu.Unlock()
		delete(ca.ports, fp.key)
	}()

	for {
		c, err := fp.list.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				ca.log.Error("error accepting new connection", "error", err)
			}
			return
		}

		go ca.forwardConnection(c, fp)
	}
}

func (ca *connApi) forwardConnection(local net.Conn, fp *forwardedPort) {
	defer local.Close()

	ca.log.Debug("beginning port forwarding", "target", fp.target)

	rs, remote, err := ca.conn.hc.Open(fp.target)
	if err != nil {
		ca.log.Error("error opening connection to port endpoint", "error", err, "target", fp.target.String())
		return
	}

	remote, err = rs.Hijack(remote)
	if err != nil {
		ca.log.Error("error hijacking conn", "error", err, "target")
		return
	}

	ca.log.Debug("port forwarding connected")

	defer remote.Close()

	go func() {
		defer local.Close()
		defer remote.Close()

		io.Copy(local, remote)
	}()

	io.Copy(remote, local)

	ca.log.Debug("port forwarding finished")
}

func (c *connApi) CancelPortForward(ctx context.Context, req *pbstream.Request[guestapi.CancelPortForwardReq]) (*pbstream.Response[guestapi.CancelPortForwardResp], error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	fp, ok := c.ports[req.Value.Key]
	if ok {
		fp.list.Close()
	}

	return pbstream.NewResponse(&guestapi.CancelPortForwardResp{}), nil
}
