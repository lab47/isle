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
	"github.com/hashicorp/yamux"
	"github.com/lab47/isle/pkg/clog"
	"github.com/lab47/isle/pkg/progressbar"
	"github.com/lab47/isle/pkg/runc"
	"github.com/lab47/isle/pkg/shardconfig"
	"github.com/pkg/errors"
	"github.com/rs/xid"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

const basePath = "/data/containers"

var perms = []string{
	"CAP_CHOWN",
	"CAP_DAC_OVERRIDE",
	"CAP_DAC_READ_SEARCH",
	"CAP_FOWNER",
	"CAP_FSETID",
	"CAP_KILL",
	"CAP_SETGID",
	"CAP_SETUID",
	"CAP_SETPCAP",
	"CAP_LINUX_IMMUTABLE",
	"CAP_NET_BIND_SERVICE",
	"CAP_NET_BROADCAST",
	"CAP_NET_ADMIN",
	"CAP_NET_RAW",
	"CAP_IPC_LOCK",
	"CAP_IPC_OWNER",
	"CAP_SYS_MODULE",
	"CAP_SYS_RAWIO",
	"CAP_SYS_CHROOT",
	"CAP_SYS_PTRACE",
	"CAP_SYS_PACCT",
	"CAP_SYS_ADMIN",
	"CAP_SYS_BOOT",
	"CAP_SYS_NICE",
	"CAP_SYS_RESOURCE",
	"CAP_SYS_TIME",
	"CAP_SYS_TTY_CONFIG",
	"CAP_MKNOD",
	"CAP_LEASE",
	"CAP_AUDIT_WRITE",
	"CAP_AUDIT_CONTROL",
	"CAP_SETFCAP",
	"CAP_MAC_OVERRIDE",
	"CAP_MAC_ADMIN",
	"CAP_SYSLOG",
	"CAP_WAKE_ALARM",
	"CAP_BLOCK_SUSPEND",
	"CAP_AUDIT_READ",
	"CAP_PERFMON",
	"CAP_BPF",
	"CAP_CHECKPOINT_RESTORE",
}

func (g *Guest) Container(ctx context.Context, info *ContainerInfo) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	rc, ok := g.running[info.Name]
	if ok {
		return rc.id, nil
	}

	started := make(chan string, 1)
	errorer := make(chan error, 1)

	ctx, cancel := context.WithCancel(ctx)

	info.StartCh = started

	id := xid.New().String()

	go func() {
		errorer <- g.StartContainer(ctx, info, id)
	}()

	select {
	case <-ctx.Done():
		cancel()
		return "", ctx.Err()
	case err := <-errorer:
		g.L.Error("error booting container", "error", err)
		cancel()
		return "", err
	case <-started:
		// ok
	}

	g.running[info.Name] = &runningContainer{
		id:     id,
		cancel: cancel,
		doneCh: errorer,
	}

	return id, nil
}

type ContainerInfo struct {
	Name    string
	Img     name.Reference
	StartCh chan string
	Status  io.Writer
	Width   int
	Session *yamux.Session

	Unpacker  func(ctx context.Context, path string) error
	FixupSpec func(sp *specs.Spec) error

	Config *shardconfig.Config

	OCIConfig *v1.Config
}

func (g *Guest) ociUnpacker(info *ContainerInfo) error {
	info.Unpacker = func(ctx context.Context, rootFsPath string) error {
		rimg, err := remote.Image(info.Img,
			remote.WithPlatform(v1.Platform{
				Architecture: runtime.GOARCH,
				OS:           "linux",
			}),
		)
		if err != nil {
			return err
		}

		cfg, err := rimg.ConfigFile()
		if err != nil {
			return err
		}

		info.OCIConfig = &cfg.Config

		err = os.MkdirAll(rootFsPath, 0755)
		if err != nil {
			return err
		}

		layers, err := rimg.Layers()
		if err != nil {
			return err
		}

		var max int64

		for _, l := range layers {
			sz, err := l.Size()
			if err == nil {
				max += sz
			}
		}

		bar := progressbar.NewOptions64(
			max,
			progressbar.OptionSetDescription("Downloading"),
			progressbar.OptionSetWriter(info.Status),
			progressbar.OptionShowBytes(true),
			progressbar.OptionSetWidth(10),
			progressbar.OptionSetTerminalWidth(info.Width),
			progressbar.OptionFullWidth(),
			progressbar.OptionThrottle(65*time.Millisecond),
			progressbar.OptionShowCount(),
			progressbar.OptionSpinnerType(14),
			progressbar.OptionUseANSICodes(true),
		)
		bar.RenderBlank()

		for i, l := range layers {
			r, err := l.Uncompressed()
			if err != nil {
				return err
			}

			defer r.Close()

			sz, err := archive.Apply(ctx, rootFsPath, io.TeeReader(r, bar))
			if err != nil {
				return err
			}

			g.L.Info("unpacked layer for image", "image", info.Img.Name(), "layer", i, "size", sz)
		}

		bar.Finish()

		return nil
	}

	return nil
}

func (g *Guest) StartContainer(
	ctx context.Context,
	info *ContainerInfo,
	id string,
) error {
	name := info.Name

	bundlePath := filepath.Join(basePath, name)
	rootFsPath := filepath.Join(bundlePath, "rootfs")

	if _, err := os.Stat(rootFsPath); err != nil {
		err = info.Unpacker(ctx, rootFsPath)
		if err != nil {
			return err
		}

		if info.OCIConfig != nil {
			f, err := os.Create(filepath.Join(bundlePath, "oci-config.json"))
			if err != nil {
				return err
			}

			json.NewEncoder(f).Encode(info.OCIConfig)

			f.Close()
		}
	} else {
		f, err := os.Open(filepath.Join(bundlePath, "oci-config.json"))
		if err == nil {
			var cfg v1.Config
			json.NewDecoder(f).Decode(&cfg)

			info.OCIConfig = &cfg

			f.Close()
		}
	}

	err := ioutil.WriteFile(
		filepath.Join(bundlePath, "resolv.conf"),
		[]byte(fmt.Sprintf("nameserver %s\n", g.hostAddr)), 0644)
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

	volHome := "/vol/user/home/" + g.User

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
				Source:      g.sshAgentPath,
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

	if info.FixupSpec != nil {
		err = info.FixupSpec(&s)
		if err != nil {
			return err
		}
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

	g.L.Info("creating container", "id", id)

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

		g.L.Error("deleted container", "id", id, "error", err)
	}()

	var pid int

	g.L.Info("waiting for signal that container started")

	select {
	case <-ctx.Done():
		return ctx.Err()
	case pid = <-started:
		// ok
	}

	g.L.Info("configuring networking")

	h := sha256.New()
	h.Write([]byte(info.Name))

	idBytes := h.Sum(nil)

	v6addr := make(net.IP, net.IPv6len)
	copy(v6addr, g.v6subnetAddr)

	copy(v6addr[8:], idBytes[:8])

	hwaddr := make(net.HardwareAddr, 6)
	copy(hwaddr, idBytes[2:])

	hwaddr[0] = hwaddr[0] & 0b11111110

	mac := hwaddr.String()

	g.L.Info("assigning container custom ipv6 address", "address", v6addr.String(), "mac", mac)

	netPath := fmt.Sprintf("/proc/%d/ns/net", pid)

	cniResult, err := g.cni.Setup(ctx, id, netPath,
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

		err := g.cni.Remove(ctx, id, netPath)
		g.L.Error("deleted cni for container", "id", id, "error", err)
	}()

	setup := []string{
		// We need be sure that /var/run is mapped to /run because
		// we mount /run under the host's /run so that it can be
		// accessed by the host.
		"rm -rf /var/run; ln -s /run /var/run",
	}

	if info.Config == nil || info.Config.Service == nil {
		setup = append(setup,
			"mkdir -p /etc/sudoers.d",
			"echo '%user ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/00-%user",
			"echo root:root | chpasswd",
			"id evan || useradd -u 501 -m %user || adduser -u 501 -h /home/%user %user",
			"echo %user:%user | chpasswd",
			"stat /home/%user/mac || ln -sf /share/home /home/%user/mac",
		)
	}

	for i, str := range setup {
		setup[i] = strings.ReplaceAll(str, "%user", g.User)
	}

	var setupSp specs.Process
	setupSp.Args = []string{"/bin/sh", "-c", strings.Join(setup, "; ")}
	setupSp.Env = []string{"PATH=/bin:/sbin:/usr/bin:/usr/sbin:/opt/isle/bin"}
	setupSp.Cwd = "/"

	g.L.Info("running container setup")

	err = r.Exec(ctx, id, setupSp, &runc.ExecOpts{})
	if err != nil {
		return err
	}

	var target, target6 string

	primary, ok := cniResult.Interfaces["eth0"]
	if !ok {
		g.L.Info("CNI failed to report an eth0")
	} else {
		for _, ipconfig := range primary.IPConfigs {
			if ipconfig.IP.To4() != nil {
				target = ipconfig.IP.String()
			} else {
				target6 = ipconfig.IP.String()
			}
		}
	}

	if target == "" {
		g.L.Info("CNI failed to report an IP, no port mapping enabled")
	} else {
		g.L.Info("networking configured", "ipv4", target, "ipv6", target6)

		path := fmt.Sprintf("/proc/%d/net/tcp", pid)

		g.L.Info("monitoring for ports", "path", path, "target", target)
		go g.monitorPorts(ctx, info.Session, target, path)
	}

	g.L.Warn("waiting for signal to stop container")

	select {
	case info.StartCh <- id:
		// ok
	case <-ctx.Done():
		return ctx.Err()
	}

	var ms mountStatus

	defer g.clearMounts(&ms)

	cur, advertCh := g.adverts.RegisterEvents(id)

	defer g.adverts.UnregisterEvents(id)

	for _, ad := range cur {
		g.mountAd(&ms, id, ad)
	}

loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case ev := <-advertCh:
			if ev.Add {
				g.mountAd(&ms, id, ev.Advertisement)
			} else {
				g.removeAd(&ms, id, ev.Advertisement)
			}
		}
	}

	orig := ctx.Err()

	ctx = context.Background()

	g.L.Warn("removing networking")
	err = g.cni.Remove(ctx, id, netPath)
	if err != nil {
		g.L.Error("error removing networking", "error", err)
	}
	removedCNI = true

	g.L.Warn("removing container")

	err = r.Delete(ctx, id, &runc.DeleteOpts{
		Force: true,
	})

	if err != nil {
		g.L.Error("error removing container", "error", err)
	}

	return orig
}

type mountStatus struct {
	paths map[string]struct{}
}

func (g *Guest) clearMounts(ms *mountStatus) {
	for path := range ms.paths {

		os.Remove(path)

		/*
			cmd := exec.Command("umount", path)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			err := cmd.Run()
			if err != nil {
				g.L.Error("error unmounting shared path", "path", path)
			} else {
				os.RemoveAll(path)
			}
		*/
	}
}

func (g *Guest) mountAd(ms *mountStatus, id string, ad Advertisement) {
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
			g.L.Error("error creating symlink for run path", "source", sourceRun)
		}

		/*

			path := filepath.Join("/run", id, ad.Name)
			fromHost := filepath.Join("/run", ad.Source, ad.Name)

			if fi, err := os.Stat(fromHost); err == nil && fi.IsDir() {
				err = os.MkdirAll(path, 0755)
				if err != nil {
					g.L.Error("error setting up mount point", "error", err, "path", path)
				}
			} else {
				err = ioutil.WriteFile(path, []byte(nil), 0644)
				if err != nil {
					g.L.Error("error setting up mount point", "error", err, "path", path)
				}
			}

			g.L.Info("bind mounting run path", "from", fromHost, "to", path)
			cmd := exec.Command("mount", "--bind", fromHost, path)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			err := cmd.Run()
			if err != nil {
				g.L.Error("error bind mounting into shared", "from", fromHost, "to", path)
			}
		*/

		if ms.paths == nil {
			ms.paths = map[string]struct{}{}
		}

		ms.paths[path] = struct{}{}
	}
}

func (g *Guest) removeAd(ms *mountStatus, id string, ad Advertisement) {
	switch ad := ad.(type) {
	case *AdvertiseRun:
		path := filepath.Join("/run", id, ad.Name)
		delete(ms.paths, path)

		/*
			cmd := exec.Command("umount", path)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			err := cmd.Run()
			if err != nil {
				g.L.Error("error unmounting", "path", path)
			}
		*/

		os.Remove(path)
	}
}
