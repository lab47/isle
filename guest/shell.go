package guest

import (
	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/guestapi"
)

type ShellManager struct {
	L          hclog.Logger
	Containers *ContainerManager
	Hub        *SignalHub
	Network    *guestapi.ResourceId

	schema *Schema
}

func (m *ShellManager) Init(ctx *ResourceContext) error {
	s, err := ctx.SetSchema("shell", "session", &guestapi.ShellSession{}, "name")
	if err != nil {
		return err
	}

	m.schema = s

	return nil
}

func (m *ShellManager) Create(ctx *ResourceContext, sess *guestapi.ShellSession) (*guestapi.Resource, error) {
	cont := &guestapi.Container{
		Image:    sess.Image,
		Networks: []*guestapi.ResourceId{m.Network},
	}

	id := m.schema.NewId()

	res, err := m.Containers.Create(ctx, cont)
	if err != nil {
		return nil, err
	}

	shellRes, err := ctx.Set(ctx, id, sess, &guestapi.ProvisionStatus{
		ContainerRef: res.Id,
	})
	if err != nil {
		return nil, err
	}

	go m.waitContainer(ctx, res.Id, shellRes)

	return shellRes, nil
}

func (m *ShellManager) Lookup(ctx *ResourceContext, name string) (*guestapi.Resource, error) {
	key, err := m.schema.Key("name", name)
	if err != nil {
		return nil, err
	}

	resources, err := ctx.FetchByIndex(key)
	if err != nil {
		return nil, err
	}

	if len(resources) != 0 {
		return resources[0], nil
	}

	return nil, nil
}

func (m *ShellManager) waitContainer(ctx *ResourceContext, cont *guestapi.ResourceId, shell *guestapi.Resource) {
	sel := ctx.ProvisionChangeSelector()

	ch := sel.Register(cont.Short())
	defer sel.Unregister(ch)

loop:
	for {
		select {
		case <-ctx.Done():
			return
		case provCha := <-ch:
			status := provCha.Status

			switch status.Status {
			case guestapi.ProvisionStatus_ADDING:
				// ok, keep waiting
			case guestapi.ProvisionStatus_DEAD, guestapi.ProvisionStatus_SUSPENDED:
				m.L.Error("container died for shell during provisioning")
				err := ctx.UpdateProvision(ctx, shell.Id, &guestapi.ProvisionStatus{
					Status: guestapi.ProvisionStatus_DEAD,
				})

				if err != nil {
					m.L.Error("error updating shell provision status", "error", err)
				}

				return
			case guestapi.ProvisionStatus_RUNNING:
				break loop
			}
		}
	}

	err := ctx.UpdateProvision(ctx, shell.Id, &guestapi.ProvisionStatus{
		Status:       guestapi.ProvisionStatus_RUNNING,
		ContainerRef: cont,
	})

	if err != nil {
		m.L.Error("error updating shell provision status", "error", err)
	}
}
