package actor

import (
	"context"
	"testing"
)

type simpleActor struct {
	Count int
}

type Add struct {
	Incr int
}

func (s *simpleActor) Handle(ctx context.Context, add Add) error {
	s.Count += add.Incr
	return nil
}

type Sy interface {
	Wait()
}

var S Sy

func (s *simpleActor) Create(ctx context.Context, name string) error {

}

func TestActor(t *testing.T) {

}
