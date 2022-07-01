package pbstream

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/hashicorp/go-hclog"
	"google.golang.org/protobuf/proto"
)

type Stream struct {
	log hclog.Logger

	orig io.ReadWriter
	br   *bufio.Reader
	bw   *bufio.Writer

	readBuf  []byte
	writeBuf []byte
	szBuf    []byte

	uo proto.UnmarshalOptions
	mo proto.MarshalOptions

	closer io.Closer
}

func Open(log hclog.Logger, rw io.ReadWriter) (*Stream, error) {
	br := bufio.NewReader(rw)
	bw := bufio.NewWriter(rw)

	s := &Stream{
		log:      log,
		orig:     rw,
		br:       br,
		bw:       bw,
		szBuf:    make([]byte, 9),
		writeBuf: make([]byte, 1024),
	}

	if c, ok := rw.(io.Closer); ok {
		s.closer = c
	}

	return s, nil
}

func (s *Stream) Close() error {
	if s.closer != nil {
		return s.closer.Close()
	}

	return nil
}

func (s *Stream) Recv(msg proto.Message) error {
	_, err := s.RecvTimed(msg)
	return err
}

func (s *Stream) RecvTimed(msg proto.Message) (time.Time, error) {
	sz, err := binary.ReadUvarint(s.br)
	if err != nil {
		return time.Time{}, err
	}

	// Handle likely short, already buffered messages directly.
	if sz <= uint64(s.br.Buffered()) {
		data, err := s.br.Peek(int(sz))
		if err == nil {
			t := time.Now()
			err = s.uo.Unmarshal(data, msg)
			s.br.Discard(int(sz))
			return t, err
		}
		s.log.Error("unable to peek data", "error", err)
	}

	if uint64(len(s.readBuf)) < sz {
		s.readBuf = make([]byte, sz+(sz/2))
	}

	_, err = io.ReadFull(s.br, s.readBuf[:sz])
	if err != nil {
		return time.Time{}, err
	}

	t := time.Now()

	return t, s.uo.Unmarshal(s.readBuf[:sz], msg)
}

func (s *Stream) Send(msg proto.Message) error {
	data, err := s.mo.MarshalAppend(s.writeBuf[:0], msg)
	if err != nil {
		return err
	}

	s.writeBuf = data[:cap(data)]

	sz := binary.PutUvarint(s.szBuf, uint64(len(data)))

	s.bw.Write(s.szBuf[:sz])
	s.bw.Write(data)

	return s.bw.Flush()
}

type headerReader struct {
	header   io.Reader
	whenDone *io.Reader
	reader   io.Reader
}

func (h *headerReader) Read(buf []byte) (int, error) {
	n, err := h.header.Read(buf)
	if err == nil {
		return n, nil
	}

	if n == 0 {
		// Prevent headerReader from being used further, since
		// there is no more header
		*h.whenDone = h.reader
		return h.reader.Read(buf)
	} else {
		return n, nil
	}
}

type pbConn struct {
	io.Reader
	net.Conn
}

func (r *pbConn) Read(buf []byte) (int, error) {
	return r.Reader.Read(buf)
}

func (s *Stream) Hijack(base net.Conn) (net.Conn, error) {
	out := &pbConn{
		Conn: base,
	}

	if s.br.Buffered() == 0 {
		out.Reader = base
	} else {
		fmt.Printf("hijack buffer: %d\n", s.br.Buffered())
		out.Reader = &headerReader{
			header:   s.br,
			whenDone: &out.Reader,
			reader:   base,
		}
	}

	return out, nil
}
