package guest

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/pkg/labels"
	"github.com/lab47/isle/pkg/pbstream"
	"github.com/pkg/errors"
	"github.com/samber/do"
)

type ShellManager struct {
	guestapi.UnimplementedGuestAPIServer

	L                 hclog.Logger
	Containers        *ContainerManager
	ConnectionManager *ConnectionManager
	Hub               *SignalHub
	Network           *guestapi.ResourceId

	schema *Schema

	hostDelegate guestapi.HostAPIClient

	dataDir string

	hostBinds map[string]string

	topCtx *ResourceContext
}

func NewShellManager(inj *do.Injector) (*ShellManager, error) {
	log, err := do.Invoke[hclog.Logger](inj)
	if err != nil {
		return nil, err
	}

	cm, err := do.Invoke[*ContainerManager](inj)
	if err != nil {
		return nil, err
	}

	conMan, err := do.Invoke[*ConnectionManager](inj)
	if err != nil {
		return nil, err
	}

	network, err := do.InvokeNamed[*guestapi.ResourceId](inj, "default-network")
	if err != nil {
		return nil, err
	}

	dataDir, err := do.InvokeNamed[string](inj, "data-dir")
	if err != nil {
		return nil, err
	}

	rctx, err := do.Invoke[*ResourceContext](inj)
	if err != nil {
		return nil, err
	}

	topCtx, err := do.InvokeNamed[context.Context](inj, "top")
	if err != nil {
		return nil, err
	}

	// we allow it to not be set.
	hostBinds, _ := do.InvokeNamed[map[string]string](inj, "host-binds")

	shellDataDir := filepath.Join(dataDir, "shell")
	err = os.MkdirAll(shellDataDir, 0755)
	if err != nil {
		return nil, err
	}

	ctx, err := do.Invoke[*ResourceContext](inj)
	if err != nil {
		return nil, err
	}

	sm := &ShellManager{
		L:                 log,
		Containers:        cm,
		ConnectionManager: conMan,
		Network:           network,

		dataDir:   shellDataDir,
		hostBinds: hostBinds,

		topCtx: rctx,
	}

	err = sm.Init(ctx)
	if err != nil {
		return nil, err
	}

	_, handler := guestapi.PBSNewShellAPIHandler(sm)

	listener := conMan.Listen(labels.New("component", "shell"))

	go func() {
		for {
			rs, _, err := listener.Accept(topCtx)
			if err != nil {
				return
			}

			go func() {
				err = handler.HandleRPC(ctx, rs)
				if err != nil {
					log.Error("error handling rpc", "error", err)
				}
			}()

		}
	}()

	return sm, nil
}

func (m *ShellManager) ListShellSessions(
	ctx context.Context, req *pbstream.Request[guestapi.Empty],
) (*pbstream.Response[guestapi.ListShellSessionsResp], error) {
	rctx := m.topCtx.Under(ctx)

	resources, err := rctx.List("shell", "session")
	if err != nil {
		return nil, err
	}

	var resp guestapi.ListShellSessionsResp

	for _, res := range resources {
		raw, err := res.Resource.UnmarshalNew()
		if err != nil {
			return nil, err
		}

		sess, ok := raw.(*guestapi.ShellSession)
		if !ok {
			return nil, errors.Wrapf(ErrInvalidResource, "not a shell session")
		}

		resp.Sessions = append(resp.Sessions, &guestapi.ShellSessionResource{
			Id:              res.Id,
			Session:         sess,
			ProvisionStatus: res.ProvisionStatus,
		})
	}

	return pbstream.NewResponse(&resp), nil
}

func (m *ShellManager) Unwrap(res *guestapi.Resource) (*guestapi.ShellSession, error) {
	raw, err := res.Resource.UnmarshalNew()
	if err != nil {
		return nil, err
	}

	sess, ok := raw.(*guestapi.ShellSession)
	if !ok {
		return nil, errors.Wrapf(ErrInvalidResource, "not a shell session")
	}

	return sess, nil
}

func (m *ShellManager) RemoveShellSession(
	ctx context.Context, req *pbstream.Request[guestapi.RemoveShellSessionReq],
) (*pbstream.Response[guestapi.RemoveShellSessionResp], error) {
	rctx := m.topCtx.Under(ctx)

	res, err := m.Lookup(rctx, req.Value.Name)
	if err != nil {
		return nil, err
	}

	shell, err := m.Unwrap(res)
	if err != nil {
		return nil, err
	}

	err = m.Delete(m.topCtx.Under(ctx), res, shell)
	if err != nil {
		return nil, err
	}

	var resp guestapi.RemoveShellSessionResp

	return pbstream.NewResponse(&resp), nil
}

func (m *ShellManager) Init(ctx *ResourceContext) error {
	s, err := ctx.SetSchema("shell", "session", &guestapi.ShellSession{}, "name")
	if err != nil {
		return err
	}

	m.schema = s

	return m.restartSessions(ctx)
}

func (m *ShellManager) Create(ctx *ResourceContext, sess *guestapi.ShellSession) (*guestapi.Resource, error) {
	setup := []string{
		// We need be sure that /var/run is mapped to /run because
		// we mount /run under the host's /run so that it can be
		// accessed by the host.
		"rm -rf /var/run; ln -s /run /var/run",
		"mkdir -p /etc/sudoers.d",
		"echo '%user ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/00-%user",
		"echo root:root | chpasswd",
		"id %user || useradd -u %uid -m %user || adduser -u %uid -h /home/%user %user",
		"echo %user:%user | chpasswd",
		// "stat /home/%user/%env || ln -sf /share/home /home/%user/%env",
	}

	rpl := strings.NewReplacer(
		"%user", sess.User.Username,
		"%uid", strconv.Itoa(int(sess.User.Uid)),
		"%env", sess.EnvName,
	)

	for i, str := range setup {
		setup[i] = rpl.Replace(str)
	}

	id := m.schema.NewId()

	servicesDir := filepath.Join(m.dataDir, id.Short())
	err := os.MkdirAll(servicesDir, 0755)
	if err != nil {
		return nil, err
	}

	hostSSHPath := filepath.Join(servicesDir, "ssh-agent.sock")

	cont := &guestapi.Container{
		Image:        sess.Image,
		Networks:     []*guestapi.ResourceId{m.Network},
		SetupCommand: []string{"/bin/sh", "-c", strings.Join(setup, "; ")},
		User:         sess.User,
		Hostname:     sess.Name,
		StableId:     id.Short(),
		PortForward:  sess.PortForward,
		Binds: []*guestapi.Bind{
			{
				HostPath:      servicesDir,
				Options:       []string{"rbind", "rw"},
				ContainerPath: "/run/services",
			},
		},
	}

	for hostPath, contPath := range m.hostBinds {
		contPath := rpl.Replace(contPath)

		cont.Binds = append(cont.Binds, &guestapi.Bind{
			HostPath:      hostPath,
			Options:       []string{"rbind", "rw"},
			ContainerPath: contPath,
		})
	}

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
	go m.startSSHAgent(hostSSHPath, sess.User)

	return shellRes, nil
}

func (m *ShellManager) Update(ctx *ResourceContext, res *guestapi.Resource) (*guestapi.Resource, error) {
	return nil, ErrImmutable
}

func (m *ShellManager) Read(ctx *ResourceContext, id *guestapi.ResourceId) (*guestapi.Resource, error) {
	res, err := ctx.Fetch(id)
	if err != nil {
		return nil, err
	}

	m.L.Debug("reading shell session", "id", id.Short(), "status", res.ProvisionStatus.Status.String())

	return res, nil
}

func (m *ShellManager) Delete(ctx *ResourceContext, res *guestapi.Resource, sess *guestapi.ShellSession) error {
	res.ProvisionStatus.Status = guestapi.ProvisionStatus_DEAD
	err := ctx.UpdateProvision(ctx, res.Id, res.ProvisionStatus)
	if err != nil {
		return err
	}

	contId := res.ProvisionStatus.ContainerRef

	var cont *guestapi.Container

	contRes, err := m.Containers.Read(ctx, contId)
	if err != nil {
		m.L.Warn("container resource is missing")
	} else {
		cont, err = m.Containers.Unwrap(contRes)
		if err != nil {
			m.L.Warn("container resource is corrupted")
		}
	}

	if cont != nil {
		err = m.Containers.Delete(ctx, contRes, cont)
		if err != nil {
			m.L.Warn("error deleting container resource", "error", err)
		} else {
			m.L.Info("deleted container due to shell deletion", "id", contRes.Id.Short())
		}
	}

	_, err = ctx.Delete(ctx, res.Id)
	return err
}

func (m *ShellManager) restartSessions(ctx *ResourceContext) error {
	resources, err := ctx.List("shell", "session")
	if err != nil {
		return err
	}

	for _, res := range resources {
		raw, err := res.Resource.UnmarshalNew()
		if err != nil {
			return err
		}

		sess, ok := raw.(*guestapi.ShellSession)
		if !ok {
			return errors.Wrapf(ErrInvalidResource, "not a shell session")
		}

		m.L.Info("attempting to restart shell session", "container", res.Id.Short())

		err = m.restartSession(ctx, res, sess)
		if err != nil {
			return err
		}
	}

	return nil
}

func (m *ShellManager) restartSession(ctx *ResourceContext, res *guestapi.Resource, sess *guestapi.ShellSession) error {
	m.L.Info("restarting shell services", "id", res.Id.Name())

	servicesDir := filepath.Join(m.dataDir, res.Id.Short())
	err := os.MkdirAll(servicesDir, 0755)
	if err != nil {
		m.L.Error("error setting up tmpdir", "error", err)
		return err
	}

	hostSSHPath := filepath.Join(servicesDir, "ssh-agent.sock")
	go m.startSSHAgent(hostSSHPath, sess.User)

	return nil
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

	if len(resources) == 0 {
		return nil, nil
	}

	res := resources[0]

	return res, nil
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

			m.L.Info("processing change", "id", cont.Short(), "details", status.StatusDetails)

			switch status.Status {
			case guestapi.ProvisionStatus_ADDING:
				ctx.UpdateProvision(ctx, shell.Id, &guestapi.ProvisionStatus{
					StatusDetails: status.StatusDetails,
				})

				// ok, keep waiting
			case guestapi.ProvisionStatus_DEAD, guestapi.ProvisionStatus_SUSPENDED:
				m.L.Error("container died for shell during provisioning")
				err := ctx.UpdateProvision(ctx, shell.Id, &guestapi.ProvisionStatus{
					Status:    guestapi.ProvisionStatus_DEAD,
					LastError: status.LastError,
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

func (m *ShellManager) startSSHAgent(path string, user *guestapi.User) {
	os.Remove(path)

	agentListener, err := net.Listen("unix", path)
	if err != nil {
		m.L.Error("error listening on ssh agent path", "error", err, "path", path)
		return
	}

	err = os.Chown(path, int(user.Uid), int(user.Gid))
	if err != nil {
		m.L.Error("unable to chown ssh-agent", "error", err)
	}

	targetSel := labels.New("ssh-agent", user.Username)

	m.L.Info("starting ssh forwarding gateway", "host-path", path, "target-sel", targetSel.String())

	for {
		local, err := agentListener.Accept()
		if err != nil {
			return
		}

		agent, remote, err := m.ConnectionManager.Open(targetSel)
		if err != nil {
			local.Close()
			m.L.Error("unabel to connect to ssh-agent", "error", err, "user", user)
			continue
		}

		remote, err = agent.Hijack(remote)
		if err != nil {
			local.Close()
			m.L.Error("error hijacking pbstream", "error", err)
			continue
		}

		m.L.Debug("established connection to ssh-agent")

		go func() {
			defer local.Close()
			defer remote.Close()

			io.Copy(remote, local)
		}()

		go func() {
			defer local.Close()
			defer remote.Close()

			io.Copy(local, remote)
		}()
	}
}

func init() {
	DefaultRegistry.Register(
		&guestapi.Container{},
		TypedManager[*guestapi.ShellSession](&ShellManager{}),
	)
}
