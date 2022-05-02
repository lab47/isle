package guest

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/lab47/isle/pkg/clog"
	"github.com/lab47/isle/pkg/runc"
	"github.com/lab47/isle/pkg/shardconfig"
	"github.com/opencontainers/runtime-spec/specs-go"
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

		err := g.MonitorApp(ctx, cfg, id)
		if err != nil {
			if err != context.Canceled {
				g.L.Error("error starting app", "error", err, "path", appPath)
			}
		}

		doneCh <- err
	}()

}

func (g *Guest) StartConfig(ctx context.Context, id string, cfg *shardconfig.Config) {
	if _, ok := g.detectedApps[id]; ok {
		g.L.Debug("already running app, skipping", "id", id)
		return
	}

	g.L.Info("starting app", "id", id)

	ctx, cancel := context.WithCancel(ctx)

	doneCh := make(chan error)

	g.apps[cfg.Name] = &runningContainer{
		id:     id,
		cancel: cancel,
		doneCh: doneCh,
	}

	g.detectedApps[id] = cfg.Name

	go func() {
		defer close(doneCh)

		err := g.MonitorApp(ctx, cfg, id)
		if err != nil {
			if err != context.Canceled {
				g.L.Error("error starting app", "error", err, "id", id)
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

func (g *Guest) MonitorApp(ctx context.Context, cfg *shardconfig.Config, id string) error {
	errCh := make(chan error)

	lastStart := time.Now()

	go func() {
		errCh <- g.StartApp(ctx, cfg, id)
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
				errCh <- g.StartApp(ctx, cfg, id)
			}()
		}
	}
}

func (g *Guest) StartApp(ctx context.Context, cfg *shardconfig.Config, id string) error {
	started := make(chan string, 1)
	errorer := make(chan error, 1)

	info := ContainerInfo{
		Name:   cfg.Name,
		Status: ioutil.Discard,
	}

	if cfg.Root.OCI != "" {
		imgref, err := name.ParseReference(cfg.Root.OCI)
		if err != nil {
			return err
		}

		info.Img = imgref
	}

	bundlePath := filepath.Join(basePath, info.Name)

	err := g.ociUnpacker(&info)
	if err != nil {
		return err
	}

	info.StartCh = started

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		errorer <- g.StartContainer(ctx, &info, id)
	}()

	select {
	case <-ctx.Done():
		// Wait for the sub-goroutine to finish
		<-errorer
		return ctx.Err()
	case err := <-errorer:
		g.L.Error("error booting container", "error", err)
		return err
	case <-started:
		// ok
	}

	g.L.Info("container for app started, running app")
	err = g.runApp(ctx, bundlePath, info, cfg, id)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			g.L.Error("error running app", "error", err)
		}
	}

	cancel()

	// We cancel the context so we should be able to safely do this to
	// wait for the StartContainer call to return.

	return <-errorer
}

func (g *Guest) runApp(ctx context.Context, bundlePath string, info ContainerInfo, cfg *shardconfig.Config, id string) error {
	r := runc.Runc{
		Debug: true,
	}

	started := make(chan int, 1)

	tmpdir, err := ioutil.TempDir("", "yalrm4")
	if err != nil {
		return err
	}

	defer os.RemoveAll(tmpdir)

	pidPath := filepath.Join(tmpdir, "pid")

	dw, err := clog.NewDirectoryWriter(filepath.Join(bundlePath, "log"), 0, 0)
	if err != nil {
		return err
	}

	defer dw.Close()

	appData, err := dw.IOInput(ctx)
	if err != nil {
		return err
	}

	pio, err := runc.SetOutputIO(appData)
	if err != nil {
		return err
	}

	sp := &specs.Process{
		Env: []string{
			"PATH=/bin:/usr/bin:/sbin:/usr/sbin:/usr/local/bin:/usr/local/sbin:/run/share/bin:/run/share/sbin:/opt/isle/bin",
			"SSH_AUTH_SOCK=/tmp/ssh-agent.sock",
		},
	}

	root := append([]string{}, info.OCIConfig.Entrypoint...)

	sp.Args = append(root, cfg.Service[0].Command...)
	sp.Cwd = "/"

	ch := g.reaper.Subscribe()
	defer g.reaper.Unsubscribe(ch)

	errCh := make(chan error)

	err = r.Exec(ctx, id, *sp, &runc.ExecOpts{
		IO:      pio,
		Detach:  true,
		Started: started,
		PidFile: pidPath,
	})

	g.L.Debug("exec finished in detach mode")

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	case <-started:
		// ok
	}

	g.L.Info("reading pid file")

	data, err := ioutil.ReadFile(pidPath)
	if err != nil {
		return err
	}

	processPid, err := strconv.Atoi(string(data))
	if err != nil {
		return err
	}

	g.L.Info("checking advertisement", "count", cfg.Advertisements)

	for _, ad := range cfg.Advertisements {
		for _, rp := range ad.RunPaths {
			name := rp.Name

			// We change the permissions of the file so that it can be used
			// by a non-root user in another container (this is typical for
			// when a service like docker runs as root and the docker.sock
			// is thusly owned by root but we want it to be usable by other
			// containers as non-root

			for i := 0; i < 100; i++ {
				err = os.Chown(filepath.Join("/run", id, name), 501, 1000)
				if err == nil {
					break
				}
				if os.IsNotExist(err) {
					g.L.Info("/run file advertiments missing, sleeping and retrying", "name", name)
					time.Sleep(250 * time.Millisecond)
				}
			}

			g.L.Info("adding /run advertisement", "name", name)

			id := g.adverts.Add(&AdvertiseRun{
				Source: id,
				Name:   name,
			})

			defer g.adverts.Remove(id)
		}

		for _, gp := range ad.Paths {
			path := filepath.Join("/run", "share", gp.Into, gp.Name)

			if _, err := os.Stat(path); err == nil {
				g.L.Info("unable to advertise to existing path", "path", path)
				continue
			}

			err = os.MkdirAll(filepath.Dir(path), 0755)
			if err != nil {
				g.L.Error("error creating shared dir", "dir", filepath.Base(path))
				continue
			}

			varName := filepath.Join("/var/isle-containers", info.Name, "rootfs", gp.Path)
			g.L.Info("mapping advertised path", "from", varName, "to", path)

			err = os.Symlink(varName, path)
			if err != nil {
				g.L.Error("error symlinking into shared", "name", gp.Name, "path", gp.Path)
				continue
			}

			defer func() {
				err := os.Remove(path)
				if err != nil {
					g.L.Error("error removing shared path", "path", path)
				} else {
					os.RemoveAll(path)
				}
			}()
		}
	}

	var exitStatus runc.Exit

	g.L.Info("waiting on exit status")

loop:
	for {
		select {
		case <-ctx.Done():
			g.L.Info("exit app management due to context closure")
			return ctx.Err()
		case exit := <-ch:
			if exit.Pid == processPid {
				g.L.Info("exit detected", "pid", exit.Pid, "pidfile", processPid)

				exitStatus = exit
				break loop
			}
		}
	}

	code := exitStatus.Status

	g.L.Info("session has exitted", "code", code)

	return nil
}
