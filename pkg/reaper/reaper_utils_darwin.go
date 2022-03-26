package reaper

import "io"

func SetSubreaper(i int) error {
	return io.EOF
}

// GetSubreaper returns the subreaper setting for the calling process
func GetSubreaper() (int, error) {
	return 0, io.EOF
}
