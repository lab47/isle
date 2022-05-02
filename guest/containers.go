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
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/containerd/containerd/archive"
	"github.com/containerd/go-cni"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/yamux"
	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/pkg/clog"
	"github.com/lab47/isle/pkg/netutil"
	"github.com/lab47/isle/pkg/progressbar"
	"github.com/lab47/isle/pkg/reaper"
	"github.com/lab47/isle/pkg/runc"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ContainerManager struct {
	L hclog.Logger

	basePath string

	nodeId        string
	hostAddr      string
	v6clusterAddr net.IP
	v6subnetAddr  net.IP

	User         string
	ClusterId    string
	sshAgentPath string

	cni       cni.CNI
	netconfig *netutil.NetworkConfigList

	currentSession *yamux.Session

	hostAPI guestapi.HostAPIClient
	adverts Advertisements

	bgCtx    context.Context
	bgCancel func()

	reaper *reaper.Monitor
}

func (m *ContainerManager) Init(ctx *ResourceContext) error {
	ctx.SetSchema("container", "container", &guestapi.Container{}, "StableName")
	m.basePath = "/data/containers"
	return nil
}

func (m *ContainerManager) Create(ctx *ResourceContext, cont *guestapi.Container) (*guestapi.Resource, error) {
	if cont.StableName != "" {
		nameVal, err := structpb.NewValue(cont.StableName)
		if err != nil {
			return nil, err
		}

		resources, err := ctx.FetchByIndex(&guestapi.ResourceIndexKey{
			Category: "container",
			Type:     "container",
			Value:    nameVal,
		})

		if err == nil {
			return nil, err
		}

		if len(resources) != 0 {
			return nil, errors.Wrapf(ErrConflict, "container with name already exist: %s", cont.StableName)
		}
	}

	id := NewId("container", "container")

	imgref, err := name.ParseReference(cont.Image)
	if err != nil {
		return nil, err
	}

	go func() {
		err := m.startContainer(ctx, imgref, id, nil, 0)
		if err != nil {
			m.L.Error("error creating container", "error", err, "id", id.UniqueId)
			err = ctx.SetError(id, err)
			if err != nil {
				m.L.Error("error recording error on container", "error", err, "id", id.UniqueId)
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

	pid := int(res.ProvisionStatus.ContainerInfo.InitPid)
	if pid == 0 {
		m.L.Warn("deleting container that has no init pid", "id", res.Id.UniqueId)

		_, err := ctx.Delete(res.Id)
		if err != nil {
			m.L.Error("error deleting resource from store", "error", err)
		}
	}

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

	return nil
}

func init() {
	DefaultRegistry.Register(
		&guestapi.Container{},
		TypedManager[*guestapi.Container](&ContainerManager{}),
	)
}

func (m *ContainerManager) unpackOCI(
	ctx context.Context,
	img name.Reference,
	rootFsPath string,
	status io.Writer,
	width int,
) (*v1.Config, error) {
	rimg, err := remote.Image(img,
		remote.WithPlatform(v1.Platform{
			Architecture: runtime.GOARCH,
			OS:           "linux",
		}),
	)
	if err != nil {
		return nil, err
	}

	cfg, err := rimg.ConfigFile()
	if err != nil {
		return nil, err
	}

	err = os.MkdirAll(rootFsPath, 0755)
	if err != nil {
		return nil, err
	}

	layers, err := rimg.Layers()
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
		r, err := l.Uncompressed()
		if err != nil {
			return nil, err
		}

		defer r.Close()

		var target io.Reader = r

		if bar != nil {
			target = io.TeeReader(target, bar)
		}

		sz, err := archive.Apply(ctx, rootFsPath, target)
		if err != nil {
			return nil, err
		}

		m.L.Info("unpacked layer for image", "image", img.Name(), "layer", i, "size", sz)
	}

	bar.Finish()

	return &cfg.Config, nil
}

func (m *ContainerManager) startContainer(
	ctx *ResourceContext,
	img name.Reference,
	rid *guestapi.ResourceId,
	status io.Writer,
	width int,
) error {
	id := rid.UniqueId
	name := rid.UniqueId

	bundlePath := filepath.Join(m.basePath, name)
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

	err := ioutil.WriteFile(
		filepath.Join(bundlePath, "resolv.conf"),
		[]byte(fmt.Sprintf("nameserver %s\n", m.hostAddr)), 0644)
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

	volHome := "/vol/user/home/" + m.User

	os.MkdirAll(filepath.Dir(volHome), 0755)

	runDir := filepath.Join("/run", id)

	os.MkdirAll(runDir, 0777)
	defer os.RemoveAll(runDir)

	// make sure that the rundir is properly setup
	os.Chmod(runDir, 0777)

	os.MkdirAll("/run/share", 0755)

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
				Source:      "/data/containers",
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
				Source:      "/usr/bin/isle-helper",
				Options:     []string{"rbind", "rshared", "ro"},
			},
			{
				Destination: "/bin/isle",
				Type:        "bind",
				Source:      "/usr/bin/isle-helper",
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
		Debug: true,
	}

	started := make(chan int, 1)

	m.L.Info("creating container", "id", id)

	dw, err := clog.NewDirectoryWriter(filepath.Join(bundlePath, "log"), 0, 0)
	if err != nil {
		return err
	}

	logw, err := dw.IOInput(ctx)
	if err != nil {
		return err
	}

	io, err := runc.SetOutputIO(logw)
	if err != nil {
		return err
	}

	pidPath := filepath.Join("/run", id+".pid")

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

	m.L.Info("configuring networking")

	h := sha256.New()
	h.Write([]byte(name))

	idBytes := h.Sum(nil)

	v6addr := make(net.IP, net.IPv6len)
	copy(v6addr, m.v6subnetAddr)

	copy(v6addr[8:], idBytes[:8])

	hwaddr := make(net.HardwareAddr, 6)
	copy(hwaddr, idBytes[2:])

	hwaddr[0] = hwaddr[0] & 0b11111110

	mac := hwaddr.String()

	m.L.Info("assigning container custom ipv6 address", "address", v6addr.String(), "mac", mac)

	netPath := fmt.Sprintf("/proc/%d/ns/net", pid)

	cniResult, err := m.cni.Setup(ctx, id, netPath,
		cni.WithCapability("ips", []string{v6addr.String() + "/64"}),
		cni.WithCapability("mac", mac),
	)
	if err != nil {
		return err
	}

	var removedCNI bool
	defer func() {
		if removedCNI {
			return
		}

		err := m.cni.Remove(ctx, id, netPath)
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
		setup[i] = strings.ReplaceAll(str, "%user", m.User)
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

	ni := &guestapi.NetworkInfo{
		Ipv4: target,
		Ipv6: target6,
	}

	if target == "" {
		m.L.Info("CNI failed to report an IP, no port mapping enabled")
	} else {
		m.L.Info("networking configured", "ipv4", target, "ipv6", target6)

		path := fmt.Sprintf("/proc/%d/net/tcp", pid)

		m.L.Info("monitoring for ports", "path", path, "target", target)

		pm := &portMonitor{
			id:      rid,
			log:     m.L,
			api:     m.hostAPI,
			sess:    m.currentSession,
			network: ni,
			target:  target,
			path:    path,
		}

		go pm.start(ctx)
	}

	var ms shareTracker

	defer m.clearMounts(&ms)

	cur, advertCh := m.adverts.RegisterEvents(id)

	defer m.adverts.UnregisterEvents(id)

	for _, ad := range cur {
		m.mountAd(&ms, id, ad)
	}

	ctx.UpdateProvision(rid, &guestapi.ProvisionStatus{
		Status: guestapi.ProvisionStatus_RUNNING,
		ContainerInfo: &guestapi.ContainerInfo{
			Image:     img.Name(),
			StartedAt: timestamppb.Now(),
			InitPid:   int32(pid),
			NodeId:    m.nodeId,
		},
		NetworkInfo: ni,
	})

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
	err = m.cni.Remove(finCtx, id, netPath)
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

		path := filepath.Join("/run", id, ad.Name)
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
		path := filepath.Join("/run", id, ad.Name)
		delete(ms.paths, path)

		os.Remove(path)
	}
}
