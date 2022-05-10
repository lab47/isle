package guestapi

import (
	"fmt"

	"github.com/oklog/ulid"
)

func (id *ResourceId) Name() string {
	var u ulid.ULID

	copy(u[:], id.UniqueId)

	return fmt.Sprintf("%s.%s.%s", id.Category, id.Type, u.String())
}

func (id *ResourceId) Short() string {
	var u ulid.ULID

	copy(u[:], id.UniqueId)

	return u.String()
}
