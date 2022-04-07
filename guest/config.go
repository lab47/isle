package guest

import (
	"context"
	"os"
	"path/filepath"

	"github.com/hashicorp/go-getter"
	"github.com/lab47/isle/pkg/shardconfig"
)

func (g *Guest) LoadConfig(ctx context.Context, path string) (*shardconfig.Config, error) {
	tdir := "/tmp/app"

	defer os.RemoveAll(tdir)

	err := getter.Get(tdir, path, getter.WithContext(ctx))
	if err != nil {
		return nil, err
	}

	appPath := filepath.Join(tdir, "app.hcl")

	cfg, err := shardconfig.Load(appPath)
	if err != nil {
		return nil, err
	}

	cfg.Name = filepath.Base(path)

	return cfg, nil
}
