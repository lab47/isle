package guest

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/yamux"
	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/pkg/labels"
	"github.com/lab47/isle/pkg/pbstream"
	"github.com/pkg/errors"
	"github.com/samber/do"
	"golang.org/x/exp/slices"
)

type registeredSession struct {
	session   *yamux.Session
	selectors []labels.Set
}

type SessionHandler interface {
	StartSession(ctx *ResourceContext, rs *pbstream.Stream, ss *guestapi.SessionStart)
}

type newConnection struct {
	rs   *pbstream.Stream
	conn net.Conn
}

type localListener struct {
	selector labels.Set
	ch       chan newConnection
}

type ConnectionManager struct {
	L               hclog.Logger
	SessionHandler  SessionHandler
	ResourceContext *ResourceContext

	mu sync.Mutex

	registered []*registeredSession

	listeners []*Listener
}

func NewConnectionManager(inj *do.Injector) (*ConnectionManager, error) {
	ctx, err := do.Invoke[*ResourceContext](inj)
	if err != nil {
		return nil, err
	}

	log, err := do.Invoke[hclog.Logger](inj)
	if err != nil {
		return nil, err
	}

	cm := &ConnectionManager{
		L:               log,
		ResourceContext: ctx,
	}

	return cm, nil
}

func (c *ConnectionManager) SetSessionHandler(hdlr SessionHandler) {
	c.SessionHandler = hdlr
}

func (c *ConnectionManager) Serve(ctx context.Context, l net.Listener) error {
	cfg := yamux.DefaultConfig()

	go func() {
		<-ctx.Done()
		l.Close()
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			return nil
		}

		c.L.Trace("accepting new connection to connection manager")

		go func() {
			var hdr pbstream.ConnectionHeader

			c.L.Trace("reading session header")

			err = pbstream.ReadOne(conn, &hdr)
			if err != nil {
				c.L.Error("error reading connection header", "error", err)
				return
			}

			c.L.Trace("session header", "multiplex", hdr.Multiplex)

			if hdr.Multiplex {
				session, err := yamux.Server(conn, cfg)
				if err != nil {
					c.L.Error("error starting yamux server", "error", err)
					return
				}

				c.handleSession(session)
			} else {
				c.handleOne(ctx, conn, nil)
			}
		}()
	}
}

func (c *ConnectionManager) handleOne(ctx context.Context, stream net.Conn, sess *yamux.Session) error {
	rs, err := pbstream.Open(c.L, stream)
	if err != nil {
		c.L.Error("error connecting protobuf stream", "error", err)
		return err
	}

	var hdr guestapi.StreamHeader

	err = rs.Recv(&hdr)
	if err != nil {
		c.L.Error("error parsing stream header", "error", err)
		return err
	}

	var ack guestapi.StreamAck

	switch sv := hdr.Operation.(type) {
	case *guestapi.StreamHeader_Register_:
		if sess == nil {
			ack.Response = &guestapi.StreamAck_Error{
				Error: "no yamux session established",
			}
		} else {
			c.registerSession(sess, sv.Register)
			ack.Response = &guestapi.StreamAck_Ok{Ok: true}
		}

		err = rs.Send(&ack)
		if err != nil {
			c.L.Error("error sending ack", "error", err)
		}
	case *guestapi.StreamHeader_Connect_:
		err := c.handOffConnect(ctx, sv.Connect.Selector, rs, stream)
		if err != nil {
			ack.Response = &guestapi.StreamAck_Error{
				Error: err.Error(),
			}

			err = rs.Send(&ack)
			if err != nil {
				c.L.Error("error sending ack", "error", err)
			}
		}

	case *guestapi.StreamHeader_Session:
		hdlr := c.SessionHandler

		if hdlr == nil {
			ack.Response = &guestapi.StreamAck_Error{
				Error: "no session handler registered",
			}

			err = rs.Send(&ack)
			if err != nil {
				c.L.Error("error sending ack", "error", err)
			}
		} else {
			ack.Response = &guestapi.StreamAck_Ok{Ok: true}

			err = rs.Send(&ack)
			if err != nil {
				c.L.Error("error sending ack", "error", err)
			}

			hdlr.StartSession(c.ResourceContext.Under(ctx), rs, sv.Session)
		}
	default:
		c.L.Error("unknown operation in header", "operation", hclog.Fmt("%T", sv))
	}

	return nil
}

func (c *ConnectionManager) handleSession(sess *yamux.Session) {
	defer c.clearSession(sess)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for {
		stream, err := sess.AcceptStream()
		if err != nil {
			c.L.Error("error accepting new stream", "error", err)
			return
		}

		go c.handleOne(ctx, stream, sess)
	}
}

func (c *ConnectionManager) handOffConnect(ctx context.Context, pbsel *guestapi.Labels, rs *pbstream.Stream, conn net.Conn) error {
	sel := pbsel.Set()

	c.mu.Lock()
	defer c.mu.Unlock()

	var found *Listener

	for _, list := range c.listeners {
		if list.sel.Match(sel) {
			if found == nil || found.sel.Len() < list.sel.Len() {
				found = list
			}
		}
	}

	if found == nil {
		c.L.Error("unable to find listener for connect", "selector", sel.String())
		return errors.Wrapf(ErrRouting, "no connections for selector: %s", sel.String())
	}

	c.L.Trace("sending stream ack")

	var ack guestapi.StreamAck
	ack.Response = &guestapi.StreamAck_Ok{
		Ok: true,
	}

	err := rs.Send(&ack)
	if err != nil {
		return err
	}

	c.L.Debug("found listener for connect", "selector", sel.String())

	go func() {
		select {
		case <-ctx.Done():
			return
		case found.ch <- newConnection{rs: rs, conn: conn}:
			//ok
		}
	}()

	return nil
}

type Listener struct {
	closed *int32
	cm     *ConnectionManager
	sel    labels.Set
	ch     chan newConnection
}

func (c *ConnectionManager) Listen(sel labels.Set) *Listener {
	c.mu.Lock()
	defer c.mu.Unlock()

	listener := &Listener{
		closed: new(int32),
		cm:     c,
		sel:    sel,
		ch:     make(chan newConnection, 10),
	}

	c.listeners = append(c.listeners, listener)

	return listener
}

func (l *Listener) Close() error {
	l.cm.mu.Lock()
	defer l.cm.mu.Unlock()

	atomic.StoreInt32(l.closed, 1)

	idx := slices.Index(l.cm.listeners, l)
	if idx >= 0 {
		l.cm.listeners = slices.Delete(l.cm.listeners, idx, idx+1)
	}

	close(l.ch)

	go func() {
		for nc := range l.ch {
			nc.conn.Close()
		}
	}()

	return nil
}

func (l *Listener) Accept(ctx context.Context) (*pbstream.Stream, net.Conn, error) {
	if atomic.LoadInt32(l.closed) == 1 {
		return nil, nil, io.EOF
	}

	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case nc := <-l.ch:
		return nc.rs, nc.conn, nil
	}
}

var ErrRouting = errors.New("no route to selector")

func (c *ConnectionManager) FindSession(capa labels.Set) (*yamux.Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var (
		found    *registeredSession
		foundLen int
	)

	for _, rs := range c.registered {
		for _, sel := range rs.selectors {
			if sel.Match(capa) {
				if found == nil || sel.Len() > foundLen {
					found = rs
					foundLen = sel.Len()
				}
			}
		}
	}

	if found == nil {
		return nil, ErrRouting
	}

	return found.session, nil
}

func (c *ConnectionManager) ConnectToHost() (net.Conn, error) {
	sess, err := c.FindSession(labels.New("service", "host"))
	if err != nil {
		return nil, err
	}

	return sess.Open()
}

func (c *ConnectionManager) GRPCDial(ctx context.Context, addr string) (net.Conn, error) {
	return c.Dial(labels.New("grpc", addr))
}

var ErrRemoteError = errors.New("an error occured remotely")

func (c *ConnectionManager) Open(sel labels.Set) (*pbstream.Stream, net.Conn, error) {
	sess, err := c.FindSession(sel)
	if err != nil {
		return nil, nil, err
	}

	conn, err := sess.Open()
	if err != nil {
		return nil, nil, err
	}

	rs, err := pbstream.Open(c.L, conn)
	if err != nil {
		return nil, nil, err
	}

	err = rs.Send(&guestapi.StreamHeader{
		Operation: &guestapi.StreamHeader_Connect_{
			Connect: &guestapi.StreamHeader_Connect{
				Selector: guestapi.FromSet(sel),
			},
		},
	})
	if err != nil {
		return nil, nil, err
	}

	var ack guestapi.StreamAck
	err = rs.Recv(&ack)
	if err != nil {
		return nil, nil, err
	}

	switch sv := ack.Response.(type) {
	case *guestapi.StreamAck_Ok:
		return rs, conn, nil
	case *guestapi.StreamAck_Error:
		return nil, nil, errors.Wrapf(ErrRemoteError, sv.Error)
	default:
		return nil, nil, errors.Wrapf(ErrRemoteError, "remote side returned unknown type")
	}
}

func (c *ConnectionManager) Dial(capa labels.Set) (net.Conn, error) {
	rs, conn, err := c.Open(capa)
	if err != nil {
		return nil, err
	}

	return rs.Hijack(conn)
}

func (c *ConnectionManager) registerSession(sess *yamux.Session, reg *guestapi.StreamHeader_Register) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, rs := range c.registered {
		if rs.session == sess {
			rs.selectors = append(rs.selectors, reg.Selector.Set())
			return
		}
	}

	c.registered = append(c.registered, &registeredSession{
		session:   sess,
		selectors: []labels.Set{reg.Selector.Set()},
	})
}

func (c *ConnectionManager) clearSession(sess *yamux.Session) {
	c.mu.Lock()
	defer c.mu.Unlock()

	idx := slices.IndexFunc(c.registered, func(e *registeredSession) bool {
		return e.session == sess
	})

	if idx >= 0 {
		c.registered = slices.Delete(c.registered, idx, idx+1)
	}
}
