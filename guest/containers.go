package guest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/containerd/console"
	"github.com/containerd/containerd/archive"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/yamux"
	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/pkg/bridge"
	"github.com/lab47/isle/pkg/clog"
	"github.com/lab47/isle/pkg/pbstream"
	"github.com/lab47/isle/pkg/progressbar"
	"github.com/lab47/isle/pkg/reaper"
	"github.com/lab47/isle/pkg/runc"
	"github.com/mr-tron/base58"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/samber/do"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ContainerManager struct {
	L hclog.Logger

	cfg ContainerConfig

	nodeId       string
	hostAddr     string
	sshAgentPath string

	currentSession *yamux.Session

	hostAPI guestapi.HostAPIClient
	adverts Advertisements

	bgCtx    context.Context
	bgCancel func()

	reaper *reaper.Monitor

	containerSchema *Schema

	track containerTracker

	runningMu sync.Mutex
	running   map[string]func()

	conMan *ConnectionManager
}

type containerTracker struct {
	mu   sync.Mutex
	cond *sync.Cond

	running int
}

func (c *containerTracker) Add() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cond == nil {
		c.cond = sync.NewCond(&c.mu)
	}

	c.running++
}

func (c *containerTracker) Done() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.running--
	c.cond.Broadcast()
}

func (c *containerTracker) Wait() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for c.running > 0 {
		c.cond.Wait()
	}
}

const DefaultHelperPath = "/usr/bin/isle-helper"

type ContainerConfig struct {
	Logger  hclog.Logger
	BaseDir string
	HomeDir string
	NodeId  string
	User    string
	// ClusterId string
	// SubnetId  string
	RuncRoot string
	RunDir   string

	BridgeID int

	LayerCacheDir string

	HelperPath  string
	IsleBinPath string

	NetworkManager *IPNetworkManager
}

func NewContainerManager(inj *do.Injector) (*ContainerManager, error) {
	log, err := do.Invoke[hclog.Logger](inj)
	if err != nil {
		return nil, err
	}

	ipm, err := do.Invoke[*IPNetworkManager](inj)
	if err != nil {
		return nil, err
	}

	rctx, err := do.Invoke[*ResourceContext](inj)
	if err != nil {
		return nil, err
	}

	dataDir, err := do.InvokeNamed[string](inj, "data-dir")
	if err != nil {
		return nil, err
	}

	conMan, err := do.Invoke[*ConnectionManager](inj)
	if err != nil {
		return nil, err
	}

	nodeId, _ := do.InvokeNamed[string](inj, "node-id")
	if nodeId == "" {
		nodeId = NewUniqueId()
	}

	var cm ContainerManager
	cm.conMan = conMan

	os.MkdirAll("/run/isle", 0755)

	runRoot := filepath.Join(dataDir, "runc")
	err = os.MkdirAll(runRoot, 0755)
	if err != nil {
		return nil, err
	}

	helperPath, _ := do.InvokeNamed[string](inj, "helper-path")
	if helperPath == "" {
		helperPath = DefaultHelperPath
	}

	err = cm.Init(rctx, &ContainerConfig{
		Logger:   log,
		BaseDir:  filepath.Join(dataDir, "containers"),
		HomeDir:  filepath.Join(dataDir, "volumes"),
		NodeId:   nodeId,
		RuncRoot: runRoot,
		RunDir:   "/run/isle",

		BridgeID: 0,

		HelperPath:     helperPath,
		NetworkManager: ipm,
	})
	if err != nil {
		return nil, err
	}

	return &cm, nil
}

func (m *ContainerManager) Init(ctx *ResourceContext, cfg *ContainerConfig) error {
	s, err := ctx.SetSchema("container", "container", &guestapi.Container{}, "stable_name", "image")
	if err != nil {
		return err
	}

	m.running = make(map[string]func())

	m.cfg = *cfg

	m.containerSchema = s

	m.L = cfg.Logger

	ip := make(net.IP, net.IPv6len)
	ip[0] = 0xfd

	bgCtx, bgCancel := context.WithCancel(ctx)
	m.bgCtx = bgCtx
	m.bgCancel = bgCancel

	err = reaper.SetSubreaper(1)
	if err != nil {
		return err
	}

	m.reaper = reaper.Default

	sigc := make(chan os.Signal, 128)
	signal.Notify(sigc, unix.SIGCHLD)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-sigc:
				err := reaper.Reap()
				if err != nil {
					m.L.Error("error reaping child processes", "error", err)
				}
			}
		}
	}()

	m.sshAgentPath = filepath.Join(m.cfg.RunDir, "ssh-agent-host.sock")

	m.L.Info("starting DNS")
	go StartDNS(ctx, m.L)

	m.L.Info("restarting any stopped containers")
	return m.restartContainers(ctx)
}

func (m *ContainerManager) Close() error {
	m.bgCancel()
	m.track.Wait()
	return nil
}

func (m *ContainerManager) Shutdown() error {
	return m.Close()
}

func (m *ContainerManager) restartContainers(ctx *ResourceContext) error {
	sel := ctx.ProvisionChangeSelector()

	resources, err := ctx.List("container", "container")
	if err != nil {
		return err
	}

	for _, res := range resources {
		cont, err := m.Unwrap(res)
		if err != nil {
			return err
		}

		m.L.Info("attempting to restart container", "container", res.Id.Short())

		err = ctx.UpdateProvision(ctx, res.Id, &guestapi.ProvisionStatus{
			Status: guestapi.ProvisionStatus_ADDING,
		})
		if err != nil {
			return err
		}

		err = m.restartContainer(ctx, res, cont, sel)
		if err != nil {
			return err
		}
	}

	return nil
}

func (m *ContainerManager) waitOnContainer(ctx *ResourceContext, start bool, ch chan ProvisionChange) (*guestapi.ProvisionStatus, error) {
loop:
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case provCha := <-ch:
			status := provCha.Status

			m.L.Trace("container status", "status", status.Status.String())

			switch status.Status {
			case guestapi.ProvisionStatus_ADDING:
				// ok, keep waiting
			case guestapi.ProvisionStatus_DEAD, guestapi.ProvisionStatus_SUSPENDED, guestapi.ProvisionStatus_STOPPED:
				return status, errors.Wrapf(ErrInvalidResource, "container died rather than start")
			case guestapi.ProvisionStatus_RUNNING:
				if start {
					return status, nil
				}
			default:
				break loop
			}
		}
	}

	return nil, nil
}

func (m *ContainerManager) restartContainer(
	ctx *ResourceContext,
	res *guestapi.Resource,
	cont *guestapi.Container,
	sel *Signal[ProvisionChange],
) error {
	ch := sel.Register(res.Id.Short())
	defer sel.Unregister(ch)

	imgref, err := name.ParseReference(cont.Image)
	if err != nil {
		return err
	}

	id := res.Id

	m.L.Info("restarting stopped container", "id", id.Short())

	m.track.Add()

	go func() {
		defer m.track.Done()

		goctx, cancel := context.WithCancel(m.bgCtx)

		m.runningMu.Lock()
		m.running[id.Short()] = cancel
		m.runningMu.Unlock()

		defer func() {
			m.runningMu.Lock()
			delete(m.running, id.Short())
			m.runningMu.Unlock()
		}()

		sub := &ResourceContext{
			Context:         goctx,
			ResourceStorage: ctx.ResourceStorage,
		}

		err := m.startContainer(sub, imgref, id, nil, 0, cont)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				ctx.UpdateProvision(ctx, id, &guestapi.ProvisionStatus{
					Status: guestapi.ProvisionStatus_STOPPED,
				})
			} else {
				m.L.Error("error creating container", "error", err, "id", id.Short())
				err = ctx.SetError(ctx, id, err)
				if err != nil {
					m.L.Error("error recording error on container", "error", err, "id", id.UniqueId)
				}
			}
		}
	}()

	_, err = m.waitOnContainer(ctx, true, ch)
	return err
}

func (m *ContainerManager) Create(ctx *ResourceContext, cont *guestapi.Container) (*guestapi.Resource, error) {
	if cont.StableName != "" {
		key, err := m.containerSchema.Key("stable_name", cont.StableName)
		if err != nil {
			return nil, err
		}

		resources, err := ctx.FetchByIndex(key)
		if err == nil {
			return nil, err
		}

		var valid int

		// allow duplicate stable names when one is dead.
		for _, r := range resources {
			switch r.ProvisionStatus.Status {
			case guestapi.ProvisionStatus_ADDING, guestapi.ProvisionStatus_RUNNING:
				valid++
			}
		}

		if valid > 0 {
			return nil, errors.Wrapf(ErrConflict, "container with name already exist: %s", cont.StableName)
		}
	}

	id := m.containerSchema.NewId()

	imgref, err := name.ParseReference(cont.Image)
	if err != nil {
		return nil, err
	}

	m.track.Add()

	go func() {
		defer m.track.Done()

		goctx, cancel := context.WithCancel(m.bgCtx)

		m.runningMu.Lock()
		m.running[id.Short()] = cancel
		m.runningMu.Unlock()

		defer func() {
			m.runningMu.Lock()
			delete(m.running, id.Short())
			m.runningMu.Unlock()
		}()

		sub := &ResourceContext{
			Context:         goctx,
			ResourceStorage: ctx.ResourceStorage,
		}

		err := m.startContainer(sub, imgref, id, nil, 0, cont)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				ctx.UpdateProvision(ctx, id, &guestapi.ProvisionStatus{
					Status: guestapi.ProvisionStatus_STOPPED,
				})
			} else {
				m.L.Error("error creating container", "error", err, "id", id.Short())
				err = ctx.SetError(ctx, id, err)
				if err != nil {
					m.L.Error("error recording error on container", "error", err, "id", id.UniqueId)
				}
			}
		}
	}()

	prov := &guestapi.ProvisionStatus{
		Status: guestapi.ProvisionStatus_ADDING,
	}

	return ctx.Set(ctx, id, cont, prov)
}

func (m *ContainerManager) Update(ctx *ResourceContext, res *guestapi.Resource) (*guestapi.Resource, error) {
	return nil, ErrImmutable
}

func (m *ContainerManager) Read(ctx *ResourceContext, id *guestapi.ResourceId) (*guestapi.Resource, error) {
	res, err := ctx.Fetch(id)
	if err != nil {
		return nil, err
	}

	m.L.Debug("reading container", "id", id.Short(), "status", res.ProvisionStatus.Status.String())

	sig := ctx.ProvisionChangeSelector()

	ch := sig.Register(id.Short())
	defer sig.Unregister(ch)

	// Block until the container is not in some final state
	if res.ProvisionStatus.Status == guestapi.ProvisionStatus_ADDING {
	loop:
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case change := <-ch:
				m.L.Debug("container provision status change", "status", change.Status.Status)

				switch change.Status.Status {
				case guestapi.ProvisionStatus_ADDING:
					// ok
				default:
					break loop
				}
			}
		}
	}

	return res, nil
}

func (m *ContainerManager) Unwrap(res *guestapi.Resource) (*guestapi.Container, error) {
	raw, err := res.Resource.UnmarshalNew()
	if err != nil {
		return nil, err
	}

	cont, ok := raw.(*guestapi.Container)
	if !ok {
		return nil, errors.Wrapf(ErrInvalidResource, "container type not a container")
	}

	return cont, nil
}

func (m *ContainerManager) Delete(ctx *ResourceContext, res *guestapi.Resource, cont *guestapi.Container) error {
	m.runningMu.Lock()
	cancel, isRunning := m.running[res.Id.Short()]
	m.runningMu.Unlock()

	var status *guestapi.ProvisionStatus

	if !isRunning {
		res.ProvisionStatus.Status = guestapi.ProvisionStatus_DEAD
		err := ctx.UpdateProvision(ctx, res.Id, res.ProvisionStatus)
		if err != nil {
			return err
		}
		status = res.ProvisionStatus
	} else {
		sig := ctx.ProvisionChangeSelector()

		ch := sig.Register(res.Id.Short())
		defer sig.Unregister(ch)

		// We cancel it and let the monitoring goroutine perform the
		// cleanup.
		m.L.Debug("canceling context of running monitor to delete container")
		cancel()

		status, _ = m.waitOnContainer(ctx, false, ch)
	}

	m.L.Info("purging container from disk", "id", res.Id.Short(), "status", status.Status.String())

	return m.purge(ctx, res, cont)
}

func init() {
	DefaultRegistry.Register(
		&guestapi.Container{},
		TypedManager[*guestapi.Container](&ContainerManager{}),
	)
}

type imageCache struct {
	Digest string `json:"digest"`
}

func (m *ContainerManager) unpackOCI(
	ctx context.Context,
	img name.Reference,
	rootFsPath string,
	status io.Writer,
	width int,
) (*v1.Config, error) {
	m.cfg.Logger.Debug("fetching image", "image", img.Name())

	var input v1.Image

	if m.cfg.LayerCacheDir != "" {
		path := filepath.Join(m.cfg.LayerCacheDir, img.String()+".tar.gz")
		tag, err := name.NewTag(img.Name())
		if err != nil {
			return nil, err
		}

		timg, err := tarball.ImageFromPath(path, &tag)
		if err == nil {
			m.L.Debug("using local cache", "path", path)
			input = timg
		} else {
			m.L.Debug("unable to find cache of image, fetching remotely", "error", err)

			rimg, err := remote.Image(img,
				remote.WithPlatform(v1.Platform{
					Architecture: runtime.GOARCH,
					OS:           "linux",
				}),
			)
			if err != nil {
				return nil, err
			}

			err = tarball.WriteToFile(path, img, rimg)
			if err != nil {
				return nil, err
			}

			input, err = tarball.ImageFromPath(path, &tag)
			if err == nil {
				return nil, err
			}
		}
	}

	if input == nil {
		rimg, err := remote.Image(img,
			remote.WithPlatform(v1.Platform{
				Architecture: runtime.GOARCH,
				OS:           "linux",
			}),
		)
		if err != nil {
			return nil, err
		}

		input = rimg
	}

	dig, err := input.Digest()
	if err != nil {
		return nil, err
	}

	m.L.Debug("fetched image", "digest", dig.String())

	cfg, err := input.ConfigFile()
	if err != nil {
		return nil, err
	}

	err = os.MkdirAll(rootFsPath, 0755)
	if err != nil {
		return nil, err
	}

	layers, err := input.Layers()
	if err != nil {
		return nil, err
	}

	var max int64

	for _, l := range layers {
		sz, err := l.Size()
		if err == nil {
			max += sz
		}
	}

	m.L.Debug("calculate image layers", "total-size", max, "layers", len(layers))

	var bar *progressbar.ProgressBar

	if status != nil {
		bar = progressbar.NewOptions64(
			max,
			progressbar.OptionSetDescription("Downloading"),
			progressbar.OptionSetWriter(status),
			progressbar.OptionShowBytes(true),
			progressbar.OptionSetWidth(10),
			progressbar.OptionSetTerminalWidth(width),
			progressbar.OptionFullWidth(),
			progressbar.OptionThrottle(65*time.Millisecond),
			progressbar.OptionShowCount(),
			progressbar.OptionSpinnerType(14),
			progressbar.OptionUseANSICodes(true),
		)
		bar.RenderBlank()
	}

	for i, l := range layers {
		err = func() error {
			if m.cfg.LayerCacheDir != "" {
				h, err := l.Digest()
				if err != nil {
					return err
				}

				cachePath := filepath.Join(m.cfg.LayerCacheDir, h.String())

				f, err := os.Open(cachePath)
				if err == nil {
					defer f.Close()

					_, err = archive.Apply(ctx, rootFsPath, f)
					if err == nil {
						return nil
					}
				}

				r, err := l.Uncompressed()
				if err != nil {
					return err
				}

				defer r.Close()

				var target io.Reader = r

				cf, err := os.Create(cachePath)
				if err != nil {
					return err
				}

				defer cf.Close()

				if bar != nil {
					target = io.TeeReader(target, io.MultiWriter(bar, cf))
				} else {
					target = io.TeeReader(target, cf)
				}

				sz, err := archive.Apply(ctx, rootFsPath, target)
				if err != nil {
					return err
				}

				m.L.Info("unpacked layer for image", "image", img.Name(), "layer", i, "size", sz)

				return nil
			}

			r, err := l.Uncompressed()
			if err != nil {
				return err
			}

			defer r.Close()

			var target io.Reader = r

			if bar != nil {
				target = io.TeeReader(target, bar)
			}

			sz, err := archive.Apply(ctx, rootFsPath, target)
			if err != nil {
				return err
			}

			m.L.Info("unpacked layer for image", "image", img.Name(), "layer", i, "size", sz)

			return nil
		}()

		if err != nil {
			return nil, err
		}
	}

	if bar != nil {
		bar.Finish()
	}

	return &cfg.Config, nil
}

func (m *ContainerManager) purge(
	ctx *ResourceContext, res *guestapi.Resource, cont *guestapi.Container,
) error {
	ctx.Delete(ctx, res.Id)

	id := res.Id.Short()

	stableId := cont.StableId
	if stableId == "" {
		stableId = id
	}

	r := runc.Runc{
		Root:  m.cfg.RuncRoot,
		Debug: true,
	}

	r.Delete(ctx, id, &runc.DeleteOpts{
		Force: true,
	})

	bundlePath := filepath.Join(m.cfg.BaseDir, stableId)

	return os.RemoveAll(bundlePath)
}

const DefaultHome = "/vol/user/home"

func (m *ContainerManager) startContainer(
	ctx *ResourceContext,
	img name.Reference,
	rid *guestapi.ResourceId,
	status io.Writer,
	width int,
	cont *guestapi.Container,
) error {
	id := rid.Short()

	hostname := cont.Hostname
	if hostname == "" {
		hostname = id
	}

	stableId := cont.StableId
	if stableId == "" {
		stableId = id
	}

	bundlePath := filepath.Join(m.cfg.BaseDir, stableId)
	rootFsPath := filepath.Join(bundlePath, "rootfs")

	var cfg *v1.Config

	var runSetup bool

	if _, err := os.Stat(rootFsPath); err != nil {
		runSetup = true

		ctx.UpdateProvision(ctx, rid, &guestapi.ProvisionStatus{
			StatusDetails: "Downloading and unpacking OCI image",
		})

		cfg, err = m.unpackOCI(ctx, img, rootFsPath, status, width)
		if err != nil {
			return err
		}

		if cfg != nil {
			f, err := os.Create(filepath.Join(bundlePath, "oci-config.json"))
			if err != nil {
				return err
			}

			json.NewEncoder(f).Encode(cfg)

			f.Close()
		}
	} else {
		f, err := os.Open(filepath.Join(bundlePath, "oci-config.json"))
		if err == nil {
			cfg = &v1.Config{}
			json.NewDecoder(f).Decode(&cfg)

			f.Close()
		}
	}

	ctx.UpdateProvision(ctx, rid, &guestapi.ProvisionStatus{
		StatusDetails: "Configuring container",
	})

	volHome := filepath.Join(m.cfg.HomeDir, cont.User.Username)

	os.MkdirAll(filepath.Dir(volHome), 0755)

	runDir := filepath.Join(m.cfg.RunDir, id)

	os.MkdirAll(runDir, 0777)
	defer os.RemoveAll(runDir)

	// make sure that the rundir is properly setup
	os.Chmod(runDir, 0777)

	shareDir := filepath.Join(m.cfg.RunDir, "share")

	os.MkdirAll(shareDir, 0755)

	// Also make sure /run/share exists, since that's what
	// we bind shareDir to.
	os.MkdirAll("/run/share", 0755)

	ipam := &bridge.IPAM{}

	ni := &guestapi.NetworkInfo{}

	var hostAddr string

	type allocatedAddr struct {
		network *guestapi.ResourceId
		address *net.IPNet
	}

	var allocatedAddrs []allocatedAddr

	for _, nw := range cont.Networks {
		allocs, err := m.cfg.NetworkManager.Allocate(ctx, nw, rid)
		if err != nil {
			return err
		}

		for _, alloc := range allocs {
			ip := alloc.Address
			gw := alloc.Gateway

			ni.Addresses = append(ni.Addresses, guestapi.ToIPAddress(ip))

			if gw.To4() != nil {
				hostAddr = gw.String()
			}

			ipam.Addresses = append(ipam.Addresses, bridge.Address{
				Address: *ip,
				Gateway: gw,
			})

			allocatedAddrs = append(allocatedAddrs, allocatedAddr{
				network: nw,
				address: ip,
			})
		}
	}

	defer func() {
		m.L.Debug("deallocating ip addresses")

		for _, addr := range allocatedAddrs {
			m.cfg.NetworkManager.Deallocate(ctx, addr.network, addr.address)
		}
	}()

	s := specs.Spec{
		Version: "1.0.2-dev",
		Process: &specs.Process{
			User: specs.User{
				UID: 0,
				GID: 0,
			},
			Args: []string{"/dev/init"},
			Env: []string{
				"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/opt/isle/bin",
				// "SSH_AUTH_SOCK=/tmp/ssh-agent.sock",
			},
			Cwd: "/",
			Capabilities: &specs.LinuxCapabilities{
				Bounding:    perms,
				Effective:   perms,
				Inheritable: perms,
				Permitted:   perms,
			},
			Rlimits: []specs.POSIXRlimit{
				{
					Type: "RLIMIT_NOFILE",
					Hard: 1024,
					Soft: 1024,
				},
			},
		},
		Root: &specs.Root{
			Path: "rootfs",
		},
		Hostname: hostname,
		Mounts: []specs.Mount{
			{
				Destination: "/proc",
				Type:        "proc",
				Source:      "proc",
				Options:     []string{"nosuid", "noexec", "nodev"},
			},
			{
				Destination: "/dev",
				Type:        "tmpfs",
				Source:      "tmpfs",
				Options:     []string{"nosuid", "strictatime", "mode=755", "size=65535k"},
			},
			{
				Destination: "/dev/pts",
				Type:        "devpts",
				Source:      "devpts",
				Options: []string{
					"nosuid", "noexec", "newinstance", "ptmxmode=0666", "mode=0620", "gid=5",
				},
			},
			{
				Destination: "/dev/shm",
				Type:        "tmpfs",
				Source:      "shm",
				Options: []string{
					"nosuid", "noexec", "nodev", "mode=1777", "size=65536k",
				},
			},
			{
				Destination: "/dev/mqueue",
				Type:        "mqueue",
				Source:      "mqueue",
				Options:     []string{"nosuid", "noexec", "nodev"},
			},
			{
				Destination: "/sys",
				Type:        "sysfs",
				Source:      "sysfs",
				Options:     []string{"nosuid", "noexec", "nodev", "rw"},
			},
			{
				Destination: "/sys/fs/cgroup",
				Type:        "cgroup2",
				Source:      "cgroup",
				Options:     []string{"nosuid", "noexec", "nodev", "rw"},
			},
			{
				Destination: "/run",
				Type:        "bind",
				Source:      runDir,
				Options:     []string{"rbind", "rshared", "rw"},
			},
			{
				Destination: "/run/share",
				Type:        "bind",
				Source:      "/run/share",
				Options:     []string{"rbind", "rshared", "rw"},
			},
			{
				Destination: "/var/global-run",
				Type:        "bind",
				Source:      "/run",
				Options:     []string{"rbind", "rshared", "rw"},
			},
			{
				Destination: "/var/isle-containers",
				Type:        "bind",
				Source:      m.cfg.BaseDir,
				Options:     []string{"rbind", "rshared", "ro"},
			},
			{
				Destination: "/share",
				Type:        "bind",
				Source:      "/share",
				Options:     []string{"rbind", "rshared", "rw"},
			},
			{
				Destination: "/vol",
				Type:        "bind",
				Source:      "/vol",
				Options:     []string{"rbind", "rshared", "rw"},
			},
			{
				Destination: "/home",
				Type:        "bind",
				Source:      m.cfg.HomeDir,
				Options:     []string{"rbind", "rshared", "rw"},
			},
			{
				Destination: "/etc/resolv.conf",
				Type:        "bind",
				Source:      filepath.Join(bundlePath, "resolv.conf"),
				Options:     []string{"rbind", "ro"},
			},
			{
				Destination: "/etc/hosts",
				Type:        "bind",
				Source:      filepath.Join(bundlePath, "hosts"),
				Options:     []string{"rbind", "rw"},
			},
			{
				Destination: "/etc/localtime",
				Type:        "bind",
				Source:      "/etc/localtime",
				Options:     []string{"rbind", "ro"},
			},
			// {
			// Destination: "/tmp/ssh-agent.sock",
			// Type:        "bind",
			// Source:      m.sshAgentPath,
			// Options:     []string{"rbind", "rw"},
			// },
			{
				Destination: "/dev/init",
				Type:        "bind",
				Source:      m.cfg.HelperPath,
				Options:     []string{"rbind", "rshared", "ro"},
			},
			{
				Destination: "/bin/isle",
				Type:        "bind",
				Source:      m.cfg.HelperPath,
				Options:     []string{"rbind", "rshared", "ro"},
			},
			{
				Destination: "/opt/isle/bin",
				Type:        "bind",
				Source:      m.cfg.IsleBinPath,
				Options:     []string{"rbind", "rshared", "ro"},
			},
		},
		Linux: &specs.Linux{
			Resources: &specs.LinuxResources{
				Devices: []specs.LinuxDeviceCgroup{
					{
						Allow:  true,
						Access: "rwm",
					},
				},
			},
			CgroupsPath: "/isle/" + stableId,
			Namespaces: []specs.LinuxNamespace{
				{
					Type: "pid",
				},
				{
					Type: "ipc",
				},
				{
					Type: "uts",
				},
				{
					Type: "mount",
				},
				{
					Type: "network",
				},
			},
		},
	}

	for _, bind := range cont.Binds {
		s.Mounts = append(s.Mounts, specs.Mount{
			Source:      bind.HostPath,
			Destination: bind.ContainerPath,
			Options:     bind.Options,
		})
	}

	err := ioutil.WriteFile(
		filepath.Join(bundlePath, "resolv.conf"),
		[]byte(fmt.Sprintf("nameserver %s\n", hostAddr)), 0644)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(
		filepath.Join(bundlePath, "hosts"),
		[]byte(fmt.Sprintf(
			"127.0.0.1\tlocalhost localhost.localdomain %s\n"+
				"::1\tlocalhost localhost.localdomain %s\n"+
				"192.168.64.1\tmac mac.internal host.internal\n",
			hostname, hostname)), 0644)
	if err != nil {
		return err
	}

	f, err := os.Create(filepath.Join(bundlePath, "config.json"))
	if err != nil {
		return err
	}

	defer f.Close()

	err = json.NewEncoder(f).Encode(s)
	if err != nil {
		return err
	}

	f.Close()

	r := runc.Runc{
		Root:  m.cfg.RuncRoot,
		Debug: true,
	}

	started := make(chan int, 1)

	m.L.Info("creating container", "id", id)

	dw, err := clog.NewDirectoryWriter(filepath.Join(bundlePath, "log"), 0, 0)
	if err != nil {
		return err
	}

	defer dw.Flush()

	logw, err := dw.IOInput(ctx)
	if err != nil {
		return err
	}

	io, err := runc.SetOutputIO(logw)
	if err != nil {
		return err
	}

	ilog := hclog.New(&hclog.LoggerOptions{
		Name:   id,
		Level:  hclog.Trace,
		Output: logw,
	})

	ilog.Info("creating container", "id", id)

	pidPath := filepath.Join(m.cfg.RunDir, id+".pid")

	os.RemoveAll(pidPath)
	defer os.RemoveAll(pidPath)

	err = r.Create(ctx, id, bundlePath, &runc.CreateOpts{
		IO:      io,
		PidFile: pidPath,
	})

	if err != nil {
		return errors.Wrapf(err, "attempting to create container")
	}

	go func() {
		for {
			pid, err := runc.ReadPidFile(pidPath)
			if err != nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}

			started <- pid
			return
		}
	}()

	err = r.Start(ctx, id)
	if err != nil {
		return errors.Wrapf(err, "attempting to start container")
	}

	defer func() {
		err := r.Delete(context.Background(), id, &runc.DeleteOpts{
			Force: true,
		})

		m.L.Error("deleted container", "id", id, "error", err)
	}()

	var pid int

	m.L.Info("waiting for signal that container started")

	select {
	case <-ctx.Done():
		return ctx.Err()
	case pid = <-started:
		// ok
	}

	ilog.Info("container started", "pid", pid)

	h := sha256.New()
	h.Write([]byte(stableId))

	idBytes := h.Sum(nil)

	m.L.Info("configuring networking", "id", base58.Encode(idBytes))

	hwaddr := make(net.HardwareAddr, 6)
	copy(hwaddr, idBytes[2:])

	hwaddr[0] = hwaddr[0] & 0b11111110

	mac := hwaddr.String()

	m.L.Info("assigning container addresses",
		"pid", pid,
		"mac", mac,
	)

	netPath := fmt.Sprintf("/proc/%d/ns/net", pid)

	bridgeConfig := &bridge.Config{
		NetworkName: "isle",
		NetNS:       netPath,
		Bridge:      fmt.Sprintf("isle%d", m.cfg.BridgeID),
		IfName:      "eth0",
		ContainerID: id,
		MAC:         hwaddr,
		IPAM:        ipam,
	}

	var removedCNI bool
	defer func() {
		if removedCNI {
			return
		}

		err := bridge.Delete(ctx, m.L, bridgeConfig)
		m.L.Error("deleted cni for container", "id", id, "error", err)
	}()

	err = bridge.Configure(ctx, m.L, bridgeConfig)
	if err != nil {
		return errors.Wrapf(err, "configuring container networking")
	}

	if runSetup && len(cont.SetupCommand) > 0 {
		ctx.UpdateProvision(ctx, rid, &guestapi.ProvisionStatus{
			StatusDetails: "Executing container setup",
		})

		var setupSp specs.Process
		setupSp.Args = cont.SetupCommand

		setupPath := []string{"/bin", "/sbin", "/usr/bin", "/usr/sbin", "/opt/isle/bin"}

		var sawPath bool

		for k, v := range cont.SetupEnvironment {
			setupSp.Env = append(setupSp.Env, k+"="+v)
			if k == "PATH" {
				sawPath = true
			}
		}

		if !sawPath {
			setupSp.Env = append(setupSp.Env, "PATH="+strings.Join(setupPath, ":"))
		}

		setupSp.Cwd = "/"

		m.L.Info("running container setup")

		err = r.Exec(ctx, id, setupSp, &runc.ExecOpts{})
		if err != nil {
			return err
		}
	}

	var addresses []string

	for _, ip := range ipam.Addresses {
		addresses = append(addresses, ip.Address.String())
	}

	m.L.Info("networking configured", "addresses", addresses)

	ilog.Info("configured networking", "addresses", strings.Join(addresses, ", "))

	path := fmt.Sprintf("/proc/%d/net/tcp", pid)

	if len(ipam.Addresses) > 0 && cont.PortForward != nil {
		m.L.Info("monitoring for ports", "path", path, "target", addresses[0], "api-target", cont.PortForward.Set().String())

		client := guestapi.PBSNewHostAPIClient(
			pbstream.StreamOpenFunc(func() (*pbstream.Stream, error) {
				rs, _, err := m.conMan.Open(cont.PortForward.Set())
				return rs, err
			}))

		pm := &portMonitor{
			id:      rid,
			log:     m.L,
			api:     client,
			network: ni,
			target:  ipam.Addresses[0].Address.IP.String(),
			path:    path,
			conMan:  m.conMan,
		}

		go pm.start(ctx)
	}

	// var ms shareTracker

	// defer m.clearMounts(&ms)

	// cur, advertCh := m.adverts.RegisterEvents(id)

	// defer m.adverts.UnregisterEvents(id)

	// for _, ad := range cur {
	// m.mountAd(&ms, id, ad)
	// }

	err = ctx.UpdateProvision(ctx, rid, &guestapi.ProvisionStatus{
		Status:        guestapi.ProvisionStatus_RUNNING,
		StatusDetails: "container running",
		ContainerInfo: &guestapi.ContainerInfo{
			Id:        id,
			Image:     img.Name(),
			StartedAt: timestamppb.Now(),
			InitPid:   int32(pid),
			NodeId:    m.nodeId,
		},
		NetworkInfo: ni,
	})
	if err != nil {
		m.L.Error("error updating provisioning", "error", err, "id", rid.String())
	}

	m.L.Warn("waiting for signal to stop container")

	ilog.Info("container running")

loop:
	for {
		select {
		case <-ctx.Done():
			break loop
			// case ev := <-advertCh:
			// if ev.Add {
			// m.mountAd(&ms, id, ev.Advertisement)
			// } else {
			// m.removeAd(&ms, id, ev.Advertisement)
			// }
		}
	}

	orig := ctx.Err()

	finCtx := context.Background()

	m.L.Warn("removing networking")
	err = bridge.Delete(finCtx, m.L, bridgeConfig)
	if err != nil {
		m.L.Error("error removing networking", "error", err)
	}
	removedCNI = true

	err = r.Delete(finCtx, id, &runc.DeleteOpts{
		Force: true,
	})

	if err != nil {
		m.L.Error("error removing container", "error", err)
	}

	m.L.Warn("setting container as stopped")

	err = ctx.UpdateProvision(ctx.Under(finCtx), rid, &guestapi.ProvisionStatus{
		Status: guestapi.ProvisionStatus_STOPPED,
	})

	if err != nil {
		m.L.Error("error updating provisioning", "error", err, "id", rid.String())
	}

	return orig
}

type shareTracker struct {
	paths map[string]struct{}
}

func (m *ContainerManager) clearMounts(ms *shareTracker) {
	for path := range ms.paths {

		os.Remove(path)
	}
}

func (m *ContainerManager) mountAd(ms *shareTracker, id string, ad Advertisement) {
	switch ad := ad.(type) {
	case *AdvertiseRun:
		// Ignore your own run paths
		if ad.Source == id {
			return
		}

		path := filepath.Join(m.cfg.RunDir, id, ad.Name)
		sourceRun := filepath.Join("/var/global-run", ad.Source, ad.Name)

		err := os.Symlink(sourceRun, path)
		if err != nil {
			m.L.Error("error creating symlink for run path", "source", sourceRun)
		}

		if ms.paths == nil {
			ms.paths = map[string]struct{}{}
		}

		ms.paths[path] = struct{}{}
	}
}

func (m *ContainerManager) removeAd(ms *shareTracker, id string, ad Advertisement) {
	switch ad := ad.(type) {
	case *AdvertiseRun:
		path := filepath.Join(m.cfg.RunDir, id, ad.Name)
		delete(ms.paths, path)

		os.Remove(path)
	}
}

var (
	ErrNoContainer = errors.New("no container defined")
)

func (m *ContainerManager) Exec(ctx context.Context, res *guestapi.Resource, proc specs.Process) ([]byte, error) {
	if res.ProvisionStatus.ContainerInfo == nil ||
		res.ProvisionStatus.ContainerInfo.Id == "" {
		return nil, ErrNoContainer
	}

	var r runc.Runc
	r.Root = m.cfg.RuncRoot

	pio, output, err := runc.CaptureIO()
	if err != nil {
		return nil, err
	}

	started := make(chan int, 1)

	id := res.ProvisionStatus.ContainerInfo.Id

	err = r.Exec(ctx, id, proc, &runc.ExecOpts{
		IO:      pio,
		Started: started,
	})
	if err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-started:
		// ok
	}

	return output.Bytes(), nil
}

type ExecSession struct {
	Stdout io.ReadCloser
	Stderr io.ReadCloser
	Stdin  io.WriteCloser

	Pid  int
	Done chan runc.Exit
}

func (m *ContainerManager) processStart(ctx context.Context, pidPath string, ch chan runc.Exit) (int, chan runc.Exit, error) {
	data, err := ioutil.ReadFile(pidPath)
	if err != nil {
		return 0, nil, err
	}

	processPid, err := strconv.Atoi(string(data))
	if err != nil {
		return 0, nil, err
	}

	m.L.Info("command running", "pid", processPid)

	done := make(chan runc.Exit, 1)

	go func() {
		defer close(done)
		defer m.reaper.Unsubscribe(ch)

		select {
		case <-ctx.Done():
			return
		case exit := <-ch:
			if exit.Pid == processPid {
				done <- exit
			}
		}
	}()

	return processPid, done, nil
}

func (m *ContainerManager) ExecToStream(ctx context.Context, res *guestapi.Resource, proc specs.Process) (*ExecSession, error) {
	if res.ProvisionStatus.ContainerInfo == nil ||
		res.ProvisionStatus.ContainerInfo.Id == "" {
		return nil, ErrNoContainer
	}

	var r runc.Runc
	r.Root = m.cfg.RuncRoot

	pio, err := runc.NewPipeIO(int(proc.User.UID), int(proc.User.GID))
	if err != nil {
		return nil, err
	}

	started := make(chan int, 1)

	id := res.ProvisionStatus.ContainerInfo.Id

	tmpdir, err := ioutil.TempDir("", "isle-shell")
	if err != nil {
		return nil, err
	}

	defer os.RemoveAll(tmpdir)

	pidPath := filepath.Join(tmpdir, "pid")

	ch := m.reaper.Subscribe()

	err = r.Exec(ctx, id, proc, &runc.ExecOpts{
		IO:      pio,
		Detach:  true,
		Started: started,
		PidFile: pidPath,
	})
	if err != nil {
		m.reaper.Unsubscribe(ch)
		return nil, err
	}

	select {
	case <-ctx.Done():
		m.reaper.Unsubscribe(ch)
		return nil, ctx.Err()
	case <-started:
		// ok
	}

	processPid, done, err := m.processStart(ctx, pidPath, ch)
	if err != nil {
		m.reaper.Unsubscribe(ch)
		return nil, err
	}

	m.L.Info("command running", "pid", processPid)

	ts := &ExecSession{
		Stdin:  pio.Stdin(),
		Stderr: pio.Stderr(),
		Stdout: pio.Stdout(),
		Pid:    processPid,
		Done:   done,
	}

	return ts, nil
}

type TerminalSession struct {
	console.Console

	Pid  int
	Done chan runc.Exit
}

func (m *ContainerManager) ExecInTerminal(ctx context.Context, res *guestapi.Resource, proc specs.Process) (*TerminalSession, error) {
	if res.ProvisionStatus.ContainerInfo == nil ||
		res.ProvisionStatus.ContainerInfo.Id == "" {
		return nil, ErrNoContainer
	}

	var r runc.Runc
	r.Root = m.cfg.RuncRoot

	pio, err := runc.NewSTDIO()
	if err != nil {
		return nil, err
	}

	started := make(chan int, 1)

	id := res.ProvisionStatus.ContainerInfo.Id
	consock, err := runc.NewTempConsoleSocket()
	if err != nil {
		return nil, err
	}

	tmpdir, err := ioutil.TempDir("", "isle-shell")
	if err != nil {
		return nil, err
	}

	defer os.RemoveAll(tmpdir)

	pidPath := filepath.Join(tmpdir, "pid")

	proc.Terminal = true

	ch := m.reaper.Subscribe()

	err = r.Exec(ctx, id, proc, &runc.ExecOpts{
		IO:            pio,
		ConsoleSocket: consock,
		Detach:        true,
		Started:       started,
		Terminal:      true,
		PidFile:       pidPath,
	})
	if err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		m.reaper.Unsubscribe(ch)
		return nil, ctx.Err()
	case <-started:
		// ok
	}

	con, err := consock.ReceiveMaster()
	if err != nil {
		m.reaper.Unsubscribe(ch)
		return nil, err
	}

	processPid, done, err := m.processStart(ctx, pidPath, ch)
	if err != nil {
		m.reaper.Unsubscribe(ch)
		return nil, err
	}

	ts := &TerminalSession{
		Console: con,
		Pid:     processPid,
		Done:    done,
	}

	return ts, nil
}

func (m *ContainerManager) Logs(ctx context.Context, res *guestapi.Resource, ch chan *clog.Entry) error {
	id := res.Id.Short()

	logPath := filepath.Join(m.cfg.BaseDir, id, "log")

	r, err := clog.NewDirectoryReader(logPath)
	if err != nil {
		return err
	}

	go func() {
		defer close(ch)

		for {
			ent, err := r.Next()
			if err != nil {
				return
			}

			select {
			case <-ctx.Done():
				return
			case ch <- ent:
				//ok
			}
		}
	}()

	return nil
}
