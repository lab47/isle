package timesync

import (
	"time"

	"github.com/hashicorp/go-hclog"
	"golang.org/x/sys/unix"
)

func SetSystemTime(log hclog.Logger, base, offset int64) error {
	t1 := base + offset
	newSec, newNano := t1/1000000000, t1%10000000000

	before := time.Now()

	err := unix.Settimeofday(&unix.Timeval{
		Sec:  newSec,
		Usec: int32(newNano / 1000),
	})
	if err != nil {
		log.Error("error setting time", "error", err)
	}

	if offset > int64(time.Second) {
		log.Info("corrected large time skew",
			"new-time", time.Now().String(),
			"old-time", before.String(),
			"diff", offset,
		)
	}

	return nil
}
