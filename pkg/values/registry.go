package values

import "reflect"

var Registry = []reflect.Type{}

func Register(f interface{}) error {
	Registry = append(Registry, reflect.TypeOf(f))
	return nil
}
