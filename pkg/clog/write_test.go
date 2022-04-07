package clog

import (
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/oklog/ulid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrite(t *testing.T) {
	t.Run("appends data to the writer", func(t *testing.T) {
		var buf bytes.Buffer

		cw, err := NewWriter(&buf)
		require.NoError(t, err)

		st := ulid.Now()
		err = cw.Write("this is a log line")
		require.NoError(t, err)

		err = cw.Flush()
		require.NoError(t, err)

		var ent Entry

		dec := cbor.NewDecoder(bytes.NewReader(buf.Bytes()))

		err = dec.Decode(&ent)
		require.NoError(t, err)

		assert.Equal(t, "this is a log line", ent.Data)

		assert.InDelta(t, st, ent.Timestamp.Time(), 1000)
	})

}

func TestWriteDirectory(t *testing.T) {
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

	t.Run("rotates when the size limit is hit", func(t *testing.T) {
		defer setup()()

		cw, err := NewDirectoryWriter(td, 5, 2)
		require.NoError(t, err)

		err = cw.Write("this is a log line")
		require.NoError(t, err)

		err = cw.Flush()
		require.NoError(t, err)

		entries, err := ioutil.ReadDir(td)
		require.NoError(t, err)

		require.Len(t, entries, 2)
	})

	t.Run("only keeps N rotations before pruning old ones", func(t *testing.T) {
		defer setup()()

		cw, err := NewDirectoryWriter(td, 5, 2)
		require.NoError(t, err)

		err = cw.Write("this is a log line")
		require.NoError(t, err)

		e1, err := ioutil.ReadDir(td)
		require.NoError(t, err)

		require.Len(t, e1, 2)

		time.Sleep(10 * time.Millisecond)

		err = cw.Write("this is a log line")
		require.NoError(t, err)

		e2, err := ioutil.ReadDir(td)
		require.NoError(t, err)

		require.Len(t, e2, 2)

		time.Sleep(10 * time.Millisecond)

		err = cw.Write("this is a log line")
		require.NoError(t, err)

		e3, err := ioutil.ReadDir(td)
		require.NoError(t, err)

		require.Len(t, e3, 2)

		assert.Equal(t, e1[1].Name(), e2[0].Name())
		assert.Equal(t, e2[1].Name(), e3[0].Name())

		assert.NotEqual(t, e1, e2)
		assert.NotEqual(t, e2, e3)
	})
}
