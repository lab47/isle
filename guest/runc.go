package guest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/containerd/archive"
	"github.com/containerd/go-runc"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/hashicorp/yamux"
	"github.com/lab47/yalr4m/pkg/progressbar"
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

func (g *Guest) Container(ctx context.Context, info ContainerInfo) (string, error) {
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

	go func() {
		errorer <- g.StartContainer(ctx, info)
	}()

	var id string

	select {
	case <-ctx.Done():
		cancel()
		return "", ctx.Err()
	case err := <-errorer:
		cancel()
		return "", err
	case id = <-started:
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
}

func (g *Guest) StartContainer(
	ctx context.Context,
	info ContainerInfo,
) error {
	name := info.Name
	status := info.Status

	bundlePath := filepath.Join(basePath, name)
	rootFsPath := filepath.Join(bundlePath, "rootfs")

	if _, err := os.Stat(rootFsPath); err != nil {
		rimg, err := remote.Image(info.Img,
			remote.WithPlatform(v1.Platform{
				Architecture: "arm64",
				OS:           "linux",
			}),
		)
		if err != nil {
			return err
		}

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
			progressbar.OptionSetWriter(status),
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

	s := specs.Spec{
		Version: "1.0.2-dev",
		Process: &specs.Process{
			User: specs.User{
				UID: 0,
				GID: 0,
			},
			Args: []string{"bash"},
			Env: []string{
				"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
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
				Destination: "/run",
				Type:        "tmpfs",
				Source:      "tmpfs",
				Options:     []string{"nosuid", "strictatime", "mode=755", "size=65535k"},
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
			CgroupsPath: "/yalr4m/" + name,
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

	id := xid.New().String()

	g.L.Info("creating container", "id", id)

	io, err := runc.NewNullIO()
	if err != nil {
		return err
	}

	pidPath := filepath.Join(bundlePath, "pid")

	err = r.Create(ctx, id, bundlePath, &runc.CreateOpts{
		IO:      io,
		PidFile: pidPath,
	})

	if err != nil {
		return err
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

	defer r.Delete(ctx, id, &runc.DeleteOpts{
		Force: true,
	})

	var pid int

	g.L.Info("waiting for signal that container started")

	select {
	case <-ctx.Done():
		return ctx.Err()
	case pid = <-started:
		// ok
	}

	g.L.Info("configuring networking")

	netPath := fmt.Sprintf("/proc/%d/ns/net", pid)

	cniResult, err := g.cni.Setup(ctx, id, netPath)
	if err != nil {
		return err
	}

	defer g.cni.Remove(ctx, id, netPath)

	g.L.Info("executing setup")

	setup := []string{
		"mkdir -p /etc/sudoers.d",
		"echo '%user ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/00-%user",
		"echo root:root | chpasswd",
		"id evan || useradd -u 501 -m %user || adduser -u 501 -h /home/%user %user",
		"echo %user:%user | chpasswd",
		"stat /home/%user/mac || ln -s /share/home /home/%user/mac",
	}

	for i, str := range setup {
		setup[i] = strings.ReplaceAll(str, "%user", g.User)
	}

	var setupSp specs.Process
	setupSp.Args = []string{"/bin/sh", "-c", strings.Join(setup, "; ")}
	setupSp.Env = []string{"PATH=/bin:/sbin:/usr/bin:/usr/sbin"}
	setupSp.Cwd = "/"

	g.L.Info("running container setup")

	err = r.Exec(ctx, id, setupSp, &runc.ExecOpts{})
	if err != nil {
		return err
	}

	var target string

	for _, iface := range cniResult.Interfaces {
		target = iface.IPConfigs[0].IP.String()
		break
	}

	path := fmt.Sprintf("/proc/%d/net/tcp", pid)

	g.L.Info("monitoring for ports", "path", path, "target", target)
	go g.monitorPorts(ctx, info.Session, target, path)

	g.L.Warn("waiting for signal to stop container")

	select {
	case info.StartCh <- id:
		// ok
	case <-ctx.Done():
		return ctx.Err()
	}

	<-ctx.Done()

	ctx = context.Background()

	g.L.Warn("removing networking")
	err = g.cni.Remove(ctx, id, netPath)
	if err != nil {
		g.L.Error("error removing networking", "error", err)
	}

	g.L.Warn("removing container")

	err = r.Delete(ctx, id, &runc.DeleteOpts{
		Force: true,
	})

	if err != nil {
		g.L.Error("error removing container", "error", err)
	}

	return nil
}
