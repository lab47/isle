package guest

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/davecgh/go-spew/spew"
	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/pkg/labels"
	"github.com/pkg/errors"
	"github.com/samber/do"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	}

	err = sm.Init(ctx)
	if err != nil {
		return nil, err
	}

	return sm, nil
}

func (m *ShellManager) Init(ctx *ResourceContext) error {
	s, err := ctx.SetSchema("shell", "session", &guestapi.ShellSession{}, "name")
	if err != nil {
		return err
	}

	m.schema = s

	/*
		serv := grpc.NewServer()

		guestapi.RegisterGuestAPIServer(serv, m)

		m.L.Info("starting api listener")

		li, err := net.Listen("tcp", "0.0.0.0:1212")
		if err != nil {
			return err
		}

		go serv.Serve(li)
	*/

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

	spew.Dump(cont.Binds)

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

func (g *ShellManager) AddApp(ctx context.Context, req *guestapi.AddAppReq) (*guestapi.AddAppResp, error) {
	return nil, status.New(codes.Unimplemented, "add-app to be removed").Err()
}

func (g *ShellManager) DisableApp(ctx context.Context, req *guestapi.DisableAppReq) (*guestapi.DisableAppResp, error) {
	return nil, status.New(codes.Unimplemented, "add-app to be removed").Err()
}

func (g *ShellManager) RunOnMac(s guestapi.GuestAPI_RunOnMacServer) error {
	if g.hostDelegate == nil {
		return RunCommand(s)
	}

	c, err := g.hostDelegate.RunOnMac(s.Context())
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

func (g *ShellManager) TrimMemory(ctx context.Context, req *guestapi.TrimMemoryReq) (*guestapi.TrimMemoryResp, error) {
	if g.hostDelegate == nil {
		return nil, status.New(codes.Unimplemented, "no access no host for memory trimming").Err()
	}

	return g.hostDelegate.TrimMemory(ctx, req)
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
