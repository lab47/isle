package values

import (
	"reflect"

	"github.com/hashicorp/go-hclog"
	"github.com/pkg/errors"
)

type Universe struct {
	log       hclog.Logger
	fixed     map[reflect.Type]reflect.Value
	construct map[reflect.Type]reflect.Value
}

func NewUniverse(l hclog.Logger) *Universe {
	u := &Universe{
		log:       l,
		fixed:     make(map[reflect.Type]reflect.Value),
		construct: make(map[reflect.Type]reflect.Value),
	}

	u.fixed[reflect.TypeOf(u)] = reflect.ValueOf(u)

	for _, t := range Registry {
		var err error

		et := reflect.TypeOf(&err).Elem()

		ft := reflect.FuncOf(nil, []reflect.Type{t, et}, false)

		t := ft.Out(0)

		fv := reflect.MakeFunc(ft, func(args []reflect.Value) (results []reflect.Value) {
			return []reflect.Value{reflect.New(t.Elem()), reflect.Zero(et)}
		})

		u.construct[t] = fv
	}

	return u
}

func (u *Universe) Provide(v interface{}) {
	rv := reflect.ValueOf(v)

	t := rv.Type()

	u.fixed[t] = rv
}

func (u *Universe) ProvideAs(v, vt interface{}) {
	rv := reflect.ValueOf(v)

	vrv := reflect.ValueOf(vt)
	t := vrv.Type().Elem()

	u.fixed[t] = rv
}

func (u *Universe) Register(f interface{}) {
	rv := reflect.ValueOf(f)

	var err error

	et := reflect.TypeOf(&err).Elem()

	ft := reflect.FuncOf(nil, []reflect.Type{rv.Type(), et}, false)

	fv := reflect.MakeFunc(ft, func(args []reflect.Value) (results []reflect.Value) {
		return []reflect.Value{reflect.New(rv.Elem().Type()), reflect.Zero(et)}
	})

	t := ft.Out(0)

	u.construct[t] = fv
}

func (u *Universe) Construct(f interface{}) {
	rv := reflect.ValueOf(f)

	if rv.Kind() != reflect.Func {
		panic("construct not passed a func")
	}

	ft := rv.Type()

	if ft.NumOut() != 2 {
		panic("construct func must return (type, error)")
	}

	t := ft.Out(0)

	u.construct[t] = rv
}

var ErrBadType = errors.New("bad type for commit")

func (u *Universe) Commit(v interface{}) error {
	rv := reflect.ValueOf(v)

	t := rv.Type()

	if t.Kind() != reflect.Ptr {
		return errors.Wrapf(ErrBadType, "type must be a pointer")
	}

	t = t.Elem()

	if t.Kind() != reflect.Struct {
		return errors.Wrapf(ErrBadType, "type must be a pointer to struct")
	}

	rv = rv.Elem()

	for i := 0; i < t.NumField(); i++ {
		f := rv.Field(i)

		if !f.IsZero() {
			continue
		}

		if rv, ok := u.fixed[f.Type()]; ok {
			f.Set(rv)

			u.log.Trace("assigned field", "type", t, "field", f.Type())
		} else if fv, ok := u.construct[f.Type()]; ok {
			vals := fv.Call(nil)
			if len(vals) == 2 {
				if ev, ok := vals[1].Interface().(error); ok {
					return ev
				}
			}

			rv := vals[0]

			if !f.CanConvert(rv.Type()) {
				return errors.Wrapf(
					ErrBadType,
					"return value of construct func wrong type: %T != %T", f.Type(), rv.Type(),
				)
			}

			err := u.Commit(rv.Interface())
			if err != nil {
				return err
			}

			u.fixed[f.Type()] = rv

			u.log.Trace("assigned field via construct", "type", t, "field", f.Type())
			f.Set(rv)
		}
	}

	return nil
}
