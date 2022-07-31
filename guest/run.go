package guest

import (
	"context"
	"net"

	"github.com/hashicorp/go-hclog"
	"github.com/samber/do"
)

type RunEventType int

const (
	Running RunEventType = iota
)

type RunEvent struct {
	Type RunEventType
}

type RunConfig struct {
	Logger     hclog.Logger
	DataDir    string
	HostBinds  map[string]string
	Listener   net.Listener
	ClusterId  string
	EventsCh   chan RunEvent
	HelperPath string
}

func Run(ctx context.Context, cfg *RunConfig) error {
	inj := do.New()

	defer func() {
		cfg.Logger.Info("performing component shutdown")
		inj.Shutdown()
	}()

	do.ProvideNamedValue(inj, "top", ctx)
	do.ProvideValue(inj, cfg.Logger)
	do.ProvideNamedValue(inj, "data-dir", cfg.DataDir)
	do.ProvideNamedValue(inj, "host-binds", cfg.HostBinds)
	do.ProvideNamedValue(inj, "cluster-id", cfg.ClusterId)
	do.ProvideNamedValue(inj, "helper-path", cfg.HelperPath)
	do.Provide(inj, OpenDB)
	do.Provide(inj, NewResourceContext)
	do.Provide(inj, NewResourceStorage)
	do.Provide(inj, BootstrapIPM)
	do.Provide(inj, NewConnectionManager)
	do.Provide(inj, NewShellLauncher)
	do.Provide(inj, NewShellManager)
	do.Provide(inj, NewContainerManager)
	do.Provide(inj, NewVMApi)

	cm, err := do.Invoke[*ConnectionManager](inj)
	if err != nil {
		return err
	}

	shl, err := do.Invoke[*ShellLauncher](inj)
	if err != nil {
		return err
	}

	vmapi, err := do.Invoke[*VMApi](inj)
	if err != nil {
		return err
	}

	go vmapi.Listen(cm, ctx)

	cm.SessionHandler = shl

	if cfg.EventsCh != nil {
		go func() {
			cfg.EventsCh <- RunEvent{
				Type: Running,
			}
		}()
	}

	return cm.Serve(ctx, cfg.Listener)
}
