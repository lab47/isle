package guest

import (
	"context"
	"path/filepath"
	"sync"

	"github.com/lab47/isle/guestapi"
)

type SignalCh struct {
	Data interface{}
}

type Emitter interface {
	Emit(ctx context.Context, val interface{})
}

type SignalHub struct {
	mu        sync.Mutex
	signals   map[string][]chan SignalCh
	selectors map[string]Emitter
}

func NewSignalHub() (*SignalHub, error) {
	sh := &SignalHub{
		signals: make(map[string][]chan SignalCh),
	}

	return sh, nil
}

func (s *SignalHub) makeKey(id *guestapi.ResourceId, name string) string {
	return id.Short() + "-" + name
}

type TypedSignal[T any] struct {
	Data T
}

type signalWaiter[T any] struct {
	next    *signalWaiter[T]
	matcher string
	ch      chan T
}

type Signal[T any] struct {
	mu      sync.Mutex
	waiters *signalWaiter[T]
}

func NewSignal[T any]() *Signal[T] {
	return &Signal[T]{}
}

func (s *Signal[T]) Emit(ctx context.Context, name string, data T) {
	s.mu.Lock()
	defer s.mu.Unlock()

	node := s.waiters

	var toEmit []chan T

	for node != nil {
		matched, _ := filepath.Match(node.matcher, name)
		if matched {
			toEmit = append(toEmit, node.ch)
		}

		node = node.next
	}

	go func() {
		for _, ch := range toEmit {
			select {
			case <-ctx.Done():
				return
			case ch <- data:
				// ok
			}
		}
	}()
}

func (s *Signal[T]) Register(match string) chan T {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan T)
	node := &signalWaiter[T]{
		ch:      ch,
		matcher: match,
		next:    s.waiters,
	}

	s.waiters = node

	return ch
}

func (s *Signal[T]) Unregister(ch chan T) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pos := &s.waiters

	for *pos != nil {
		node := *pos

		if node.ch == ch {
			*pos = node.next
		}

		pos = &node.next
	}
}

func (s *SignalHub) RegisterForResource(id *guestapi.ResourceId, name string) <-chan SignalCh {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan SignalCh, 1)

	key := s.makeKey(id, name)
	s.signals[key] = append(s.signals[key], ch)

	return ch
}

func (s *SignalHub) targets(id *guestapi.ResourceId, name string) []chan SignalCh {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.makeKey(id, name)

	targets := s.signals[key]
	if len(targets) == 0 {
		return nil
	}

	cp := make([]chan SignalCh, len(targets))
	copy(cp, targets)

	return cp
}

func (s *SignalHub) Emit(id *guestapi.ResourceId, name string, sig SignalCh) {
	for _, tgt := range s.targets(id, name) {
		tgt <- sig
	}
}
