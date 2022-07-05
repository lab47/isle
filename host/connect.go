package host

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/yamux"
	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/pkg/labels"
	"github.com/lab47/isle/pkg/pbstream"
	"github.com/pkg/errors"
	"golang.org/x/exp/slices"
)

type newInput struct {
	rs   *pbstream.Stream
	conn net.Conn
}

type Connection struct {
	log  hclog.Logger
	conn io.ReadWriteCloser
	sess *yamux.Session

	mu     sync.Mutex
	inputs []*Listener
}

func ConnectIO(log hclog.Logger, conn io.ReadWriteCloser) (*Connection, error) {
	var hdr pbstream.ConnectionHeader
	hdr.Multiplex = true

	err := pbstream.WriteOne(conn, &hdr)
	if err != nil {
		return nil, err
	}

	cfg := yamux.DefaultConfig()

	sess, err := yamux.Client(conn, cfg)
	if err != nil {
		return nil, err
	}

	c := &Connection{
		log:  log,
		conn: conn,
		sess: sess,
	}

	go c.acceptIncoming()

	return c, nil
}

func Connect(log hclog.Logger, proto, addr string) (*Connection, error) {
	conn, err := net.Dial(proto, addr)
	if err != nil {
		return nil, errors.Wrapf(err, "error connecting to guest: %s", addr)
	}

	return ConnectIO(log, conn)
}

func ConnectSingle(rwc io.ReadWriteCloser) error {
	var hdr pbstream.ConnectionHeader
	hdr.Multiplex = false

	err := pbstream.WriteOne(rwc, &hdr)
	if err != nil {
		return err
	}

	return nil
}

type Listener struct {
	c        *Connection
	ch       chan newInput
	selector labels.Set
	closed   bool
}

func (c *Connection) Listen(set labels.Set) (*Listener, error) {
	ch := make(chan newInput, 10)

	l := &Listener{c: c, ch: ch, selector: set}

	c.mu.Lock()
	c.inputs = append(c.inputs, l)
	c.mu.Unlock()

	err := c.register(set)
	if err != nil {
		l.Close()
		return nil, err
	}

	return l, nil
}

func (l *Listener) Accept(ctx context.Context) (*pbstream.Stream, net.Conn, error) {
	l.c.mu.Lock()
	closed := l.closed
	l.c.mu.Unlock()

	if closed {
		return nil, nil, io.EOF
	}

	for {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case in := <-l.ch:
			var ack guestapi.StreamAck
			ack.Response = &guestapi.StreamAck_Ok{Ok: true}

			err := in.rs.Send(&ack)
			if err != nil {
				return nil, nil, err
			}

			return in.rs, in.conn, nil
		}
	}
}

func (l *Listener) Close() error {
	l.c.mu.Lock()
	defer l.c.mu.Unlock()

	l.closed = true

	idx := slices.Index(l.c.inputs, l)
	if idx >= 0 {
		l.c.inputs = slices.Delete(l.c.inputs, idx, idx+1)
	}

	return nil
}

func (c *Connection) lookupSelector(sel labels.Set) *Listener {
	c.mu.Lock()
	defer c.mu.Unlock()

	var (
		found    *Listener
		foundLen int
	)

	for _, rs := range c.inputs {
		if rs.selector.Match(sel) {
			if found == nil || sel.Len() > foundLen {
				found = rs
				foundLen = sel.Len()
			}
		}
	}

	return found
}

func (c *Connection) acceptIncoming() error {
	for {
		conn, err := c.sess.AcceptStream()
		if err != nil {
			return err
		}

		rs, err := pbstream.Open(c.log, conn)
		if err != nil {
			return errors.Wrapf(err, "starting pbstream")
		}

		var hdr guestapi.StreamHeader

		err = rs.Recv(&hdr)
		if err != nil {
			return err
		}

		var xmitError string

		if sv, ok := hdr.Operation.(*guestapi.StreamHeader_Connect_); ok {
			target := c.lookupSelector(sv.Connect.Selector.Set())

			if target != nil {
				select {
				case target.ch <- newInput{rs: rs, conn: conn}:
					// ok
				default:
					xmitError = "listener out of backlog"
				}
			} else {
				xmitError = fmt.Sprintf("no listener matching: %s", sv.Connect.Selector.Set().String())
			}
		} else {
			xmitError = "unknown operation"
		}

		if xmitError != "" {
			var ack guestapi.StreamAck

			ack.Response = &guestapi.StreamAck_Error{
				Error: xmitError,
			}

			err := rs.Send(&ack)
			if err != nil {
				c.log.Error("error sending error back to connector")
			}
		}
	}
}

var ErrRemoteError = errors.New("remote error detected")

func (c *Connection) Session(sess *guestapi.SessionStart) (*pbstream.Stream, net.Conn, error) {
	conn, err := c.sess.OpenStream()
	if err != nil {
		return nil, nil, err
	}

	rs, err := StartSession(c.log, conn, sess)
	if err != nil {
		return nil, nil, err
	}

	return rs, conn, nil
}

func StartSession(log hclog.Logger, rwc io.ReadWriteCloser, sess *guestapi.SessionStart) (*pbstream.Stream, error) {
	rs, err := pbstream.Open(log, rwc)
	if err != nil {
		return nil, errors.Wrapf(err, "starting pbstream")
	}

	err = rs.Send(&guestapi.StreamHeader{
		Operation: &guestapi.StreamHeader_Session{
			Session: sess,
		},
	})
	if err != nil {
		return nil, err
	}

	var ack guestapi.StreamAck

	err = rs.Recv(&ack)
	if err != nil {
		return nil, err
	}

	switch sv := ack.Response.(type) {
	case *guestapi.StreamAck_Ok:
		// cool.
	case *guestapi.StreamAck_Error:
		return nil, errors.Wrapf(ErrRemoteError, sv.Error)
	default:
		return nil, errors.Wrapf(ErrRemoteError, "valid response not seen")
	}

	return rs, nil

}

func (c *Connection) Open(sel labels.Set) (*pbstream.Stream, net.Conn, error) {
	conn, err := c.sess.OpenStream()
	if err != nil {
		return nil, nil, err
	}

	rs, err := pbstream.Open(c.log, conn)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "starting pbstream")
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
		// cool.
	case *guestapi.StreamAck_Error:
		return nil, nil, errors.Wrapf(ErrRemoteError, sv.Error)
	default:
		return nil, nil, errors.Wrapf(ErrRemoteError, "valid response not seen")
	}

	return rs, conn, nil
}

func (c *Connection) register(selector labels.Set) error {
	conn, err := c.sess.OpenStream()
	if err != nil {
		return err
	}

	defer conn.Close()

	rs, err := pbstream.Open(c.log, conn)
	if err != nil {
		return errors.Wrapf(err, "starting pbstream")
	}

	err = rs.Send(&guestapi.StreamHeader{
		Operation: &guestapi.StreamHeader_Register_{
			Register: &guestapi.StreamHeader_Register{
				Selector: guestapi.FromSet(selector),
			},
		},
	})
	if err != nil {
		return err
	}

	var ack guestapi.StreamAck

	err = rs.Recv(&ack)
	if err != nil {
		return err
	}

	switch sv := ack.Response.(type) {
	case *guestapi.StreamAck_Ok:
		return nil
	case *guestapi.StreamAck_Error:
		return errors.Wrapf(ErrRemoteError, sv.Error)
	default:
		return errors.Wrapf(ErrRemoteError, "valid response not seen")
	}
}
