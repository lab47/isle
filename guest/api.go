package guest

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/pkg/locator"
)

func (g *Guest) startAPI(ctx context.Context) {
	server := &http.Server{
		Addr:    "0.0.0.0:1212",
		Handler: guestapi.NewGuestAPIServer(g),
		BaseContext: func(l net.Listener) context.Context {
			return ctx
		},
	}

	g.L.Info("starting api listener", "addr", server.Addr)

	err := server.ListenAndServe()
	if err != nil {
		g.L.Error("error in api listener", "error", err)
	}
}

func (g *Guest) AddApp(ctx context.Context, req *guestapi.AddAppReq) (*guestapi.AddAppResp, error) {
	if req.Name == "" || req.Selector == "" {
		return nil, fmt.Errorf("invalid request, require name and selector")
	}

	oldName := filepath.Join("/share/home/linux-apps", req.Name)
	if _, err := os.Stat(oldName); err == nil {
		err := g.validateApp(ctx, oldName)
		if err != nil {
			return nil, err
		}

		err = os.Symlink(oldName, filepath.Join("/data/apps/running", req.Name))
		if err != nil {
			return nil, err
		}

		var resp guestapi.AddAppResp
		return &resp, nil
	}

	cfg, data, err := locator.Fetch(ctx, req.Selector)
	if err != nil {
		return nil, err
	}

	availDir := filepath.Join("/data/apps/available", cfg.Name)

	err = os.MkdirAll(availDir, 0755)
	if err != nil {
		return nil, err
	}

	f, err := os.Create(filepath.Join(availDir, "app.hcl"))
	if err != nil {
		return nil, err
	}

	f.Write(data)

	err = f.Close()
	if err != nil {
		return nil, err
	}

	err = os.Symlink(availDir, filepath.Join("/data/apps/running", cfg.Name))
	if err != nil {
		return nil, err
	}

	var resp guestapi.AddAppResp
	return &resp, nil
}

func (g *Guest) DisableApp(ctx context.Context, req *guestapi.DisableAppReq) (*guestapi.DisableAppResp, error) {
	path := filepath.Join("/data/apps/running", req.Id)

	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("unknown app: %s", req.Id)
	}

	err := os.Remove(path)
	if err != nil {
		return nil, err
	}

	var resp guestapi.DisableAppResp
	return &resp, nil
}
