package clog

import (
	"bufio"
	"context"
	"crypto/rand"
	"io"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fxamacker/cbor/v2"
	"github.com/oklog/ulid"
)

type Writer struct {
	w io.Writer
	e *cbor.Encoder

	st sizeTracker
}

func NewWriter(w io.Writer) (*Writer, error) {
	cw := &Writer{
		w: w,
	}

	cw.e = cbor.NewEncoder(io.MultiWriter(cw.w, &cw.st))

	return cw, nil
}

var reader = ulid.Monotonic(rand.Reader, 0)

func (w *Writer) Write(line string) error {
	ts, err := ulid.New(ulid.Now(), reader)
	if err != nil {
		return err
	}

	return w.e.Encode(Entry{
		Timestamp: ts,
		Data:      line,
	})
}

func (w *Writer) Flush() error {
	return nil
}

type DirectoryWriter struct {
	*Writer

	cur *os.File
	dir string

	max    int64
	shards int64
}

type sizeTracker struct {
	sz int64
}

func (s *sizeTracker) Write(data []byte) (int, error) {
	s.sz += int64(len(data))
	return len(data), nil
}

const (
	DefaultMax    = 1024 * 1024
	DefaultShards = 10
)

func NewDirectoryWriter(dir string, max, shards int64) (*DirectoryWriter, error) {
	if max == 0 {
		max = DefaultMax
	}

	if shards == 0 {
		shards = DefaultShards
	}

	ts, err := ulid.New(ulid.Now(), reader)
	if err != nil {
		return nil, err
	}

	os.Mkdir(dir, 0755)

	path := filepath.Join(dir, ts.String()+".clog")

	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}

	dw := &DirectoryWriter{
		cur:    f,
		dir:    dir,
		max:    max,
		shards: shards,
	}

	w, err := NewWriter(f)
	if err != nil {
		return nil, err
	}

	dw.Writer = w

	return dw, nil
}

func (d *DirectoryWriter) IOInput(ctx context.Context) (io.Writer, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	go func() {
		defer r.Close()

		br := bufio.NewReader(r)

		for {
			line, _ := br.ReadString('\n')
			if line == "" {
				return
			}

			err := d.Write(strings.TrimRight(line, " \t\n"))
			if err != nil {
				return
			}
		}
	}()

	return w, nil
}

func (d *DirectoryWriter) Write(data string) error {
	err := d.Writer.Write(data)
	if err != nil {
		return err
	}

	if d.Writer.st.sz >= d.max {
		err = d.rotate()
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *DirectoryWriter) Close() error {
	d.Flush()
	return d.cur.Close()
}

func (d *DirectoryWriter) pruneEntries(entries []fs.FileInfo) error {
	type diskEntry struct {
		id   ulid.ULID
		name string
	}

	var ids []diskEntry

	for _, e := range entries {
		strId := filepath.Base(e.Name())
		dot := strings.IndexByte(strId, '.')
		if dot == -1 {
			continue
		}

		id, err := ulid.Parse(strId[:dot])
		if err != nil {
			continue
		}

		ids = append(ids, diskEntry{id, e.Name()})
	}

	sort.Slice(ids, func(i, j int) bool {
		return ids[i].id.Time() < ids[j].id.Time()
	})

	toRemove := ids[:len(ids)-int(d.shards)]

	for _, ent := range toRemove {
		os.Remove(filepath.Join(d.dir, ent.name))
	}

	return nil
}

func (d *DirectoryWriter) rotate() error {
	d.Flush()
	d.cur.Close()

	ts, err := ulid.New(ulid.Now(), reader)
	if err != nil {
		return err
	}

	path := filepath.Join(d.dir, ts.String()+".clog")

	f, err := os.Create(path)
	if err != nil {
		return err
	}

	w, err := NewWriter(f)
	if err != nil {
		return err
	}

	d.Writer = w

	entries, err := ioutil.ReadDir(d.dir)
	if err != nil {
		return err
	}

	if int64(len(entries)) >= d.shards {
		d.pruneEntries(entries)
	}

	return nil
}
