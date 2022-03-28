package bytesize

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	Kilobytes = 1024
	Megabytes = Kilobytes * 1024
	Gigabytes = Megabytes * 1024
	Terabytes = Gigabytes * 1024
)

type ByteSize struct {
	Bytes int64
}

type suffix struct {
	str   string
	scale int64
}

var suffixes = []suffix{
	{"KiB", 1024},
	{"kiB", 1024},
	{"K", 1024},
	{"k", 1024},
	{"MiB", 1048576},
	{"miB", 1048576},
	{"M", 1048576},
	{"m", 1048576},
	{"GiB", 1073741824},
	{"giB", 1073741824},
	{"G", 1073741824},
	{"g", 1073741824},
	{"KB", 1000},
	{"MB", 1000000},
	{"GB", 1000000000},
	{"", 0},
}

func Parse(str string) (ByteSize, error) {
	for _, suf := range suffixes {
		if strings.HasSuffix(str, suf.str) {
			numStr := str[:len(str)-len(suf.str)]

			num, err := strconv.ParseInt(numStr, 10, 64)
			if err != nil {
				return ByteSize{}, err
			}

			return ByteSize{
				Bytes: num * suf.scale,
			}, nil
		}
	}

	return ByteSize{}, fmt.Errorf("no known suffix detected")
}
