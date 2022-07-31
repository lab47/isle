package guest

import (
	"context"

	"github.com/lab47/isle/guestapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ResourceAPI struct {
	rctx *ResourceContext
}

func (r *ResourceAPI) resourceContext(ctx context.Context) *ResourceContext {
	return r.rctx.Under(ctx)
}

func (r *ResourceAPI) Create(ctx context.Context, req *guestapi.CreateResourceReq) (*guestapi.CreateResourceResp, error) {
	m, err := req.Resource.UnmarshalNew()
	if err != nil {
		return nil, err
	}

	mgr, ok := DefaultRegistry.Manager(m)
	if !ok {
		return nil, status.Error(codes.NotFound, "no manager for type")
	}

	resource, err := mgr.Create(r.resourceContext(ctx), m)
	if err != nil {
		return nil, err
	}

	var resp guestapi.CreateResourceResp
	resp.Resource = resource

	return &resp, nil
}

func (r *ResourceAPI) Update(ctx context.Context, req *guestapi.UpdateResourceReq) (*guestapi.UpdateResourceResp, error) {
	m, err := req.Resource.Resource.UnmarshalNew()
	if err != nil {
		return nil, err
	}

	mgr, ok := DefaultRegistry.Manager(m)
	if !ok {
		return nil, status.Error(codes.NotFound, "no manager for type")
	}

	resource, err := mgr.Update(r.resourceContext(ctx), req.Resource)
	if err != nil {
		return nil, err
	}

	var resp guestapi.UpdateResourceResp
	resp.Resource = resource

	return &resp, nil
}

func (r *ResourceAPI) Read(ctx context.Context, req *guestapi.ReadResourceReq) (*guestapi.ReadResourceResp, error) {
	rctx := r.resourceContext(ctx)

	res, err := rctx.Fetch(req.Id)
	if err != nil {
		return nil, err
	}

	var resp guestapi.ReadResourceResp
	resp.Resource = res
	return &resp, nil
}

func (r *ResourceAPI) CheckProvision(ctx context.Context, req *guestapi.CheckProvisionReq) (*guestapi.CheckProvisionResp, error) {
	rctx := r.resourceContext(ctx)

	res, err := rctx.Fetch(req.Id)
	if err != nil {
		return nil, err
	}

	var resp guestapi.CheckProvisionResp
	resp.Status = res.ProvisionStatus

	return &resp, nil
}

func (r *ResourceAPI) List(ctx context.Context, req *guestapi.ListResourcesReq) (*guestapi.ListResourcesResp, error) {
	rctx := r.resourceContext(ctx)

	switch crit := req.Criteria.(type) {
	case *guestapi.ListResourcesReq_Type_:
		ids, err := rctx.ListIds(crit.Type.Category, crit.Type.Type)
		if err != nil {
			return nil, err
		}

		return &guestapi.ListResourcesResp{
			Ids: ids,
		}, nil
	}

	return nil, nil
}

func (r *ResourceAPI) Delete(ctx context.Context, req *guestapi.DeleteResourceReq) (*guestapi.DeleteResourceResp, error) {
	rctx := r.resourceContext(ctx)

	res, err := rctx.Delete(ctx, req.Id)
	if err != nil {
		return nil, err
	}

	var resp guestapi.DeleteResourceResp
	resp.Resource = res
	return &resp, nil
}
