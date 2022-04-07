package clog

import "github.com/oklog/ulid"

type Entry struct {
	Timestamp ulid.ULID `cbor:"ts"`
	Data      string    `cbor:"data"`
}
