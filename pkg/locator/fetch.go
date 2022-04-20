package locator

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v43/github"
	"github.com/lab47/isle/pkg/shardconfig"
	"github.com/pkg/errors"
)

var ErrBadLocation = errors.New("bad location")

func Fetch(ctx context.Context, sel string) (*shardconfig.Config, []byte, error) {
	loc, err := Resolve(ctx, sel)
	if err != nil {
		return nil, nil, err
	}

	rl, err := ResolveLocation(ctx, loc)
	if err != nil {
		return nil, nil, err
	}

	cfg, err := FetchConfig(ctx, rl)
	if err != nil {
		return nil, nil, err
	}

	obj, err := shardconfig.Decode(sel, []byte(cfg))
	if err != nil {
		return nil, nil, err
	}

	return obj, []byte(cfg), nil
}

func FetchConfig(ctx context.Context, loc *ResolvedLocation) (string, error) {
	switch loc.Type {
	case "github":
		gh := github.NewClient(nil)
		parts := strings.SplitN(loc.Location, "/", 3)
		file, dir, _, err := gh.Repositories.GetContents(ctx, parts[0], parts[1], parts[2], nil)
		if err != nil {
			return "", nil
		}

		if file != nil {
			return file.GetContent()
		}

		for _, ent := range dir {
			if ent.GetName() == "app.hcl" {
				file, _, _, err := gh.Repositories.GetContents(
					ctx, parts[0], parts[1], ent.GetPath(), nil,
				)
				if err != nil {
					return "", err
				}

				if file == nil {
					return "", errors.Wrapf(ErrBadLocation, "app.hcl was not a file")
				}

				return file.GetContent()
			}
		}
	}

	return "", fmt.Errorf("Unable to resolve %s:%s", loc.Type, loc.Location)
}
