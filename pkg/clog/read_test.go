package clog

import (
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRead(t *testing.T) {
	t.Run("can read values", func(t *testing.T) {
		var buf bytes.Buffer

		w, err := NewWriter(&buf)
		require.NoError(t, err)

		err = w.Write("this is a log")
		require.NoError(t, err)

		w.Flush()

		r, err := NewReader(bytes.NewReader(buf.Bytes()))
		require.NoError(t, err)

		ent, err := r.Next()
		require.NoError(t, err)

		assert.Equal(t, "this is a log", ent.Data)

		_, err = r.Next()
		require.Error(t, err)
	})
}

func TestDirectoryReader(t *testing.T) {
	tmpdir, err := ioutil.TempDir("", "clog")
	require.NoError(t, err)

	defer os.RemoveAll(tmpdir)

	td := filepath.Join(tmpdir, "cur")

	setup := func() func() {
		os.MkdirAll(td, 0755)
		return func() {
			os.RemoveAll(td)
		}
	}

	t.Run("reads data from the directory in order", func(t *testing.T) {
		defer setup()()

		cw, err := NewDirectoryWriter(td, 5, 2)
		require.NoError(t, err)

		err = cw.Write("this is a log line")
		require.NoError(t, err)

		time.Sleep(10 * time.Millisecond)

		err = cw.Write("this is a log line")
		require.NoError(t, err)

		time.Sleep(10 * time.Millisecond)

		err = cw.Write("this is a log line")
		require.NoError(t, err)

		r, err := NewDirectoryReader(td)
		require.NoError(t, err)

		var entries []*Entry

		for {
			ent, err := r.Next()
			if err != nil {
				break
			}

			entries = append(entries, ent)
		}

		assert.True(t, sort.SliceIsSorted(entries, func(i, j int) bool {
			return entries[i].Timestamp.Time() < entries[j].Timestamp.Time()
		}))
	})
}
