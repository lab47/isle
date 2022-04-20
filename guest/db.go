package guest

import (
	"encoding/json"
	"errors"

	"go.etcd.io/bbolt"
)

func (g *Guest) openDB() error {
	db, err := bbolt.Open("/data/data.db", 0644, bbolt.DefaultOptions)
	if err != nil {
		return err
	}

	g.db = db

	return nil
}

var ErrUnknownKey = errors.New("unknown key")

func (g *Guest) getVar(name string, out interface{}) error {
	var val []byte

	err := g.db.View(func(tx *bbolt.Tx) error {
		buk, err := tx.CreateBucketIfNotExists([]byte("config"))
		if err != nil {
			return err
		}

		val = buk.Get([]byte(name))
		return nil
	})
	if err != nil {
		return err
	}

	if val == nil {
		return ErrUnknownKey
	}

	return json.Unmarshal(val, &out)
}

func (g *Guest) setVar(name string, in interface{}) error {
	val, err := json.Marshal(in)
	if err != nil {
		return err
	}

	return g.db.Update(func(tx *bbolt.Tx) error {
		buk, err := tx.CreateBucketIfNotExists([]byte("config"))
		if err != nil {
			return err
		}

		return buk.Put([]byte(name), val)
	})
}
