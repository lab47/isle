package pbstream

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
)

const IsAtLeastVersion0_1_0 = true

type Endpoint[Req, Resp any] struct {
	opener   StreamOpener
	selector string
}

func NewEndpoint[Req, Resp any](client Client, selector string) *Endpoint[Req, Resp] {
	checkProtoMessage[Req]()
	checkProtoMessage[Resp]()

	return &Endpoint[Req, Resp]{
		opener:   client,
		selector: selector,
	}
}

func (c *Endpoint[Req, Resp]) CallUnary(ctx context.Context, req *Request[Req]) (*Response[Resp], error) {
	rs, err := c.opener.Open()
	if err != nil {
		return nil, err
	}

	defer rs.Close()

	var hdr RPCHeader
	hdr.Selector = c.selector

	err = rs.Send(&hdr)
	if err != nil {
		return nil, err
	}

	var raw any = req.Value

	msg := raw.(proto.Message)

	err = rs.Send(msg)
	if err != nil {
		return nil, err
	}

	var ftr RPCFooter

	err = rs.Recv(&ftr)
	if err != nil {
		return nil, err
	}

	switch sv := ftr.Response.(type) {
	case *RPCFooter_Error_:
		return nil, errors.Wrapf(ErrRemoteError, sv.Error.Msg)
	case *RPCFooter_Success_:
		// ok!
	default:
		return nil, errors.Wrapf(ErrRemoteError, "unexpected footer response type")
	}

	var resp Resp
	raw = &resp

	msg = raw.(proto.Message)

	err = rs.Recv(msg)
	if err != nil {
		return nil, err
	}

	return &Response[Resp]{Value: &resp}, nil
}

type ClientOption func()

type StreamOpener interface {
	Open() (*Stream, error)
}

type StreamOpenFunc func() (*Stream, error)

func (s StreamOpenFunc) Open() (*Stream, error) {
	return s()
}

type Client interface {
	StreamOpener
}

type ServerStreamForClient[T any] struct {
	rs  *Stream
	err error
}

func (c *Endpoint[Req, Resp]) CallServerStream(ctx context.Context, req *Request[Req]) (*ServerStreamForClient[Resp], error) {
	rs, err := c.opener.Open()
	if err != nil {
		return nil, err
	}

	var hdr RPCHeader
	hdr.Selector = c.selector

	err = rs.Send(&hdr)
	if err != nil {
		return nil, err
	}

	var raw any = req.Value

	msg := raw.(proto.Message)

	err = rs.Send(msg)
	if err != nil {
		return nil, err
	}

	return &ServerStreamForClient[Resp]{
		rs: rs,
	}, nil
}

func (c *ServerStreamForClient[Resp]) Close() error {
	var hdr RPCStreamHeader
	hdr.Operation = &RPCStreamHeader_Footer{
		Footer: &RPCFooter{
			Response: &RPCFooter_Success_{
				Success: &RPCFooter_Success{},
			},
		},
	}

	return c.rs.Send(&hdr)
}

func (c *ServerStreamForClient[Resp]) Receive() (*Resp, error) {
	var hdr RPCStreamHeader

	err := c.rs.Recv(&hdr)
	if err != nil {
		c.err = err
		return nil, err
	}

	switch sv := hdr.Operation.(type) {
	case *RPCStreamHeader_Continue_:
		// ok
	case *RPCStreamHeader_Footer:
		switch fv := sv.Footer.Response.(type) {
		case *RPCFooter_Success_:
			c.err = ErrClosed

			return nil, ErrClosed
		case *RPCFooter_Error_:
			c.err = errors.Wrapf(ErrRemoteError, fv.Error.Msg)
		default:
			c.err = errors.Wrapf(ErrRemoteError, "unknown footer operation: %T", fv)
		}
	default:
		c.err = errors.Wrapf(ErrRemoteError, "unknown stream operation: %T", sv)
	}

	var value Resp

	var raw any = &value
	msg := raw.(proto.Message)

	err = c.rs.Recv(msg)
	if err != nil {
		c.err = err
		return nil, err
	}

	return &value, nil
}

type BidiStreamForClient[Req, Resp any] struct {
	rs  *Stream
	err error
}

func (c *Endpoint[Req, Resp]) CallBidiStream(ctx context.Context) *BidiStreamForClient[Req, Resp] {
	rs, err := c.opener.Open()
	if err != nil {
		return &BidiStreamForClient[Req, Resp]{
			err: err,
		}
	}

	var hdr RPCHeader
	hdr.Selector = c.selector

	err = rs.Send(&hdr)
	if err != nil {
		return &BidiStreamForClient[Req, Resp]{
			err: err,
		}
	}

	return &BidiStreamForClient[Req, Resp]{
		rs: rs,
	}
}

func (c *BidiStreamForClient[Req, Resp]) Close() error {
	var hdr RPCStreamHeader
	hdr.Operation = &RPCStreamHeader_Footer{
		Footer: &RPCFooter{
			Response: &RPCFooter_Success_{
				Success: &RPCFooter_Success{},
			},
		},
	}

	return c.rs.Send(&hdr)
}

func (c *BidiStreamForClient[Req, Resp]) Send(msg *Req) error {
	var hdr RPCStreamHeader
	hdr.Operation = &RPCStreamHeader_Continue_{
		Continue: &RPCStreamHeader_Continue{},
	}

	err := c.rs.Send(&hdr)
	if err != nil {
		return err
	}

	var raw any = msg

	tmsg := raw.(proto.Message)

	return c.rs.Send(tmsg)
}

func (c *BidiStreamForClient[Req, Resp]) Receive() (*Resp, error) {
	var hdr RPCStreamHeader

	err := c.rs.Recv(&hdr)
	if err != nil {
		c.err = err
		return nil, err
	}

	switch sv := hdr.Operation.(type) {
	case *RPCStreamHeader_Continue_:
		// ok
	case *RPCStreamHeader_Footer:
		switch fv := sv.Footer.Response.(type) {
		case *RPCFooter_Success_:
			c.err = ErrClosed

			return nil, ErrClosed
		case *RPCFooter_Error_:
			c.err = errors.Wrapf(ErrRemoteError, fv.Error.Msg)
		default:
			c.err = errors.Wrapf(ErrRemoteError, "unknown footer operation: %T", fv)
		}
	default:
		c.err = errors.Wrapf(ErrRemoteError, "unknown stream operation: %T", sv)
	}

	var value Resp

	var raw any = &value
	msg := raw.(proto.Message)

	err = c.rs.Recv(msg)
	if err != nil {
		c.err = err
		return nil, err
	}

	return &value, nil
}

type ReceiveInfo[Resp any] struct {
	Value       *Resp
	ReceiveTime time.Time
}

func (c *BidiStreamForClient[Req, Resp]) ReceiveMessage() (ReceiveInfo[Resp], error) {
	var hdr RPCStreamHeader

	t, err := c.rs.RecvTimed(&hdr)
	if err != nil {
		c.err = err
		return ReceiveInfo[Resp]{}, err
	}

	switch sv := hdr.Operation.(type) {
	case *RPCStreamHeader_Continue_:
		// ok
	case *RPCStreamHeader_Footer:
		switch fv := sv.Footer.Response.(type) {
		case *RPCFooter_Success_:
			c.err = ErrClosed

			return ReceiveInfo[Resp]{}, ErrClosed
		case *RPCFooter_Error_:
			c.err = errors.Wrapf(ErrRemoteError, fv.Error.Msg)
		default:
			c.err = errors.Wrapf(ErrRemoteError, "unknown footer operation: %T", fv)
		}
	default:
		c.err = errors.Wrapf(ErrRemoteError, "unknown stream operation: %T", sv)
	}

	var value Resp

	var raw any = &value
	msg := raw.(proto.Message)

	err = c.rs.Recv(msg)
	if err != nil {
		c.err = err
		return ReceiveInfo[Resp]{}, err
	}

	return ReceiveInfo[Resp]{
		Value:       &value,
		ReceiveTime: t,
	}, nil
}

type Request[Req any] struct {
	Value *Req
}

func NewRequest[Req any](val *Req) *Request[Req] {
	return &Request[Req]{
		Value: val,
	}
}

type Response[Resp any] struct {
	Value *Resp
}

func NewResponse[T any](val *T) *Response[T] {
	return &Response[T]{
		Value: val,
	}
}

var (
	ErrClosed      = errors.New("remote side closed stream")
	ErrRemoteError = errors.New("remote side produced error")
)

type ServerStream[Resp any] struct {
	err error
	rs  *Stream
}

func (b *ServerStream[Resp]) Send(msg *Resp) error {
	var hdr RPCStreamHeader
	hdr.Operation = &RPCStreamHeader_Continue_{
		Continue: &RPCStreamHeader_Continue{},
	}

	err := b.rs.Send(&hdr)
	if err != nil {
		return err
	}

	var raw any = msg

	tmsg := raw.(proto.Message)

	return b.rs.Send(tmsg)
}

func (b *ServerStream[Resp]) Close() error {
	var hdr RPCStreamHeader
	hdr.Operation = &RPCStreamHeader_Footer{
		Footer: &RPCFooter{
			Response: &RPCFooter_Success_{
				Success: &RPCFooter_Success{},
			},
		},
	}

	return b.rs.Send(&hdr)
}

type BidiStream[Req, Resp any] struct {
	err error
	rs  *Stream
}

func (b *BidiStream[Req, Resp]) Receive() (*Req, error) {
	i, err := b.ReceiveInfo()
	if err != nil {
		return nil, err
	}

	return i.Value, nil
}

func (b *BidiStream[Req, Resp]) ReceiveInfo() (ReceiveInfo[Req], error) {
	if b.err != nil {
		return ReceiveInfo[Req]{}, b.err
	}

	var hdr RPCStreamHeader

	t, err := b.rs.RecvTimed(&hdr)
	if err != nil {
		b.err = err
		return ReceiveInfo[Req]{}, err
	}

	switch sv := hdr.Operation.(type) {
	case *RPCStreamHeader_Continue_:
		// ok
	case *RPCStreamHeader_Footer:
		switch fv := sv.Footer.Response.(type) {
		case *RPCFooter_Success_:
			b.err = ErrClosed

			return ReceiveInfo[Req]{}, ErrClosed
		case *RPCFooter_Error_:
			b.err = errors.Wrapf(ErrRemoteError, fv.Error.Msg)
		default:
			b.err = errors.Wrapf(ErrRemoteError, "unknown footer operation: %T", fv)
		}
	default:
		b.err = errors.Wrapf(ErrRemoteError, "unknown stream operation: %T", sv)
	}

	var value Req

	var raw any = &value
	msg := raw.(proto.Message)

	err = b.rs.Recv(msg)
	if err != nil {
		b.err = err
		return ReceiveInfo[Req]{}, err
	}

	return ReceiveInfo[Req]{
		Value:       &value,
		ReceiveTime: t,
	}, nil
}

func (b *BidiStream[Req, Resp]) Send(msg *Resp) error {
	var hdr RPCStreamHeader
	hdr.Operation = &RPCStreamHeader_Continue_{
		Continue: &RPCStreamHeader_Continue{},
	}

	err := b.rs.Send(&hdr)
	if err != nil {
		return err
	}

	var raw any = msg

	tmsg := raw.(proto.Message)

	return b.rs.Send(tmsg)
}

func (b *BidiStream[Req, Resp]) Close() error {
	var hdr RPCStreamHeader
	hdr.Operation = &RPCStreamHeader_Footer{
		Footer: &RPCFooter{
			Response: &RPCFooter_Success_{
				Success: &RPCFooter_Success{},
			},
		},
	}

	return b.rs.Send(&hdr)
}

type HandlerOption func()

func NewServerStreamHandler[Req, Resp any](
	selector string,
	fn func(context.Context, *Request[Req], *ServerStream[Resp]) error,
) DispatchHandler {
	checkProtoMessage[Req]()
	checkProtoMessage[Resp]()

	return &serverStreamHandler[Req, Resp]{
		fn: fn,
	}
}

type serverStreamHandler[Req, Resp any] struct {
	fn func(context.Context, *Request[Req], *ServerStream[Resp]) error
}

func (ua *serverStreamHandler[Req, Resp]) HandleRPC(ctx context.Context, rs *Stream) error {
	var req Request[Req]

	req.Value = new(Req)

	var raw any = req.Value

	msg := raw.(proto.Message)

	err := rs.Recv(msg)
	if err != nil {
		return err
	}

	ss := &ServerStream[Resp]{rs: rs}

	return ua.fn(ctx, &req, ss)
}

func NewBidiStreamHandler[Req, Resp any](
	selector string,
	fn func(context.Context, *BidiStream[Req, Resp]) error,
) DispatchHandler {
	checkProtoMessage[Req]()
	checkProtoMessage[Resp]()

	return &bidiStreamHandler[Req, Resp]{
		fn: fn,
	}
}

type bidiStreamHandler[Req, Resp any] struct {
	fn func(context.Context, *BidiStream[Req, Resp]) error
}

func (ua *bidiStreamHandler[Req, Resp]) HandleRPC(ctx context.Context, rs *Stream) error {
	bs := &BidiStream[Req, Resp]{rs: rs}
	return ua.fn(ctx, bs)
}

var ErrInvalidType = errors.New("invalid type of request or response, not proto.Message")

type unaryHandler[Req, Resp any] struct {
	fn func(context.Context, *Request[Req]) (*Response[Resp], error)
}

func (ua *unaryHandler[Req, Resp]) HandleRPC(ctx context.Context, rs *Stream) error {
	var req Request[Req]

	req.Value = new(Req)

	var raw any = req.Value

	msg := raw.(proto.Message)

	err := rs.Recv(msg)
	if err != nil {
		return err
	}

	resp, err := ua.fn(ctx, &req)
	if err != nil {
		return err
	}

	var ftr RPCFooter
	ftr.Response = &RPCFooter_Success_{
		Success: &RPCFooter_Success{},
	}

	err = rs.Send(&ftr)
	if err != nil {
		return err
	}

	raw = resp.Value

	msg = raw.(proto.Message)

	return rs.Send(msg)
}

func checkProtoMessage[T any]() {
	var r any = new(T)

	_, ok := r.(proto.Message)
	if !ok {
		panic(fmt.Sprintf("%T must be a proto.Message", r))
	}
}

func NewUnaryHandler[Req, Resp any](
	selector string,
	fn func(context.Context, *Request[Req]) (*Response[Resp], error),
) DispatchHandler {
	checkProtoMessage[Req]()
	checkProtoMessage[Resp]()

	ua := &unaryHandler[Req, Resp]{
		fn: fn,
	}
	return ua
}

type Handler interface {
	HandleRPC(ctx context.Context, rs *Stream) error
}

type Mux struct {
	handlers map[string]DispatchHandler
}

func (m *Mux) HandleRPC(ctx context.Context, rs *Stream) error {
	var hdr RPCHeader

	err := rs.Recv(&hdr)
	if err != nil {
		return err
	}

	if handler, ok := m.handlers[hdr.Selector]; ok {
		err = handler.HandleRPC(ctx, rs)
		if err == nil {
			return nil
		}
	} else {
		err = fmt.Errorf("unknown handler for selector: %s", hdr.Selector)
	}

	var ftr RPCFooter

	ftr.Response = &RPCFooter_Error_{
		Error: &RPCFooter_Error{
			Msg: err.Error(),
		},
	}

	rs.Send(&ftr)

	return err
}

func NewMux() *Mux {
	return &Mux{
		handlers: make(map[string]DispatchHandler),
	}
}

func CombineHandlers(handlers ...Handler) Handler {
	mux := NewMux()

	for _, h := range handlers {
		m := h.(*Mux)

		for key, dh := range m.handlers {
			mux.handlers[key] = dh
		}
	}

	return mux
}

type DispatchHandler interface {
	HandleRPC(ctx context.Context, rs *Stream) error
}

func (m *Mux) Handle(selector string, dh DispatchHandler) {
	m.handlers[selector] = dh
}

type Code int

const (
	CodeUnimplemented Code = 0
)

func NewError(code Code, err error) error {
	return nil
}
