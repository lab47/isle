package guest

import (
	"context"

	"github.com/lab47/isle/guestapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type GuestAPI struct {
	ConnectionManager *ConnectionManager
}

func (g *GuestAPI) AddApp(ctx context.Context, req *guestapi.AddAppReq) (*guestapi.AddAppResp, error) {
	return nil, status.New(codes.Unimplemented, "add-app to be removed").Err()
}

func (g *GuestAPI) DisableApp(ctx context.Context, req *guestapi.DisableAppReq) (*guestapi.DisableAppResp, error) {
	return nil, status.New(codes.Unimplemented, "add-app to be removed").Err()
}

func (g *GuestAPI) hostAPI(ctx context.Context) (guestapi.HostAPIClient, error) {
	cc, err := grpc.DialContext(ctx, "host", grpc.WithContextDialer(g.ConnectionManager.GRPCDial))
	if err != nil {
		return nil, err
	}

	return guestapi.NewHostAPIClient(cc), nil
}

func (g *GuestAPI) RunOnMac(s guestapi.GuestAPI_RunOnMacServer) error {
	hostApi, err := g.hostAPI(s.Context())

	c, err := hostApi.RunOnMac(s.Context())
	if err != nil {
		return err
	}

	go func() {
		for {
			m, err := c.Recv()
			if err != nil {
				return
			}

			err = s.Send(m)
			if err != nil {
				return
			}
		}
	}()

	for {
		m, err := s.Recv()
		if err != nil {
			return err
		}

		err = c.Send(m)
		if err != nil {
			return err
		}
	}
}

func (g *GuestAPI) TrimMemory(ctx context.Context, req *guestapi.TrimMemoryReq) (*guestapi.TrimMemoryResp, error) {
	hostApi, err := g.hostAPI(ctx)
	if err != nil {
		return nil, err
	}

	return hostApi.TrimMemory(ctx, req)
}
