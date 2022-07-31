package labels

import (
	"sort"
	"strings"

	"golang.org/x/exp/slices"
)

type Pair struct {
	Key, Value string
}

type Set struct {
	pairs []Pair
}

func New(labels ...string) Set {
	if len(labels)%2 != 0 {
		panic("labels not even")
	}

	var s []Pair

	for i := 0; i < len(labels); i += 2 {
		s = append(s, Pair{
			Key:   labels[i],
			Value: labels[i+1],
		})
	}

	sort.Slice(s, func(i, j int) bool {
		return s[i].Key < s[j].Key
	})

	return Set{pairs: s}
}

func (s Set) String() string {
	var set []string

	for _, p := range s.pairs {
		set = append(set, p.Key+"="+p.Value)
	}

	return strings.Join(set, ",")
}

func (s Set) Len() int {
	return len(s.pairs)
}

func (s Set) Match(os Set) bool {
	sp := s.pairs
	op := os.pairs

	// sp can have more pairs than op, but everything in op must be in sp

	for len(op) > 0 {
		switch {
		case len(sp) == 0:
			return false
		case op[0] == sp[0]:
			op = op[1:]
			sp = sp[1:]
		case op[0].Key < sp[0].Key:
			return false
		default:
			sp = sp[1:]
		}
	}

	return true
}

func (s Set) Pairs() []Pair {
	return slices.Clone(s.pairs)
}
