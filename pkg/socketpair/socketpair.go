package socketpair

import (
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func New() (net.Conn, net.Conn, error) {
	fds, err := unix.Socketpair(unix.AF_LOCAL, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, nil, err
	}

	fd1 := os.NewFile(uintptr(fds[0]), "fd1")
	defer fd1.Close()

	fd2 := os.NewFile(uintptr(fds[1]), "fd2")
	defer fd2.Close()

	sock1, err := net.FileConn(fd1)
	if err != nil {
		return nil, nil, err
	}

	sock2, err := net.FileConn(fd2)
	if err != nil {
		sock1.Close()
		return nil, nil, err
	}

	return sock1, sock2, nil
}
