package guest

import (
	"sync"

	"github.com/rs/xid"
)

type Advertisement interface {
	IsAdvert()
}

type AdvertiseRun struct {
	Source string
	Name   string
}

func (a *AdvertiseRun) IsAdvert() {}

type Advertisements struct {
	mu      sync.Mutex
	adverts map[string]Advertisement
}

func (a *Advertisements) Add(ad Advertisement) string {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.adverts == nil {
		a.adverts = map[string]Advertisement{}
	}

	id := xid.New().String()

	a.adverts[id] = ad

	return id
}

func (a *Advertisements) Remove(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	delete(a.adverts, id)
}

func (a *Advertisements) Adverts() []Advertisement {
	a.mu.Lock()
	defer a.mu.Unlock()

	var out []Advertisement

	for _, ad := range a.adverts {
		out = append(out, ad)
	}

	return out
}
