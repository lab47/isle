package helper

import (
	"os"
	"os/signal"
	"time"

	"golang.org/x/sys/unix"
)

func InitMain() {
	// see if we should instead just launch the container's own
	// init.

	if _, err := os.Stat("/sbin/init"); err == nil {
		unix.Exec("/sbin/init", []string{"/sbin/init"}, os.Environ())
		// If exec fails, we stay running our own exec.
	}

	signal.Ignore(unix.SIGPIPE, unix.SIGINT)

	ch := make(chan os.Signal, 1)

	signal.Notify(ch, unix.SIGCHLD)

	tick := time.NewTicker(5 * time.Second)

	for {
		select {
		case <-ch:
			//ok
		case <-tick.C:
			// ok
		}

		var (
			status unix.WaitStatus
			usage  unix.Rusage
		)

		unix.Wait4(-1, &status, 0, &usage)
	}
}
