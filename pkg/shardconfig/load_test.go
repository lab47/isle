package shardconfig

import (
	"testing"

	"github.com/davecgh/go-spew/spew"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	t.Run("basic oci", func(t *testing.T) {
		cfg, err := Load("testdata/docker.hcl")
		require.NoError(t, err)

		spew.Dump(cfg)
	})

	t.Run("rootfs", func(t *testing.T) {
		cfg, err := Load("testdata/ubuntu.hcl", WithPlatform("amd64"))
		require.NoError(t, err)

		assert.Equal(t, "amd64-only", cfg.Root.URL)

		spew.Dump(cfg)
	})
}
