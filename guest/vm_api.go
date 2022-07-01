package guest

import (
	"context"

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

func (a *VMApi) SyncTime(context.Context, *pbstream.Request[guestapi.SyncTimeReq]) (*pbstream.Response[guestapi.SyncTimeResp], error) {
	return nil, nil
}

func (a *VMApi) VMInfo(context.Context, *pbstream.Request[guestapi.VMInfoReq]) (*pbstream.Response[guestapi.VMInfoResp], error) {
	return nil, nil
}
