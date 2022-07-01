package guestapi

import "github.com/lab47/isle/pkg/labels"

func (l *Labels) Set() labels.Set {
	var vals []string

	for _, pair := range l.Pairs {
		vals = append(vals, pair.Key, pair.Value)
	}

	return labels.New(vals...)
}

func FromSet(s labels.Set) *Labels {
	var lbs Labels

	for _, pair := range s.Pairs() {
		lbs.Pairs = append(lbs.Pairs, &Labels_Pair{
			Key:   pair.Key,
			Value: pair.Value,
		})
	}

	return &lbs
}
