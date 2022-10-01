package signal

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	SIGABRT = 0x6
	SIGALRM = 0xe
	SIGFPE  = 0x8
	SIGHUP  = 0x1
	SIGILL  = 0x4
	SIGINT  = 0x2
	SIGIOT  = 0x6
	SIGKILL = 0x9
	SIGPIPE = 0xd
	SIGQUIT = 0x3
	SIGSEGV = 0xb
	SIGTERM = 0xf
	SIGUSR1 = 0x10
	SIGUSR2 = 0x11
)

var (
	TransportToOS = map[int32]syscall.Signal{
		SIGABRT: unix.SIGABRT,
		SIGALRM: unix.SIGALRM,
		SIGFPE:  unix.SIGFPE,
		SIGHUP:  unix.SIGHUP,
		SIGILL:  unix.SIGILL,
		SIGINT:  unix.SIGINT,
		SIGKILL: unix.SIGKILL,
		SIGPIPE: unix.SIGPIPE,
		SIGQUIT: unix.SIGQUIT,
		SIGSEGV: unix.SIGSEGV,
		SIGTERM: unix.SIGTERM,
		SIGUSR1: unix.SIGUSR1,
		SIGUSR2: unix.SIGUSR2,
	}

	OSToTransport = map[syscall.Signal]int32{
		unix.SIGABRT: SIGABRT,
		unix.SIGALRM: SIGALRM,
		unix.SIGFPE:  SIGFPE,
		unix.SIGHUP:  SIGHUP,
		unix.SIGILL:  SIGILL,
		unix.SIGINT:  SIGINT,
		unix.SIGKILL: SIGKILL,
		unix.SIGPIPE: SIGPIPE,
		unix.SIGQUIT: SIGQUIT,
		unix.SIGSEGV: SIGSEGV,
		unix.SIGTERM: SIGTERM,
		unix.SIGUSR1: SIGUSR1,
		unix.SIGUSR2: SIGUSR2,
	}

	CompleteSet []os.Signal
)

func init() {
	for k := range OSToTransport {
		CompleteSet = append(CompleteSet, k)
	}
}
