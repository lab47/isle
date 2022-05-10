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
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd/archive"
	"github.com/containerd/go-cni"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/yamux"
	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/pkg/bridge"
	"github.com/lab47/isle/pkg/netutil"
	"github.com/lab47/isle/pkg/progressbar"
	"github.com/lab47/isle/pkg/reaper"
	"github.com/lab47/isle/pkg/runc"
	"github.com/mr-tron/base58"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ContainerManager struct {
	L hclog.Logger

	cfg ContainerConfig

	nodeId   string
	hostAddr string
	// v6clusterAddr net.IP
	// v6subnetAddr  net.IP
	// v6gateway     net.IP

	sshAgentPath string

	cni       cni.CNI
	netconfig *netutil.NetworkConfigList

	currentSession *yamux.Session

	hostAPI guestapi.HostAPIClient
	adverts Advertisements

	bgCtx    context.Context
	bgCancel func()

	reaper *reaper.Monitor

	containerSchema *Schema

	cniEnv *netutil.CNIEnv

	v4gateway net.IP
	v4addrs   map[string]string

	track containerTracker

	runningMu sync.Mutex
	running   map[string]func()
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

	HelperPath string

	NetworkManager *IPNetworkManager
}

func CNIEnv() (*netutil.CNIEnv, error) {
	ce := &netutil.CNIEnv{
		NetconfPath: "/etc/cni/net.d",
	}

	for _, path := range []string{"/usr/libexec/cni", "/usr/lib/cni"} {
		if _, err := os.Stat(path); err == nil {
			ce.Path = path
			break
		}
	}

	if ce.Path == "" {
		return nil, fmt.Errorf("unable to find cni plugins")
	}

	return ce, nil
}

func (m *ContainerManager) Init(ctx *ResourceContext, cfg *ContainerConfig) error {
	s, err := ctx.SetSchema("container", "container", &guestapi.Container{}, "stable_name", "image")
	if err != nil {
		return err
	}

	m.running = make(map[string]func())

	m.cfg = *cfg

	m.containerSchema = s
	m.v4addrs = make(map[string]string)

	m.L = cfg.Logger

	ip := make(net.IP, net.IPv6len)
	ip[0] = 0xfd

	// data, err := hex.DecodeString(m.cfg.ClusterId)
	// if err != nil {
	// return err
	// }

	// copy(ip[1:], data)

	// m.v6clusterAddr = ip

	// m.v6subnetAddr = make(net.IP, net.IPv6len)
	// copy(m.v6subnetAddr, ip)

	// subnet := cfg.SubnetId

	// if cfg.SubnetId != "" {
	// data, err = hex.DecodeString(subnet)
	// if err != nil {
	// return err
	// }
	// } else {
	// data = make([]byte, 2)
	// _, err = io.ReadFull(rand.Reader, data)
	// if err != nil {
	// return err
	// }

	// subnet = hex.EncodeToString(data)
	// }

	// copy(m.v6subnetAddr[6:], data)

	m.cniEnv, err = CNIEnv()
	if err != nil {
		return err
	}

	// v6gateway, _, err := net.ParseCIDR(m.v6subnetAddr.String() + "/64")
	// if err != nil {
	// return err
	// }

	/*
		m.netconfig = ll

		gc, err := cni.New(
			cni.WithPluginDir([]string{m.cniEnv.Path}),
			cni.WithConfListBytes(ll.Bytes),
		)
		if err != nil {
			return err
		}
	*/

	// v6gateway[len(v6gateway)-1] = 1
	// m.v6gateway = v6gateway

	// m.hostAddr = m.netconfig.GatewayV4
	// m.cni = gc

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

	return nil
}

func (m *ContainerManager) Close() error {
	m.bgCancel()
	m.track.Wait()
	return nil
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

		if len(resources) != 0 {
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

			ctx.Delete(id)
		}()

		sub := &ResourceContext{
			Context:         goctx,
			ResourceStorage: ctx.ResourceStorage,
		}

		err := m.startContainer(sub, imgref, id, nil, 0, cont)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				ctx.UpdateProvision(id, &guestapi.ProvisionStatus{
					Status: guestapi.ProvisionStatus_DEAD,
				})
			} else {
				m.L.Error("error creating container", "error", err, "id", id.UniqueId)
				err = ctx.SetError(id, err)
				if err != nil {
					m.L.Error("error recording error on container", "error", err, "id", id.UniqueId)
				}
			}
		}
	}()

	prov := &guestapi.ProvisionStatus{
		Status: guestapi.ProvisionStatus_ADDING,
	}

	return ctx.Set(id, cont, prov)
}

func (m *ContainerManager) Update(ctx *ResourceContext, res *guestapi.Resource) (*guestapi.Resource, error) {
	return nil, ErrImmutable
}

func (m *ContainerManager) Read(ctx *ResourceContext, id *guestapi.ResourceId) (*guestapi.Resource, error) {
	return ctx.Fetch(id)
}

func (m *ContainerManager) Delete(ctx *ResourceContext, res *guestapi.Resource, cont *guestapi.Container) error {
	res.ProvisionStatus.Status = guestapi.ProvisionStatus_DEAD
	err := ctx.UpdateProvision(res.Id, res.ProvisionStatus)
	if err != nil {
		return err
	}

	m.runningMu.Lock()
	cancel, isRunning := m.running[res.Id.Short()]
	m.runningMu.Unlock()

	if !isRunning {
		m.L.Warn("deleting container that is not running", "id", res.Id.UniqueId)

		_, err = ctx.Delete(res.Id)
		if err != nil {
			m.L.Error("error deleting resource from store", "error", err)
		}

		return nil
	}

	// We cancel it and let the monitoring goroutine perform the
	// cleanup.
	m.L.Debug("canceling context of running monitor to delete container")
	cancel()

	/*
		pid := int(res.ProvisionStatus.ContainerInfo.InitPid)

		go func() {
			defer func() {
				_, err := ctx.Delete(res.Id)
				if err != nil {
					m.L.Error("error deleting resource from store", "error", err)
				}
			}()

			ch := m.reaper.Subscribe()
			defer m.reaper.Unsubscribe(ch)

			err = unix.Kill(pid, unix.SIGQUIT)
			if err != nil {
				m.L.Error("error sending kill signal", "error", err, "pid", pid)
			}

			timer := time.NewTicker(10 * time.Second)
			defer timer.Stop()

			var killed bool

			for {
				select {
				case <-ctx.Done():
					m.L.Debug("force killing pid due to context shutdown", "pid", pid)
					unix.Kill(pid, unix.SIGKILL)
					return
				case <-timer.C:
					if killed {
						m.L.Error("killed container init, but never saw wait status", "pid", pid)
						return
					}

					unix.Kill(pid, unix.SIGKILL)
					killed = true
				case es := <-ch:
					if es.Pid == pid {
						m.L.Debug("detected container has exited")
						return
					}
				}
			}
		}()
	*/

	return nil
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
	name := rid.Short()

	bundlePath := filepath.Join(m.cfg.BaseDir, name)
	rootFsPath := filepath.Join(bundlePath, "rootfs")

	var cfg *v1.Config

	if _, err := os.Stat(rootFsPath); err != nil {
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

	volHome := filepath.Join(m.cfg.HomeDir, m.cfg.User)

	os.MkdirAll(filepath.Dir(volHome), 0755)

	runDir := filepath.Join(m.cfg.RunDir, id)

	os.MkdirAll(runDir, 0777)
	defer os.RemoveAll(runDir)

	// make sure that the rundir is properly setup
	os.Chmod(runDir, 0777)

	shareDir := filepath.Join(m.cfg.RunDir, "share")

	os.MkdirAll(shareDir, 0755)

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

			if gw.To4() == nil {
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
				"SSH_AUTH_SOCK=/tmp/ssh-agent.sock",
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
		Hostname: name,
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
				Source:      "/vol/user/home",
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
			{
				Destination: "/tmp/ssh-agent.sock",
				Type:        "bind",
				Source:      m.sshAgentPath,
				Options:     []string{"rbind", "rw"},
			},
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
				Source:      "/opt/isle/bin",
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
			CgroupsPath: "/isle/" + name,
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
			name, name)), 0644)
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

	io, err := runc.NewSTDIO()

	/*

		dw, err := clog.NewDirectoryWriter(filepath.Join(bundlePath, "log"), 0, 0)
		if err != nil {
			return err
		}

		logw, err := dw.IOInput(ctx)
		if err != nil {
			return err
		}

		io, err := runc.SetOutputIO(logw)
	*/
	if err != nil {
		return err
	}

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

	h := sha256.New()
	h.Write([]byte(name))

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

	err = bridge.Configure(ctx, m.L, bridgeConfig)
	if err != nil {
		return errors.Wrapf(err, "configuring container networking")
	}

	/*
		ll, err := netutil.StaticConfigList(m.cniEnv, 2, ipv4addr, ipv6addr)
		if err != nil {
			return errors.Wrapf(err, "attempting to setup config list")
		}

		gc, err := cni.New(
			cni.WithPluginDir([]string{m.cniEnv.Path}),
			cni.WithConfListBytes(ll.Bytes),
		)
		if err != nil {
			return err
		}

		cniResult, err := gc.Setup(ctx, id, netPath,
			cni.WithCapability("mac", mac),
		)
		if err != nil {
			return errors.Wrapf(err, "error configuring container networking: %s", id)
		}
	*/

	var removedCNI bool
	defer func() {
		if removedCNI {
			return
		}

		err := bridge.Delete(ctx, m.L, bridgeConfig)
		m.L.Error("deleted cni for container", "id", id, "error", err)
	}()

	setup := []string{
		// We need be sure that /var/run is mapped to /run because
		// we mount /run under the host's /run so that it can be
		// accessed by the host.
		"rm -rf /var/run; ln -s /run /var/run",
		"mkdir -p /etc/sudoers.d",
		"echo '%user ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/00-%user",
		"echo root:root | chpasswd",
		"id evan || useradd -u 501 -m %user || adduser -u 501 -h /home/%user %user",
		"echo %user:%user | chpasswd",
		"stat /home/%user/mac || ln -sf /share/home /home/%user/mac",
	}

	for i, str := range setup {
		setup[i] = strings.ReplaceAll(str, "%user", m.cfg.User)
	}

	var setupSp specs.Process
	setupSp.Args = []string{"/bin/sh", "-c", strings.Join(setup, "; ")}
	setupSp.Env = []string{"PATH=/bin:/sbin:/usr/bin:/usr/sbin:/opt/isle/bin"}
	setupSp.Cwd = "/"

	m.L.Info("running container setup")

	err = r.Exec(ctx, id, setupSp, &runc.ExecOpts{})
	if err != nil {
		return err
	}

	// target := ipv4addr.IP.String()
	// target6 := ipv6addr.IP.String()

	/*
		var target, target6 string

		primary, ok := cniResult.Interfaces["eth0"]
		if !ok {
			m.L.Info("CNI failed to report an eth0")
		} else {
			for _, ipconfig := range primary.IPConfigs {
				if ipconfig.IP.To4() != nil {
					target = ipconfig.IP.String()
				} else {
					target6 = ipconfig.IP.String()
				}
			}
		}
	*/

	var addresses []string

	for _, ip := range ipam.Addresses {
		addresses = append(addresses, ip.Address.String())
	}

	m.L.Info("networking configured", "addresses", addresses)

	path := fmt.Sprintf("/proc/%d/net/tcp", pid)

	m.L.Info("monitoring for ports", "path", path, "target", addresses[0])

	pm := &portMonitor{
		id:      rid,
		log:     m.L,
		api:     m.hostAPI,
		sess:    m.currentSession,
		network: ni,
		target:  addresses[0],
		path:    path,
	}

	go pm.start(ctx)

	var ms shareTracker

	defer m.clearMounts(&ms)

	cur, advertCh := m.adverts.RegisterEvents(id)

	defer m.adverts.UnregisterEvents(id)

	for _, ad := range cur {
		m.mountAd(&ms, id, ad)
	}

	err = ctx.UpdateProvision(rid, &guestapi.ProvisionStatus{
		Status: guestapi.ProvisionStatus_RUNNING,
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

loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case ev := <-advertCh:
			if ev.Add {
				m.mountAd(&ms, id, ev.Advertisement)
			} else {
				m.removeAd(&ms, id, ev.Advertisement)
			}
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

	m.L.Warn("removing container")

	err = r.Delete(finCtx, id, &runc.DeleteOpts{
		Force: true,
	})

	if err != nil {
		m.L.Error("error removing container", "error", err)
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
