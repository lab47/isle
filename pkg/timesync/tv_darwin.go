package timesync

import (
	"time"

	"golang.org/x/sys/unix"
)

func toTimeVal(t time.Time) *unix.Timeval {
	return &unix.Timeval{
		Sec:  t.Unix(),
		Usec: int32(t.Nanosecond() / 1000),
	}
}
