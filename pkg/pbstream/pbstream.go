package pbstream

import (
	"bufio"
	"encoding/binary"
	"io"

	"github.com/davecgh/go-spew/spew"
	"github.com/hashicorp/go-hclog"
	"google.golang.org/protobuf/proto"
)

type Stream struct {
	log hclog.Logger

	br *bufio.Reader
	bw *bufio.Writer

	readBuf  []byte
	writeBuf []byte
	szBuf    []byte

	uo proto.UnmarshalOptions
	mo proto.MarshalOptions
}

func Open(log hclog.Logger, rw io.ReadWriter) (*Stream, error) {
	br := bufio.NewReader(rw)
	bw := bufio.NewWriter(rw)

	s := &Stream{
		log:      log,
		br:       br,
		bw:       bw,
		szBuf:    make([]byte, 9),
		writeBuf: make([]byte, 1024),
	}

	return s, nil
}

func (s *Stream) Recv(msg proto.Message) error {
	sz, err := binary.ReadUvarint(s.br)
	if err != nil {
		return err
	}

	s.log.Trace("recieve packet len", "sz", sz)

	// Handle likely short, already buffered messages directly.
	s.log.Trace("buffered data", "data", s.br.Buffered())

	if sz <= uint64(s.br.Buffered()) {
		data, err := s.br.Peek(int(sz))
		if err == nil {
			s.log.Trace("unmarshal peek'd data", "data", spew.Sdump(data))
			err = s.uo.Unmarshal(data, msg)
			s.br.Discard(int(sz))
			return err
		}
		s.log.Error("unable to peek data", "error", err)
	}

	if uint64(len(s.readBuf)) < sz {
		s.readBuf = make([]byte, sz+(sz/2))
	}

	_, err = io.ReadFull(s.br, s.readBuf[:sz])
	if err != nil {
		return err
	}

	return s.uo.Unmarshal(s.readBuf[:sz], msg)
}

func (s *Stream) Send(msg proto.Message) error {
	data, err := s.mo.MarshalAppend(s.writeBuf[:0], msg)
	if err != nil {
		return err
	}

	s.writeBuf = data[:cap(data)]

	sz := binary.PutUvarint(s.szBuf, uint64(len(data)))

	s.log.Trace("sending packet size", "sz", len(data))

	spew.Dump(s.szBuf[:sz], data)

	s.bw.Write(s.szBuf[:sz])
	s.bw.Write(data)

	return s.bw.Flush()
}
