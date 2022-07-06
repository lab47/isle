package macos

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/cli/config"
	"github.com/lab47/isle/client"
	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/host"
	"github.com/lab47/isle/pkg/bytesize"
	"github.com/lab47/isle/pkg/labels"
	"github.com/lab47/isle/pkg/pbstream"
	"github.com/lab47/isle/pkg/timesync"
	"github.com/lab47/isle/pkg/vz"

	"golang.org/x/sys/unix"
)

type VM struct {
	log hclog.Logger

	stateDir string
	cfg      config.Config

	user *user.User

	sharePath  string
	kernelPath string
	initrdPath string
	osPath     string
	dataPath   string
	userPath   string

	cores         int
	memory        int
	kernelCmdLine []string

	totalMemory   int64
	currentMemory int64

	storageConfigs []vz.StorageDeviceConfiguration

	hostReader io.Reader
	hostWriter io.Writer

	consoleWriter io.Writer
	consoleReader io.Reader

	shutdownCh chan struct{}

	connector *client.Connector

	vmConsoleReader *os.File
	vmConsoleWriter *os.File

	eventCh chan Event
}

type EventType int

const (
	VMStarted EventType = iota
	VMStopped
)

type Event struct {
	Type EventType
}

func NewVM(log hclog.Logger, stateDir string, cfg config.Config) (*VM, error) {
	vm := &VM{
		log:      log,
		stateDir: stateDir,
		cfg:      cfg,
	}

	vmr, hostw, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	vm.vmConsoleReader = vmr
	vm.hostWriter = hostw

	hostr, vmw, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	vm.hostReader = hostr
	vm.vmConsoleWriter = vmw

	return vm, nil
}

func (v *VM) SetConsoleIO(r io.Reader, w io.Writer) {
	v.consoleReader = r
	v.consoleWriter = w
}

func systemMemory() (uint64, error) {
	return unix.SysctlUint64("hw.memsize")
}

func (v *VM) Setup() error {
	v.log.Trace("performing vm setup")
	u, err := user.Current()
	if err != nil {
		return err
	}

	v.user = u

	homedir, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}

	v.log.Trace("discovered user info", "home-dir", homedir)

	vmlinuz := filepath.Join(v.stateDir, "vmlinux")
	initrd := filepath.Join(v.stateDir, "initrd")
	diskPath := filepath.Join(v.stateDir, "os.fs")
	dataPath := filepath.Join(v.stateDir, "data.fs")
	userPath := filepath.Join(v.stateDir, "user.fs")

	if _, err := os.Stat(vmlinuz); err != nil {
		return err
	}

	if _, err := os.Stat(initrd); err != nil {
		return err
	}

	if _, err := os.Stat(diskPath); err != nil {
		return err
	}

	v.kernelPath = vmlinuz
	v.initrdPath = initrd
	v.osPath = diskPath
	v.dataPath = dataPath
	v.userPath = userPath
	v.sharePath = homedir

	v.cores = v.cfg.Cores
	if v.cores == 0 {
		v.cores = runtime.NumCPU()
	}

	var memInBytes int

	mem := v.cfg.Memory
	if mem != "" {
		bs, err := bytesize.Parse(mem)
		if err != nil {
			return err
		}

		memInBytes = int(bs.Bytes)
	} else {
		sysmem, err := systemMemory()
		if err == nil {
			memInBytes = int(sysmem / 2)
		} else {
			v.log.Warn("unable to calculate system memory, default to 1GB")
			memInBytes = 1 * bytesize.Gigabytes
		}
	}

	v.memory = memInBytes

	swap := v.cfg.Swap
	if swap != "" {
		_, err := bytesize.Parse(swap)
		if err != nil {
			return err
		}
	} else {
		swapBytes := int(memInBytes / bytesize.Gigabytes)
		// There are many opinions about how to allocate swap.
		// It used to be 2x, but most don't do that anymore?
		// And this is a VM, so we want to be a little conservative
		// because the user can always set it high if the need later.

		switch {
		case swapBytes == 0: // under a gig of memory
			swap = fmt.Sprintf("%dM", memInBytes/bytesize.Megabytes)
		case swapBytes <= 8: // up to 4GB of ram, have it equal the ram
			swap = fmt.Sprintf("%dG", memInBytes/bytesize.Gigabytes)
		default:
			swap = "8G"
		}
	}

	v.log.Debug("vm configured",
		"cores", v.cores,
		"ram", v.memory,
		"swap", swap,
		"cluster-id", v.cfg.ClusterId,
	)

	v.kernelCmdLine = []string{
		// Use the first virtio console device as system console.
		"console=hvc0",
		// Stop in the initial ramdisk before attempting to transition to
		// the root file system.
		"root=/dev/vda",
		"acpi=on",
		"mitigations=off",
		// "acpi.debug_layer=0x2",
		// "acpi.debug_level=0xffffffff",
		"overlaytmpfs",
		"swap=" + swap,
		"data=/dev/vdb",     // don't love assuming this
		"vol_user=/dev/vdc", // don't love assuming this
		"share_home=home",
		"cluster_id=" + v.cfg.ClusterId,
		"user_name=" + u.Username,
		"user_uid=" + u.Uid,
		"user_gid=" + u.Gid,
	}

	return nil
}

func (v *VM) SetEventCh(eventCh chan Event) {
	v.eventCh = eventCh
}

func (v *VM) sendEvent(ev Event) {
	if v.eventCh == nil {
		return
	}

	go func() {
		v.eventCh <- ev
	}()
}

func (v *VM) Run(ctx context.Context) error {
	err := v.Setup()
	if err != nil {
		return err
	}

	bootLoader := vz.NewLinuxBootLoader(
		v.kernelPath,
		vz.WithCommandLine(strings.Join(v.kernelCmdLine, " ")),
		vz.WithInitrd(v.initrdPath),
	)

	v.log.Info("creating virtual machine", "cores", v.cores, "memory", v.memory)

	config := vz.NewVirtualMachineConfiguration(
		bootLoader,
		uint(v.cores),
		uint64(v.memory),
	)

	v.totalMemory = int64(v.memory)
	v.currentMemory = v.totalMemory

	err = v.attachIO(config)
	if err != nil {
		return err
	}

	err = v.attachDisks(config)
	if err != nil {
		return err
	}

	err = v.attachMisc(config)
	if err != nil {
		return err
	}

	err = v.attachShare(config)
	if err != nil {
		return err
	}

	validated, err := config.Validate()
	if err != nil {
		return err
	}

	if !validated {
		return fmt.Errorf("VM config did not validate")
	}

	vm := vz.NewVirtualMachine(config)

	sock := vm.SocketDevices()[0]

	// memoryBalloon := vm.MemoryBalloonDevices()[0]

	errCh := make(chan error, 1)

	if v.consoleWriter == nil {
		f, err := os.Create(filepath.Join(v.stateDir, "vm.log"))
		if err != nil {
			return err
		}

		defer f.Close()

		v.consoleWriter = f
	}

	go v.setupGuestListener(ctx, sock)
	go v.startSessionListener(filepath.Join(v.stateDir, "session.sock"), sock)

	vm.Start(func(err error) {
		errCh <- err
	})

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		if err != nil {
			return err
		}
	}

	v.shutdownCh = make(chan struct{})

	go v.handleConsole()

	if v.consoleReader != nil {
		go io.Copy(v.hostWriter, v.consoleReader)
	}

	// go v.connectToGuest(ctx, sock)

	for {
		select {
		case <-ctx.Done():
			v.log.Info("attempting to shutdown VM")

			sub := context.Background()

			sub, cancel := signal.NotifyContext(sub, unix.SIGTERM, unix.SIGQUIT)
			defer cancel()

			v.requestShutdown(sub, sock)

			for {
				select {
				case <-sub.Done():
					v.log.Info("shutdown wait aborted")
					return nil
				case <-v.shutdownCh:
					v.log.Info("vm has shutdown")
					return nil
				}
			}
		case <-v.shutdownCh:
			v.log.Info("vm has shutdown")
			return nil
		case newState := <-vm.StateChangedNotify():
			v.log.Info("observed vm state", "state", newState)

			if newState == vz.VirtualMachineStateRunning {
				v.log.Debug("start VM is running")
			}

			if newState == vz.VirtualMachineStateStopped {
				v.log.Debug("stopped successfully")
				v.sendEvent(Event{
					Type: VMStarted,
				})
				return nil
			}

		}
	}

	return nil
}

func (v *VM) handleConsole() error {
	br := bufio.NewReader(io.TeeReader(v.hostReader, v.consoleWriter))

	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil
		}

		if strings.HasSuffix(strings.TrimSpace(line), "reboot: System halted") {
			v.log.Info("detected VM shutdown")
			close(v.shutdownCh)
		}
	}
}

func (v *VM) requestShutdown(ctx context.Context, sockdev *vz.VirtioSocketDevice) error {
	var outer error

	sockdev.ConnectToPort(47, func(conn *vz.VirtioSocketConnection, localErr error) {
		if localErr != nil {
			v.log.Error("error connecting to guest", "error", localErr)
			outer = localErr
			return
		}

		if conn == nil {
			v.log.Error("no connection set")
			outer = fmt.Errorf("error connecting to socket device port")
			return
		}

		defer conn.Close()

		client, err := host.ConnectIO(v.log, conn)
		if err != nil {
			outer = err
			return
		}

		vmClient := guestapi.PBSNewVMAPIClient(pbstream.StreamOpenFunc(func() (*pbstream.Stream, error) {
			rs, _, err := client.Open(labels.New("component", "vm"))
			return rs, err
		}))

		_, err = vmClient.RequestShutdown(ctx, pbstream.NewRequest(&guestapi.Empty{}))
		if err != nil {
			outer = err
			return
		}

		return
	})

	return outer
}

func (v *VM) VMRunning(ctx context.Context, req *pbstream.Request[guestapi.RunningReq]) (*pbstream.Response[guestapi.RunningResp], error) {
	var tz string

	path, err := os.Readlink("/etc/localtime")
	if err == nil {
		idx := strings.Index(path, "/zoneinfo/")
		if idx != -1 {
			tz = path[idx+10:]
		}
	}

	v.log.Info("vm running", "ip", req.Value.Ip, "timezone", tz)

	v.sendEvent(Event{
		Type: VMStarted,
	})

	return pbstream.NewResponse(&guestapi.RunningResp{
		Timezone: tz,
	}), nil
}

func (v *VM) setupGuestListener(ctx context.Context, sockdev *vz.VirtioSocketDevice) error {
	_, startH := guestapi.PBSNewStartupAPIHandler(v)

	handler := pbstream.CombineHandlers(
		startH, timesync.NewServerHandler(v.log),
	)

	listener := vz.NewVirtioSocketListener(func(conn *vz.VirtioSocketConnection, err error) {
		if err != nil {
			v.log.Error("error accepting new listener connection", "error", err)
			return
		}

		defer conn.Close()

		v.log.Info("connection from virtio socket detected")

		rs, err := pbstream.Open(v.log, conn)
		if err != nil {
			v.log.Error("error opening pbstream", "error", err)
			return
		}

		err = handler.HandleRPC(ctx, rs)
		if err != nil {
			v.log.Error("error handling rpc for guest", "error", err)
			return
		}
	})

	sockdev.SetSocketListenerForPort(listener, 48)

	return nil
}

func (v *VM) startSessionListener(addr string, sockdev *vz.VirtioSocketDevice) error {
	v.log.Info("starting multiplex session", "addr", addr)

	os.Remove(addr)

	uaddr := &net.UnixAddr{Name: addr, Net: "unix"}

	l, err := net.ListenUnix("unix", uaddr)
	if err != nil {
		return err
	}

	for {
		local, err := l.AcceptUnix()
		if err != nil {
			return err
		}

		v.log.Info("connection to multiplexer")

		err = func() error {
			// defer local.Close()

			/*
				var (
					remote *vz.VirtioSocketConnection
					err    error
				)
			*/

			v.log.Trace("connecting to guest", "virtio-port", 47)

			sockdev.ConnectToPort(47, func(remote *vz.VirtioSocketConnection, localErr error) {
				defer local.Close()

				v.log.Trace("called connect-to-port")

				if err != nil {
					v.log.Error("error connecting to guest", "error", err)
					return
				}

				if remote == nil {
					v.log.Error("no connection set")
					return
				}

				defer remote.Close()

				localRaw, err := local.SyscallConn()
				if err != nil {
					v.log.Error("error getting raw local descriptor", "error", err)
					return
				}

				remoteFd := remote.FileDescriptor()

				rights := unix.UnixRights(int(remoteFd))

				localRaw.Control(func(fd uintptr) {
					err := unix.Sendmsg(int(fd), nil, rights, nil, 0)
					if err != nil {
						v.log.Error("error passing fd", "error", err)
					}
				})

				v.log.Info("passed virtio fd to client", "fd", remoteFd)
			})

			return nil
		}()

		if err != nil {
			v.log.Error("error establishing session via multiplexer", "error", err)
		}
	}
}

func (v *VM) attachIO(config *vz.VirtualMachineConfiguration) error {
	serialPortAttachment := vz.NewFileHandleSerialPortAttachment(v.vmConsoleReader, v.vmConsoleWriter)
	consoleConfig := vz.NewVirtioConsoleDeviceSerialPortConfiguration(serialPortAttachment)
	config.SetSerialPortsVirtualMachineConfiguration([]*vz.VirtioConsoleDeviceSerialPortConfiguration{
		consoleConfig,
	})

	// network
	natAttachment := vz.NewNATNetworkDeviceAttachment()
	networkConfig := vz.NewVirtioNetworkDeviceConfiguration(natAttachment)
	config.SetNetworkDevicesVirtualMachineConfiguration([]*vz.VirtioNetworkDeviceConfiguration{
		networkConfig,
	})

	hw, err := net.ParseMAC(v.cfg.MacAddress)
	if err != nil {
		return err
	}

	networkConfig.SetMACAddress(vz.NewMACAddress(hw))

	return nil
}

func (v *VM) setupDisk(config *vz.VirtualMachineConfiguration, path, strsize, name string) error {
	if path == "" {
		return nil
	}

	bs, err := bytesize.Parse(strsize)
	if err != nil {
		return err
	}

	size := bs.Bytes

	fi, err := os.Stat(path)
	if err == nil {
		// support expanding only for now.
		if fi.Size() < size {
			f, err := os.OpenFile(path, os.O_WRONLY, fi.Mode().Perm())
			if err != nil {
				return err
			}

			err = f.Truncate(size)
			if err != nil {
				return err
			}

			f.Close()
		}
	} else {
		f, err := os.Create(path)
		if err != nil {
			panic(err)
		}

		fmt.Fprintln(f, name)

		err = f.Truncate(size)
		if err != nil {
			return err
		}

		f.Close()
	}

	diskImageAttachment, err := vz.NewDiskImageStorageDeviceAttachment(
		path,
		false,
	)
	if err != nil {
		return err
	}

	v.storageConfigs = append(v.storageConfigs,
		vz.NewVirtioBlockDeviceConfiguration(diskImageAttachment),
	)

	return nil
}

func (v *VM) attachDisks(config *vz.VirtualMachineConfiguration) error {
	diskImageAttachment, err := vz.NewDiskImageStorageDeviceAttachment(
		v.osPath,
		true,
	)
	if err != nil {
		return err
	}
	storageDeviceConfig := vz.NewVirtioBlockDeviceConfiguration(diskImageAttachment)
	v.storageConfigs = []vz.StorageDeviceConfiguration{
		storageDeviceConfig,
	}

	if v.dataPath != "" {
		err = v.setupDisk(config, v.dataPath, v.cfg.DataSize, "data")
		if err != nil {
			return err
		}
	}

	if v.userPath != "" {
		err = v.setupDisk(config, v.userPath, v.cfg.UserSize, "user")
		if err != nil {
			return err
		}
	}

	config.SetStorageDevicesVirtualMachineConfiguration(v.storageConfigs)

	return nil
}

func (v *VM) attachMisc(config *vz.VirtualMachineConfiguration) error {
	config.SetMemoryBalloonDevicesVirtualMachineConfiguration([]vz.MemoryBalloonDeviceConfiguration{
		vz.NewVirtioTraditionalMemoryBalloonDeviceConfiguration(),
	})

	config.SetSocketDevicesVirtualMachineConfiguration([]vz.SocketDeviceConfiguration{
		vz.NewVirtioSocketDeviceConfiguration(),
	})

	// entropy
	entropyConfig := vz.NewVirtioEntropyDeviceConfiguration()
	config.SetEntropyDevicesVirtualMachineConfiguration([]*vz.VirtioEntropyDeviceConfiguration{
		entropyConfig,
	})

	return nil
}

func (v *VM) attachShare(config *vz.VirtualMachineConfiguration) error {
	if v.sharePath == "" {
		return nil
	}

	fs := vz.NewVirtioFileSystemDeviceConfiguration("home")

	fs.SetDirectoryShare(
		vz.NewSingleDirectoryShare(
			vz.NewSharedDirectory(v.sharePath, false),
		),
	)

	config.SetDirectorySharingDevicesVirtualMachineConfiguration([]vz.DirectorySharingDeviceConfiguration{
		fs,
	})

	return nil
}
