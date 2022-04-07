package vm

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/yamux"
	"github.com/lab47/yalr4m/pkg/bytesize"
	"github.com/lab47/yalr4m/pkg/timesync"
	"github.com/lab47/yalr4m/pkg/vz"
	"github.com/lab47/yalr4m/types"
	"golang.org/x/sys/unix"
)

type portListener struct {
	net.Listener
	Key string
}

type VM struct {
	L        hclog.Logger
	StateDir string
	Config   Config

	mu        sync.Mutex
	listeners map[int]portListener

	ownHomeLink    bool
	linuxHomePath  string
	linuxMountPath string

	mountOnce sync.Once
}

type RunningVM struct {
	Stdout io.ReadCloser
	Stdin  io.WriteCloser
}

type State struct {
	Running bool

	Info *RunningVM
}

func systemMemory() (uint64, error) {
	return unix.SysctlUint64("hw.memsize")
}

func (v *VM) Run(ctx context.Context, stateCh chan State, sigC chan os.Signal) error {
	u, err := user.Current()
	if err != nil {
		return err
	}

	homedir, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}

	vmlinuz := filepath.Join(v.StateDir, "vmlinux")
	initrd := filepath.Join(v.StateDir, "initrd")
	diskPath := filepath.Join(v.StateDir, "os.fs")
	dataPath := filepath.Join(v.StateDir, "data.fs")
	userPath := filepath.Join(v.StateDir, "user.fs")

	if _, err := os.Stat(vmlinuz); err != nil {
		return err
	}

	if _, err := os.Stat(initrd); err != nil {
		return err
	}

	if _, err := os.Stat(diskPath); err != nil {
		return err
	}

	sharePath := homedir

	cores := v.Config.Cores
	if cores == 0 {
		cores = runtime.NumCPU()
	}

	var memInBytes int

	mem := v.Config.Memory
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
			v.L.Warn("unable to calculate system memory, default to 1GB")
			memInBytes = 1 * bytesize.Gigabytes
		}
	}

	swap := v.Config.Swap
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

	kernelCommandLineArguments := []string{
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
		"cluster_id=" + v.Config.ClusterId,
		"user_name=" + u.Username,
		"user_uid=" + u.Uid,
		"user_gid=" + u.Gid,
	}

	bootLoader := vz.NewLinuxBootLoader(
		vmlinuz,
		vz.WithCommandLine(strings.Join(kernelCommandLineArguments, " ")),
		vz.WithInitrd(initrd),
	)

	v.L.Info("creating virtual machine", "cores", cores, "memory", mem)

	config := vz.NewVirtualMachineConfiguration(
		bootLoader,
		uint(cores),
		uint64(memInBytes),
	)

	vmr, hostw, err := os.Pipe()
	if err != nil {
		return err
	}

	hostr, vmw, err := os.Pipe()
	if err != nil {
		return err
	}

	result := &RunningVM{
		Stdin:  hostw,
		Stdout: hostr,
	}

	// console
	serialPortAttachment := vz.NewFileHandleSerialPortAttachment(vmr, vmw)
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

	hw, err := net.ParseMAC(v.Config.MacAddress)
	if err != nil {
		return err
	}

	networkConfig.SetMACAddress(vz.NewMACAddress(hw))

	// entropy
	entropyConfig := vz.NewVirtioEntropyDeviceConfiguration()
	config.SetEntropyDevicesVirtualMachineConfiguration([]*vz.VirtioEntropyDeviceConfiguration{
		entropyConfig,
	})

	diskImageAttachment, err := vz.NewDiskImageStorageDeviceAttachment(
		diskPath,
		true,
	)
	if err != nil {
		return err
	}
	storageDeviceConfig := vz.NewVirtioBlockDeviceConfiguration(diskImageAttachment)
	storageConfigs := []vz.StorageDeviceConfiguration{
		storageDeviceConfig,
	}

	if dataPath != "" {
		bs, err := bytesize.Parse(v.Config.DataSize)
		if err != nil {
			return err
		}

		size := bs.Bytes

		fi, err := os.Stat(dataPath)
		if err == nil {
			// support expanding only for now.
			if fi.Size() < size {
				f, err := os.OpenFile(userPath, os.O_WRONLY, fi.Mode().Perm())
				if err != nil {
					panic(err)
				}

				err = f.Truncate(size)
				if err != nil {
					panic(err)
				}

				f.Close()
			}
		} else {
			f, err := os.Create(dataPath)
			if err != nil {
				panic(err)
			}

			fmt.Fprintln(f, "data")

			err = f.Truncate(size)
			if err != nil {
				panic(err)
			}

			f.Close()
		}

		diskImageAttachment, err := vz.NewDiskImageStorageDeviceAttachment(
			dataPath,
			false,
		)
		if err != nil {
			log.Fatal(err)
		}

		storageConfigs = append(storageConfigs,
			vz.NewVirtioBlockDeviceConfiguration(diskImageAttachment))
	}

	if userPath != "" {
		bs, err := bytesize.Parse(v.Config.UserSize)
		if err != nil {
			return err
		}

		size := bs.Bytes

		fi, err := os.Stat(userPath)
		if err == nil {
			// support expanding only for now.
			if fi.Size() < size {
				f, err := os.OpenFile(userPath, os.O_WRONLY, fi.Mode().Perm())
				if err != nil {
					panic(err)
				}

				err = f.Truncate(size)
				if err != nil {
					panic(err)
				}

				f.Close()
			}
		} else {
			f, err := os.Create(userPath)
			if err != nil {
				panic(err)
			}

			fmt.Fprintln(f, "user")

			err = f.Truncate(size)
			if err != nil {
				panic(err)
			}

			f.Close()
		}

		diskImageAttachment, err := vz.NewDiskImageStorageDeviceAttachment(
			userPath,
			false,
		)
		if err != nil {
			log.Fatal(err)
		}

		storageConfigs = append(storageConfigs,
			vz.NewVirtioBlockDeviceConfiguration(diskImageAttachment))
	}

	config.SetStorageDevicesVirtualMachineConfiguration(storageConfigs)

	// traditional memory balloon device which allows for managing guest memory. (optional)
	config.SetMemoryBalloonDevicesVirtualMachineConfiguration([]vz.MemoryBalloonDeviceConfiguration{
		vz.NewVirtioTraditionalMemoryBalloonDeviceConfiguration(),
	})

	if sharePath != "" {
		fs := vz.NewVirtioFileSystemDeviceConfiguration("home")

		fs.SetDirectoryShare(
			vz.NewSingleDirectoryShare(
				vz.NewSharedDirectory(sharePath, false),
			),
		)

		config.SetDirectorySharingDevicesVirtualMachineConfiguration([]vz.DirectorySharingDeviceConfiguration{
			fs,
		})
	}

	// socket device (optional)
	config.SetSocketDevicesVirtualMachineConfiguration([]vz.SocketDeviceConfiguration{
		vz.NewVirtioSocketDeviceConfiguration(),
	})
	validated, err := config.Validate()
	if err != nil {
		return err
	}

	if !validated {
		return fmt.Errorf("VM config did not validate")
	}

	vm := vz.NewVirtualMachine(config)

	sock := vm.SocketDevices()[0]

	/*

		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}

				go func() {
					log.Printf("making connetion to vsock")
					sock.ConnectToPort(47, func(conn *vz.VirtioSocketConnection, err error) {
						defer c.Close()

						if err != nil {
							log.Printf("error making connection: %s", err)
						} else {
							log.Printf("connetion made to vsock")
							go io.Copy(c, conn)
							io.Copy(conn, c)
						}
					})
				}()
			}
		}()
	*/

	errCh := make(chan error, 1)

	vm.Start(func(err error) {
		if err != nil {
			errCh <- err
		}
	})

	ctx, cancel := context.WithCancel(ctx)

	defer cancel()

	ctrlC := make(chan types.ControlMessage, 1)

	var shutdown bool

	tick := time.NewTicker(10 * time.Second)

	monr, monw, err := os.Pipe()
	if err != nil {
		return err
	}

	result.Stdout = monr

	shutdownCh := make(chan struct{})

	go func() {
		br := bufio.NewReader(io.TeeReader(hostr, monw))

		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}

			if strings.HasSuffix(strings.TrimSpace(line), "reboot: System halted") {
				close(shutdownCh)
			}
		}
	}()

	defer v.cleanup()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-tick.C:
			if shutdown {
				v.L.Warn("timed out waiting for VM to shutdown")
				stateCh <- State{Running: false}
				return nil
			}
		case <-shutdownCh:
			time.Sleep(500 * time.Millisecond)
			v.L.Debug("stopped successfully")
			stateCh <- State{Running: false}
			return nil

		case <-sigC:
			v.L.Info("attempting to shutdown VM")

			ctrlC <- types.ControlMessage{
				HeaderMessage: types.HeaderMessage{
					Kind: "shutdown",
				},
			}

			result, err := vm.RequestStop()
			if err != nil {
				v.L.Debug("request stop error", "error", err)
				return err
			}

			v.L.Info("requested shutdown, waiting 10 seconds for safe shutdown", "result", result)

			tick.Reset(10 * time.Second)
			shutdown = true
		case newState := <-vm.StateChangedNotify():
			v.L.Info("observed vm state", "state", newState)

			if newState == vz.VirtualMachineStateRunning {
				v.L.Debug("start VM is running")

				listener, err := v.startListener(ctx, ctrlC)
				if err != nil {
					return err
				}

				sock.SetSocketListenerForPort(listener, 47)

				stateCh <- State{Running: true, Info: result}
			}

			if newState == vz.VirtualMachineStateStopped {
				v.L.Debug("stopped successfully")
				stateCh <- State{Running: false}
				return nil
			}
		case err := <-errCh:
			v.L.Info("error booting vm", "error", err)
			return err
		}
	}

	// vm.Resume(func(err error) {
	// 	fmt.Println("in resume:", err)
	// })
}

func (v *VM) cleanup() {
	out, err := exec.Command("umount", v.linuxMountPath).CombinedOutput()
	if err != nil {
		v.L.Error("error unmounting guest", "error", err, "output", string(out))
	}

	if v.ownHomeLink {
		os.Remove(v.linuxHomePath)
	}
}

func (v *VM) startListener(
	ctx context.Context,
	ctrlC chan types.ControlMessage,
) (*vz.VirtioSocketListener, error) {
	socketPath := filepath.Join(v.StateDir, "control.sock")
	os.Remove(socketPath)

	l, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}

	type newConn struct {
		conn *vz.VirtioSocketConnection
		wait chan struct{}
	}

	connCh := make(chan newConn)

	go func() {
		var (
			conn      *vz.VirtioSocketConnection
			wait      chan struct{}
			sess      *yamux.Session
			curHostCh chan net.Conn
			hostCh    = make(chan net.Conn)
		)

		go func() {
			defer l.Close()

			for {
				c, err := l.Accept()
				if err != nil {
					if ne, ok := err.(net.Error); ok {
						if ne.Temporary() || ne.Timeout() {
							continue
						}
					}
					return
				}

				v.L.Debug("accepted new client on host side")

				hostCh <- c
			}
		}()

		for {
			select {
			case <-ctx.Done():
				l.Close()
				return
			case ci := <-connCh:
				if wait != nil {
					wait <- struct{}{}
				}

				conn = ci.conn
				wait = ci.wait

				if sess != nil {
					sess.Close()
				}

				cfg := yamux.DefaultConfig()
				cfg.EnableKeepAlive = true
				cfg.AcceptBacklog = 10

				sess, _ = yamux.Client(conn, cfg)
				v.L.Debug("connected yamux to guest")

				// This will make it so now we start to recieve any socket connections
				// from the CLI
				curHostCh = hostCh

				go v.timesync(ctx, sess)
				go v.handleFromGuest(ctx, sess)
			case c := <-curHostCh:
				if sess == nil {
					v.L.Error("attempted connection to session before started")
					c.Close()
					continue
				}

				v.L.Info("starting bridge...")

				out, err := sess.Open()
				if err != nil {
					v.L.Error("error opening for yamux", "error", err)
					c.Close()
					continue
				}

				v.L.Info("bridging connection...")

				go func() {
					defer c.Close()
					defer out.Close()

					_, err := io.Copy(out, c)
					v.L.Info("closing down bridge 1", "error", err)
				}()

				go func() {
					defer c.Close()
					defer out.Close()

					_, err := io.Copy(c, out)
					v.L.Info("closing down bridge 2", "error", err)
				}()
			case msg := <-ctrlC:
				v.L.Debug("sending control message")

				if sess == nil {
					v.L.Warn("attempted to send control message before connection was made")
					continue
				}

				out, err := sess.Open()
				if err != nil {
					v.L.Error("error opening session for control", "error", err)
					continue
				}

				func() {
					defer out.Close()

					out.Write([]byte{types.ProtocolByte})

					enc := cbor.NewEncoder(out)
					dec := cbor.NewDecoder(out)

					enc.Encode(msg.HeaderMessage)

					var resp types.ResponseMessage
					dec.Decode(&resp)

					if resp.Code != types.OK {
						v.L.Error("error processing control message", "error", resp.Error)
					}
				}()
			}
		}
	}()

	listener := vz.NewVirtioSocketListener(func(conn *vz.VirtioSocketConnection, err error) {
		if err != nil {
			return
		}

		defer conn.Close()

		v.L.Info("connection from virtio socket detected")

		wait := make(chan struct{})

		connCh <- newConn{conn, wait}

		<-wait
	})

	return listener, nil
}

func (v *VM) timesync(ctx context.Context, sess *yamux.Session) {
	for {
		out, err := sess.Open()
		if err != nil {
			v.L.Error("error opening session for control", "error", err)
			return
		}

		out.Write([]byte{types.ProtocolByte})

		enc := cbor.NewEncoder(out)
		dec := cbor.NewDecoder(out)

		enc.Encode(types.HeaderMessage{
			Kind: "timesync",
		})

		var resp types.ResponseMessage

		dec.Decode(&resp)

		if resp.Code != types.OK {
			v.L.Error("error confirming timesync channel", "error", resp.Error)
			time.Sleep(10 * time.Second)
			continue
		}

		v.L.Info("beginning host timesync loop")

		timesync.Host(ctx, v.L, out)
	}
}

func (v *VM) handleFromGuest(ctx context.Context, sess *yamux.Session) {
	for {
		c, err := sess.AcceptStream()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				v.L.Warn("unable to accept new incoming yamux streams", "error", err)
			}
			return
		}

		go v.handleGuestConn(c)
	}
}

func (v *VM) handleGuestConn(c *yamux.Stream) {
	defer c.Close()

	enc := cbor.NewEncoder(c)
	dec := cbor.NewDecoder(c)

	var msg types.HeaderMessage

	err := dec.Decode(&msg)
	if err != nil {
		v.L.Error("error decoding guest message", "error", err)
		return
	}

	v.L.Debug("received event from guest", "kind", msg.Kind)

	switch msg.Kind {
	default:
		var resp types.ResponseMessage

		resp.Code = types.Error
		resp.Error = fmt.Sprintf("unknown event kind")

		err = enc.Encode(resp)
		if err != nil {
			v.L.Error("error encoding response", "error", err)
		}
	case "running":
		var pm types.RunningMessage
		err = dec.Decode(&pm)
		if err != nil {
			v.L.Error("error decoding port forward message", "error", err)
			return
		}

		v.L.Info("detected guest running", "ip", pm.IP)

		v.mountOnce.Do(func() {
			v.mountLinux(pm.IP)
		})
	case "cancel-port-forward":
		var pm types.PortForwardMessage
		err = dec.Decode(&pm)
		if err != nil {
			v.L.Error("error decoding port forward message", "error", err)
			return
		}

		v.mu.Lock()
		if l, ok := v.listeners[pm.Port]; ok {
			l.Close()
		}
		v.mu.Unlock()

		var resp types.ResponseMessage
		resp.Code = types.OK

		err = enc.Encode(resp)
		if err != nil {
			v.L.Error("error encoding response", "error", err)
		}

		v.L.Debug("removed port forwarder", "port", pm.Port)

	case "port-forward":
		var pm types.PortForwardMessage
		err = dec.Decode(&pm)
		if err != nil {
			v.L.Error("error decoding port forward message", "error", err)
			return
		}

		var resp types.ResponseMessage

		l, err := net.Listen("tcp", fmt.Sprintf(":%d", pm.Port))
		if err != nil {
			v.L.Error("unable to listen on port", "error", err)
			resp.Code = types.Error
			resp.Error = err.Error()
		} else {
			resp.Code = types.OK
		}

		v.mu.Lock()
		if v.listeners == nil {
			v.listeners = make(map[int]portListener)
		}

		v.listeners[pm.Port] = portListener{Listener: l, Key: pm.Key}
		v.mu.Unlock()

		err = enc.Encode(resp)
		if err != nil {
			v.L.Error("error encoding response", "error", err)
		}

		v.L.Debug("setup port forwarder", "port", pm.Port)
		go v.forwardPort(pm.Port, pm.Key, l, c.Session())

	case "ssh-agent":
		path := os.Getenv("SSH_AUTH_SOCK")
		if path == "" {
			enc.Encode(types.ResponseMessage{
				Code:  types.Error,
				Error: "no local agent",
			})

			v.L.Error("no SSH_AUTH_SOCK set, rejecting connection")
			c.Close()
			return
		}

		local, err := net.Dial("unix", path)
		if err != nil {
			enc.Encode(types.ResponseMessage{
				Code:  types.Error,
				Error: "local agent rejected connection",
			})

			v.L.Error("error connecting to ssh agent", "error", err)
			c.Close()
			return
		}

		v.L.Info("forwarding connection to ssh-agent", "path", path)

		enc.Encode(types.ResponseMessage{
			Code: types.OK,
		})

		go func() {
			defer local.Close()
			defer c.Close()

			io.Copy(c, local)

			v.L.Info("ssh-agent session ended 1")
		}()

		defer c.Close()
		defer local.Close()

		io.Copy(local, c)

		v.L.Info("ssh-agent session ended 2")
	}
}

func (v *VM) forwardPort(port int, key string, l net.Listener, sess *yamux.Session) {
	defer l.Close()

	for {
		c, err := l.Accept()
		if err != nil {
			v.L.Error("error accepting new connections for forwarding", "error", err)
			return
		}

		guest, err := sess.Open()
		if err != nil {
			v.L.Error("error connection to guest for forwarding", "error", err)
			continue
		}

		guest.Write([]byte{types.ProtocolByte})
		enc := cbor.NewEncoder(guest)
		dec := cbor.NewDecoder(guest)

		err = enc.Encode(types.HeaderMessage{
			Kind: "port-forward",
		})
		if err != nil {
			v.L.Error("error sending forwarding start message", "error", err)
			continue
		}

		err = enc.Encode(types.PortForwardMessage{
			Port: port,
			Key:  key,
		})
		if err != nil {
			v.L.Error("error sending forwarding start message", "error", err)
			continue
		}

		var resp types.ResponseMessage
		err = dec.Decode(&resp)
		if err != nil {
			v.L.Error("error decoding forwarding start message", "error", err)
			continue
		}

		if resp.Code != types.OK {
			v.L.Error("guest rejected port forward", "error", resp.Error)
			continue
		}

		v.L.Debug("forwarding port to guest", "port", port)

		go func() {
			defer guest.Close()
			defer c.Close()

			io.Copy(c, guest)
		}()

		go func() {
			defer c.Close()
			defer guest.Close()

			io.Copy(guest, c)
		}()
	}
}

func (v *VM) mountLinux(ip string) {
	// lazy way to let smbd boot up first
	time.Sleep(time.Second)

	path := filepath.Join(v.StateDir, "linux")

	os.MkdirAll(path, 0755)

	var mounted bool

	for i := 0; i < 100; i++ {
		cmd := exec.Command("mount", "-t", "smbfs", fmt.Sprintf("//macstorage:mac@%s/storage", ip), path)
		out, err := cmd.CombinedOutput()
		if err == nil {
			mounted = true
			break
		}

		v.L.Error("unable to mount linux", "error", err, "output", string(out))
		time.Sleep(time.Second)
	}

	if !mounted {
		v.L.Error("error timed out trying to mount guest")
		return
	}

	v.linuxMountPath = path

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}

	homePath := filepath.Join(homeDir, "linux")

	if _, err := os.Lstat(homePath); os.IsNotExist(err) {
		os.Symlink(path, homePath)
		v.ownHomeLink = true
		v.linuxHomePath = homePath
	}
}
