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

// ReadOne pulls data out of a reader to read one proto message. Unless
// the Reader is also a ByteReader, this is not effecient, but it is correct.
func ReadOne(r io.Reader, msg proto.Message) error {
	var (
		sz  uint64
		err error
	)

	if br, ok := r.(io.ByteReader); ok {
		sz, err = binary.ReadUvarint(br)
	} else {
		szBuf := make([]byte, 1)

		var x uint64
		var s uint
		for i := 0; i < binary.MaxVarintLen64; i++ {
			_, err = r.Read(szBuf)
			if err != nil {
				return err
			}
			b := szBuf[0]
			if b < 0x80 {
				if i == binary.MaxVarintLen64-1 && b > 1 {
					return io.EOF
				}
				sz = x | uint64(b)<<s
				break
			} else {
				x |= uint64(b&0x7f) << s
				s += 7
			}
		}
	}

	if err != nil {
		return err
	}

	buf := make([]byte, sz)
	_, err = io.ReadFull(r, buf)
	if err != nil {
		return err
	}

	return proto.Unmarshal(buf, msg)
}

// WriteOne sends one message to the writer only.
func WriteOne(w io.Writer, msg proto.Message) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return err
	}

	writeBuf := make([]byte, 9)

	sz := binary.PutUvarint(writeBuf, uint64(len(data)))

	w.Write(writeBuf[:sz])
	_, err = w.Write(data)
	return err
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

type fastUnmarshal interface {
	FastUnmarshalProto(b []byte) error
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

			if fm, ok := msg.(fastUnmarshal); ok {
				err = fm.FastUnmarshalProto(data)
				if err == nil {
					s.br.Discard(int(sz))
					return t, nil
				}
			}

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

	if fm, ok := msg.(fastUnmarshal); ok {
		err = fm.FastUnmarshalProto(s.readBuf[:sz])
		if err == nil {
			return t, nil
		}
	}

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

// SendPremarshaled transmits the data as it normally would a marshaled
// protobuf. This is available to allow users to provide their own
// protobuf marshaling. This is useful for specialized cases only.
func (s *Stream) SendPremarshaled(data []byte) error {
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
