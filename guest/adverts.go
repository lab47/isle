package guest

import (
	"sync"

	"github.com/rs/xid"
)

type AdvertEvent struct {
	Add           bool
	Advertisement Advertisement
}

type Advertisement interface {
	IsAdvert()
}

type AdvertiseRun struct {
	Source string
	Name   string
}

func (a *AdvertiseRun) IsAdvert() {}

type Advertisements struct {
	mu           sync.Mutex
	adverts      map[string]Advertisement
	advertEvents map[string]chan *AdvertEvent
}

func (a *Advertisements) RegisterEvents(id string) ([]Advertisement, chan *AdvertEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()

	ads := make([]Advertisement, len(a.adverts))

	for _, ad := range a.adverts {
		ads = append(ads, ad)
	}

	ch := make(chan *AdvertEvent, 1)

	if a.advertEvents == nil {
		a.advertEvents = map[string]chan *AdvertEvent{}
	}

	a.advertEvents[id] = ch

	return ads, ch
}

func (a *Advertisements) UnregisterEvents(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	delete(a.advertEvents, id)
}

func (a *Advertisements) Add(ad Advertisement) string {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.adverts == nil {
		a.adverts = map[string]Advertisement{}
	}

	id := xid.New().String()

	a.adverts[id] = ad

	go func() {
		for _, ch := range a.advertEvents {
			ch <- &AdvertEvent{
				Add:           true,
				Advertisement: ad,
			}
		}
	}()

	return id
}

func (a *Advertisements) Remove(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	ad := a.adverts[id]
	delete(a.adverts, id)

	go func() {
		for _, ch := range a.advertEvents {
			ch <- &AdvertEvent{
				Add:           true,
				Advertisement: ad,
			}
		}
	}()

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
