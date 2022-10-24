package guest

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/lab47/isle/pkg/shardconfig"
	"github.com/pkg/errors"
	"github.com/rs/xid"
)

func (g *Guest) monitorAppsDir(ctx context.Context) {
	g.startAppsDir(ctx)

	root := "/data/apps/running"
	os.MkdirAll(root, 0755)

	w, err := fsnotify.NewWatcher()
	if err != nil {
		g.L.Error("unable to create fs watcher", "err", err)
		return
	}

	defer w.Close()

	w.Add(root)

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-w.Events:
			switch ev.Op {
			case fsnotify.Create, fsnotify.Rename:
				g.startApp(ctx, ev.Name)
			case fsnotify.Remove:
				g.removeApp(ctx, ev.Name)
			}
		}
	}
}

func (g *Guest) validateApp(ctx context.Context, path string) error {
	_, err := g.LoadConfig(ctx, path)
	if err != nil {
		return errors.Wrapf(err, "invalid app configuration")
	}

	return nil
}

func (g *Guest) startApp(ctx context.Context, appPath string) {
	if _, ok := g.detectedApps[appPath]; ok {
		g.L.Debug("already running app, skipping", "path", appPath)
		return
	}

	id := xid.New().String()

	g.L.Info("starting app", "id", id, "path", appPath)

	cfg, err := g.LoadConfig(ctx, appPath)
	if err != nil {
		g.L.Error("error loading app config", "error", err)
		return
	}

	ctx, cancel := context.WithCancel(ctx)

	doneCh := make(chan error)

	g.apps[cfg.Name] = &runningContainer{
		id:     id,
		cancel: cancel,
		doneCh: doneCh,
	}

	g.detectedApps[appPath] = cfg.Name

	go func() {
		defer close(doneCh)

		err = g.MonitorApp(ctx, appPath, cfg, id)
		if err != nil {
			if err != context.Canceled {
				g.L.Error("error starting app", "error", err, "path", appPath)
			}
		}

		doneCh <- err
	}()

}

func (g *Guest) removeApp(ctx context.Context, appPath string) {
	name, ok := g.detectedApps[appPath]
	if !ok {
		g.L.Warn("attempted to remove unknown app", "path", appPath)
		return
	}

	rc, ok := g.apps[name]
	if !ok {
		g.L.Warn("no configuration for app found", "name", name)
		return
	}

	rc.cancel()

	select {
	case <-ctx.Done():
		return
	case <-rc.doneCh:
		return
	}
}

func (g *Guest) startAppsDir(ctx context.Context) {
	if g.detectedApps == nil {
		g.detectedApps = map[string]string{}
	}

	root := "/data/apps/running"
	os.MkdirAll(root, 0755)

	entries, err := ioutil.ReadDir(root)
	if err != nil {
		g.L.Error("error reading apps", "error", err)
		return
	}

	for _, ent := range entries {
		appPath := filepath.Join(root, ent.Name())
		g.startApp(ctx, appPath)
	}
}

func (g *Guest) MonitorApp(ctx context.Context, path string, cfg *shardconfig.Config, id string) error {
	errCh := make(chan error)

	lastStart := time.Now()

	go func() {
		errCh <- g.StartApp(ctx, path, cfg, id)
	}()

	for {
		select {
		case err := <-errCh:
			select {
			case <-ctx.Done():
				// ok, we're done anyway!
				return err
			default:
			}

			if err == context.Canceled {
				return err
			}

			g.L.Error("error running app, restarting", "error", err)
			if time.Since(lastStart) < 10*time.Second {
				g.L.Info("detected fast crash, pausing before restarting")
				time.Sleep(10 * time.Second)
			}

			go func() {
				errCh <- g.StartApp(ctx, path, cfg, id)
			}()
		}
	}
}

func (g *Guest) StartApp(ctx context.Context, path string, cfg *shardconfig.Config, id string) error {
	return fmt.Errorf("removed")
}

func (g *Guest) runApp(ctx context.Context, bundlePath string, info ContainerInfo, cfg *shardconfig.Config, id string) error {
	return io.EOF
}
