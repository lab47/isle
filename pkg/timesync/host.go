package timesync

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/go-hclog"
	"golang.org/x/sys/unix"
)

func Host(ctx context.Context, log hclog.Logger, w io.Writer) {
	var tz string

	path, err := os.Readlink("/etc/localtime")
	if err == nil {
		idx := strings.Index(path, "/zoneinfo/")
		if idx != -1 {
			tz = path[idx+10:]
		}
	}

	log.Info("running host time sync", "tz", tz)

	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()

	bw := bufio.NewWriter(w)

	for {
		t := time.Now()
		if tz == "" {
			tz, _ = t.Zone()
		}

		data, _ := t.MarshalBinary()
		bw.WriteByte(byte(len(data)))
		bw.Write(data)
		fmt.Fprintln(bw, tz)

		err = bw.Flush()
		if err != nil {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			// ok
		}
	}
}

func Guest(ctx context.Context, log hclog.Logger, r io.Reader) {
	log.Info("running guest time sync")

	br := bufio.NewReader(r)

	buf := make([]byte, 128)

	var setTZ string

	for {
		b, err := br.ReadByte()
		if err != nil {
			return
		}

		l := int(b)

		_, err = io.ReadFull(br, buf[:l])
		if err != nil {
			return
		}

		var t time.Time

		err = t.UnmarshalBinary(buf[:l])
		if err != nil {
			continue
		}

		name, err := br.ReadString('\n')
		if err != nil {
			continue
		}

		name = strings.TrimRight(name, " \n")

		unix.Settimeofday(toTimeVal(t))

		if setTZ == "" {
			_, offset := t.Zone()

			log.Info("detected time from host",
				"time", t.String(),
				"tz", name,
				"offset", offset)

			offset = offset / 60 / 60

			if name == "" {
				switch {
				case offset == 0:
					name = "GMT"
				case offset < 0:
					name = fmt.Sprintf("GMT-%d", -offset)
				default:
					name = fmt.Sprintf("GMT+%d", offset)
				}
			}

			setTZFile(log, name)

			setTZ = name
		}
	}
}

func setTZFile(log hclog.Logger, name string) {
	path := fmt.Sprintf("/usr/share/zoneinfo/%s", name)

	w, err := os.Open(path)
	if err != nil {
		log.Error("unable to find tz path", "path", path)
		return
	}

	defer w.Close()

	f, err := os.Create("/etc/localtime")
	if err != nil {
		return
	}

	defer f.Close()
	io.Copy(f, w)
}
