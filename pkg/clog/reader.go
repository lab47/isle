package clog

import (
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/fxamacker/cbor/v2"
	"github.com/pkg/errors"
)

type Reader struct {
	r io.Reader
	d *cbor.Decoder
}

func NewReader(r io.Reader) (*Reader, error) {
	d := cbor.NewDecoder(r)

	return &Reader{
		r: r,
		d: d,
	}, nil
}

func (r *Reader) Next() (*Entry, error) {
	var ent Entry

	err := r.d.Decode(&ent)
	if err != nil {
		return nil, err
	}

	return &ent, nil
}

type DirectoryReader struct {
	dir string
	cur string

	f *os.File
	r *Reader
}

func NewDirectoryReader(dir string) (*DirectoryReader, error) {
	r := &DirectoryReader{dir: dir}

	err := r.openNext()
	if err != nil {
		return nil, err
	}

	return r, nil
}

func (d *DirectoryReader) openNext() error {
	if d.f != nil {
		d.f.Close()
	}

	entries, err := ioutil.ReadDir(d.dir)
	if err != nil {
		return errors.Wrapf(err, "attempting to read directory: %s", d.dir)
	}

	var target string

	if d.cur == "" {
		target = entries[0].Name()
	} else {
		var next bool

		for _, ent := range entries {
			if next {
				target = ent.Name()
				break
			}
			if ent.Name() == d.cur {
				next = true
			}
		}
	}

	if target == "" {
		return errors.Wrapf(io.EOF, "no more files to read. cur=%s", d.cur)
	}

	f, err := os.Open(filepath.Join(d.dir, target))
	if err != nil {
		return err
	}

	d.r, err = NewReader(f)
	if err != nil {
		return err
	}

	d.cur = target
	d.f = f

	return nil
}

func (d *DirectoryReader) Next() (*Entry, error) {
	ent, err := d.r.Next()
	if err == nil {
		return ent, nil
	}

	err = d.openNext()
	if err != nil {
		return nil, err
	}

	return d.r.Next()
}
