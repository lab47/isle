package locator

import (
	"context"
	"testing"

	"github.com/lab47/isle/pkg/shardconfig"
	"github.com/stretchr/testify/require"
)

func TestLocator(t *testing.T) {
	t.Run("can fetch a config from github", func(t *testing.T) {
		ctx := context.Background()

		loc, err := Resolve(ctx, "docker")
		require.NoError(t, err)

		rl, err := ResolveLocation(ctx, loc)
		require.NoError(t, err)

		cfg, err := FetchConfig(ctx, rl)
		require.NoError(t, err)

		_, err = shardconfig.Decode("test", []byte(cfg))
		require.NoError(t, err)
	})
}
