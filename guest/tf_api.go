package guest

import (
	"context"

	"github.com/lab47/isle/guestapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (g *Guest) resourceContext(ctx context.Context) *ResourceContext {
	return &ResourceContext{
		Context: ctx,
		ResourceStorage: &ResourceStorage{
			db: g.db,
		},
	}
}

func (g *Guest) Create(ctx context.Context, req *guestapi.CreateResourceReq) (*guestapi.CreateResourceResp, error) {
	m, err := req.Resource.UnmarshalNew()
	if err != nil {
		return nil, err
	}

	mgr, ok := DefaultRegistry.Manager(m)
	if !ok {
		return nil, status.Error(codes.NotFound, "no manager for type")
	}

	resource, err := mgr.Create(g.resourceContext(ctx), m)
	if err != nil {
		return nil, err
	}

	var resp guestapi.CreateResourceResp
	resp.Resource = resource

	return &resp, nil
}

func (g *Guest) Update(ctx context.Context, req *guestapi.UpdateResourceReq) (*guestapi.UpdateResourceResp, error) {
	m, err := req.Resource.Resource.UnmarshalNew()
	if err != nil {
		return nil, err
	}

	mgr, ok := DefaultRegistry.Manager(m)
	if !ok {
		return nil, status.Error(codes.NotFound, "no manager for type")
	}

	resource, err := mgr.Update(g.resourceContext(ctx), req.Resource)
	if err != nil {
		return nil, err
	}

	var resp guestapi.UpdateResourceResp
	resp.Resource = resource

	return &resp, nil
}

func (g *Guest) Read(ctx context.Context, req *guestapi.ReadResourceReq) (*guestapi.ReadResourceResp, error) {
	rctx := g.resourceContext(ctx)

	res, err := rctx.Fetch(req.Id)
	if err != nil {
		return nil, err
	}

	var resp guestapi.ReadResourceResp
	resp.Resource = res
	return &resp, nil
}

func (g *Guest) CheckProvision(ctx context.Context, req *guestapi.CheckProvisionReq) (*guestapi.CheckProvisionResp, error) {
	rctx := g.resourceContext(ctx)

	res, err := rctx.Fetch(req.Id)
	if err != nil {
		return nil, err
	}

	var resp guestapi.CheckProvisionResp
	resp.Status = res.ProvisionStatus

	return &resp, nil
}

func (g *Guest) Delete(ctx context.Context, req *guestapi.DeleteResourceReq) (*guestapi.DeleteResourceResp, error) {
	rctx := g.resourceContext(ctx)

	res, err := rctx.Delete(ctx, req.Id)
	if err != nil {
		return nil, err
	}

	var resp guestapi.DeleteResourceResp
	resp.Resource = res
	return &resp, nil
}

func (g *Guest) mustEmbedUnimplementedResourceAPIServer() {
	panic("not implemented") // TODO: Implement
}
