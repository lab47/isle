package guest

import (
	"os"
	"testing"

	"github.com/lab47/isle/guestapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
)

func TestResourceStorage(t *testing.T) {
	t.Run("can index protobufs", func(t *testing.T) {
		tf, err := os.CreateTemp("", "rs")
		require.NoError(t, err)

		defer os.Remove(tf.Name())

		tf.Close()

		db, err := bbolt.Open(tf.Name(), 0644, bbolt.DefaultOptions)
		require.NoError(t, err)

		defer db.Close()

		var r ResourceStorage
		r.db = db

		err = r.Init()
		require.NoError(t, err)

		schema, err := r.SetSchema("container", "container", &guestapi.Container{}, "stable_name")
		require.NoError(t, err)

		id := schema.NewId()

		cont := &guestapi.Container{
			StableName: "foo",
		}

		_, err = r.Set(id, cont, nil)
		require.NoError(t, err)

		cont2 := &guestapi.Container{
			StableName: "bar",
		}

		id2 := schema.NewId()

		_, err = r.Set(id2, cont2, nil)
		require.NoError(t, err)

		cont3 := &guestapi.Container{
			StableName: "zux",
		}

		_, err = r.Set(schema.NewId(), cont3, nil)
		require.NoError(t, err)

		cont4 := &guestapi.Container{
			StableName: "bar",
		}

		id4 := schema.NewId()
		_, err = r.Set(id4, cont4, nil)
		require.NoError(t, err)

		key, err := schema.Key("stable_name", "foo")
		require.NoError(t, err)

		res, err := r.FetchByIndex(key)
		require.NoError(t, err)

		require.Len(t, res, 1)

		assert.True(t, proto.Equal(id, res[0].Id))

		key2, err := schema.Key("stable_name", "bar")
		require.NoError(t, err)

		res2, err := r.FetchByIndex(key2)
		require.NoError(t, err)

		require.Len(t, res2, 2)

		assert.True(t, proto.Equal(id2, res2[0].Id), "%s <> %s", id2, res2[0].Id.String())
		assert.True(t, proto.Equal(id4, res2[1].Id), "%s <> %s", id4, res2[1].Id.String())

		// Change one and see the indexes change

		cont4.StableName = "bar2"
		_, err = r.Set(id4, cont4, nil)
		require.NoError(t, err)

		res3, err := r.FetchByIndex(key2)
		require.NoError(t, err)

		require.Len(t, res3, 1)

		assert.True(t, proto.Equal(id2, res3[0].Id), "%s <> %s", id2, res2[0].Id.String())

		cont.StableName = "abc"
		_, err = r.Set(id, cont, nil)
		require.NoError(t, err)

		res4, err := r.FetchByIndex(key)
		require.NoError(t, err)

		require.Len(t, res4, 0)
	})
}
