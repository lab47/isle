package guest

import (
	"context"
	"os"
	"os/exec"

	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/pkg/labels"
	"github.com/lab47/isle/pkg/pbstream"
	"github.com/samber/do"
)

type VMApi struct {
	log     hclog.Logger
	handler pbstream.Handler
}

func NewVMApi(inj *do.Injector) (*VMApi, error) {
	log, err := do.Invoke[hclog.Logger](inj)
	if err != nil {
		return nil, err
	}

	api := &VMApi{
		log: log,
	}

	_, api.handler = guestapi.PBSNewVMAPIHandler(api)

	return api, nil
}

func (a *VMApi) Listen(conMan *ConnectionManager, ctx context.Context) error {
	listener := conMan.Listen(labels.New("component", "vm"))
	defer listener.Close()

	for {
		rs, _, err := listener.Accept(ctx)
		if err != nil {
			return err
		}

		go func() {
			err = a.handler.HandleRPC(ctx, rs)
			if err != nil {
				a.log.Error("error handling rpc", "error", err)
			}
		}()
	}
}

func (a *VMApi) RequestShutdown(context.Context, *pbstream.Request[guestapi.Empty]) (*pbstream.Response[guestapi.Empty], error) {
	cmd := exec.Command("halt")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return nil, cmd.Run()
}
