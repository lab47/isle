package timesync

import (
	"time"

	"github.com/hashicorp/go-hclog"
	"golang.org/x/sys/unix"
)

var (
	ADJ_OFFSET    = 0x0001 /* time offset */
	ADJ_FREQUENCY = 0x0002 /* frequency offset */
	ADJ_MAXERROR  = 0x0004 /* maximum time error */
	ADJ_ESTERROR  = 0x0008 /* estimated time error */
	ADJ_STATUS    = 0x0010 /* clock status */
	ADJ_TIMECONST = 0x0020 /* pll time constant */
	ADJ_TAI       = 0x0080 /* set TAI offset */
	ADJ_SETOFFSET = 0x0100 /* add 'time' to current time */
	ADJ_MICRO     = 0x1000 /* select microsecond resolution */
	ADJ_NANO      = 0x2000 /* select nanosecond resolution */
	ADJ_TICK      = 0x4000 /* tick value */
)

func SetSystemTime(log hclog.Logger, base, offset int64) error {
	change := offset
	if change < 0 {
		change = -change
	}

	if change < 500000000 {
		var buf unix.Timex
		buf.Modes = uint32(ADJ_OFFSET | ADJ_NANO)
		buf.Offset = offset

		_, err := unix.Adjtimex(&buf)
		if err == nil {
			log.Trace("time adjustment", "offset", offset)
			return nil
		}

		log.Error("error adjusting time with adjtimex, using settimeofday", "error", err)
	}

	t1 := base + offset
	newSec := t1 / 1e9
	newNano := t1 % 1e9

	before := time.Now()

	err := unix.Settimeofday(&unix.Timeval{
		Sec:  newSec,
		Usec: int64(newNano / 1000),
	})
	if err != nil {
		log.Error("error setting time", "error", err)
	}

	log.Info("corrected large time skew",
		"new-time", time.Now().String(),
		"old-time", before.String(),
		"diff", offset,
		"sec", newSec,
		"nsec", newNano,
		"base", base,
		"t1", t1,
	)

	return nil
}
