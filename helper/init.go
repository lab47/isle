package helper

import (
	"os"
	"os/signal"
	"time"

	"golang.org/x/sys/unix"
)

func InitMain() {
	signal.Ignore(unix.SIGPIPE, unix.SIGINT)

	exitCh := make(chan os.Signal, 1)

	signal.Notify(exitCh, unix.SIGQUIT)

	ch := make(chan os.Signal, 1)

	signal.Notify(ch, unix.SIGCHLD)

	tick := time.NewTicker(5 * time.Second)

	for {
		select {
		case <-ch:
			//ok
		case <-tick.C:
			// ok
		case <-exitCh:
			os.Exit(1)
		}

		var (
			status unix.WaitStatus
			usage  unix.Rusage
		)

		unix.Wait4(-1, &status, 0, &usage)
	}
}
