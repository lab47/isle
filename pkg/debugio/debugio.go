package debugio

import (
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"runtime/debug"
	"strings"
)

type showRead struct{}

func ShowRead(r io.Reader) io.Reader {
	return io.TeeReader(r, &showRead{})
}

func (s *showRead) Write(b []byte) (int, error) {
	fmt.Printf("data read:\n")
	hex.Dump(b)

	return len(b), nil
}

type showWrite struct{}

func ShowWrite(w io.Writer) io.Writer {
	return io.MultiWriter(w, &showWrite{})
}

func (s *showWrite) Write(b []byte) (int, error) {
	fmt.Printf("data write:\n")
	hex.Dump(b)

	return len(b), nil
}

type showNet struct {
	net.Conn
}

func ShowNet(c net.Conn) net.Conn {
	return &showNet{c}
}

func (s *showNet) Read(b []byte) (int, error) {
	n, err := s.Conn.Read(b)

	fmt.Printf("data read (%d):\r\n%s\r\n", n,
		strings.Replace(hex.Dump(b[:n]), "\n", "\r\n", -1))

	fmt.Printf("error: %s\n", err)
	if err != nil {
		debug.PrintStack()
	}

	return n, err
}

func (s *showNet) Write(b []byte) (int, error) {
	n, err := s.Conn.Write(b)

	fmt.Printf("data write (%d):\r\n%s\r\n", n,
		strings.Replace(hex.Dump(b), "\n", "\r\n", -1))

	fmt.Printf("error: %s\n", err)
	if err != nil {
		debug.PrintStack()
	}

	return n, err
}

func (s *showNet) Close() error {
	fmt.Printf("closing net!\n")
	debug.PrintStack()
	return s.Conn.Close()
}
