package guest

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/go-cni"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/davecgh/go-spew/spew"
	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/pkg/clog"
	"github.com/lab47/isle/pkg/netutil"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
)

func TestNetworking(t *testing.T) {
	t.Run("can setup a static cni config", func(t *testing.T) {
		ce, err := CNIEnv()
		require.NoError(t, err)

		ll, err := netutil.StaticConfigList(
			ce, 100, "10.101.1.2/24", "fd0c:731a:b056:5355:dd7d:769b:3569:25f5/64")
		require.NoError(t, err)

		_, err = cni.New(
			cni.WithPluginDir([]string{ce.Path}),
			cni.WithConfListBytes(ll.Bytes),
		)
		require.NoError(t, err)
	})
}

func TestContainers(t *testing.T) {
	t.Run("can spawn a container", func(t *testing.T) {
		log := hclog.New(&hclog.LoggerOptions{
			Name:  "testspawn",
			Level: hclog.Trace,
		})

		home, err := os.UserHomeDir()
		require.NoError(t, err)

		homeTmp := filepath.Join(home, "tmp")
		os.MkdirAll(homeTmp, 0755)

		dir, err := os.MkdirTemp(homeTmp, "cont")
		require.NoError(t, err)

		os.MkdirAll(dir, 0755)

		defer os.RemoveAll(dir)

		tf, err := os.CreateTemp("", "rs")
		require.NoError(t, err)

		defer os.Remove(tf.Name())

		tf.Close()

		db, err := bbolt.Open(tf.Name(), 0644, bbolt.DefaultOptions)
		require.NoError(t, err)

		defer db.Close()

		var r ResourceStorage
		r.log = log

		err = r.Init(db)
		require.NoError(t, err)

		top, cancel := context.WithCancel(context.Background())
		defer cancel()

		ctx := &ResourceContext{
			Context:         top,
			ResourceStorage: &r,
		}

		runDir, err := os.MkdirTemp("/tmp", "testc")
		require.NoError(t, err)

		defer os.RemoveAll(runDir)

		runcDir := filepath.Join(runDir, "runc")

		var ipm IPNetworkManager

		err = ipm.Init(ctx)
		require.NoError(t, err)

		v6net, err := IPv6Base(newHexId(6), newHexId(2))
		require.NoError(t, err)

		ipn := &guestapi.IPNetwork{
			Name:        "default",
			Ipv4Block:   guestapi.MustParseCIDR("10.100.0.0/24"),
			Ipv4Gateway: guestapi.MustParseCIDR("10.100.0.1/32"),
			Ipv6Block:   guestapi.ToIPAddress(v6net),
			Ipv6Gateway: guestapi.FromNetIP(FirstAddress(v6net)),
		}

		resip, err := ipm.Create(ctx, ipn)
		require.NoError(t, err)

		var cm ContainerManager

		layerCache := filepath.Join(homeTmp, "layer-cache")
		os.MkdirAll(layerCache, 0755)

		err = cm.Init(ctx, &ContainerConfig{
			Logger:  log,
			BaseDir: dir,
			HomeDir: runDir,
			NodeId:  newUniqueId(),
			// ClusterId: newUniqueId(),
			// SubnetId:  newHexId(2),
			User:     "test",
			RuncRoot: runcDir,
			RunDir:   runDir,

			LayerCacheDir: layerCache,

			BridgeID: 2,

			HelperPath:     "/usr/bin/isle",
			NetworkManager: &ipm,
		})
		require.NoError(t, err)

		defer cm.Close()

		go cm.StartSSHAgent(ctx)

		cont := &guestapi.Container{
			Image:    "alpine",
			Networks: []*guestapi.ResourceId{resip.Id},
		}

		log.Info("booting new container")

		res, err := cm.Create(ctx, cont)
		require.NoError(t, err)

		// Use a closure here so we pick up any updates to res,
		// which can occur in the wait loop below.
		defer func() {
			cm.Delete(ctx, res, cont)
		}()

		assert.Equal(t, guestapi.ProvisionStatus_ADDING, res.ProvisionStatus.Status)

		log.Info("Waiting for container to start")

		var running bool

		for i := 0; i < 20; i++ {
			res, err = cm.Read(ctx, res.Id)
			require.NoError(t, err)

			spew.Dump(res.ProvisionStatus)

			if res.ProvisionStatus.Status == guestapi.ProvisionStatus_RUNNING {
				running = true
				break
			} else if res.ProvisionStatus.Status != guestapi.ProvisionStatus_ADDING {
				break
			}

			time.Sleep(time.Second)
		}

		require.True(t, running, "container never started")

		proc := specs.Process{
			Cwd:  "/",
			Args: []string{"sh", "-c", "echo 'hello from container'"},
			Env: []string{
				"PATH=/bin:/usr/bin:/sbin:/usr/sbin:/usr/local/bin:/usr/local/sbin:/run/share/bin:/run/share/sbin:/opt/isle/bin",
			},
		}

		output, err := cm.Exec(ctx, res, proc)
		require.NoError(t, err)

		assert.Equal(t, "hello from container\n", string(output))

		x, err := ns.GetNS(fmt.Sprintf("/proc/%d/ns/net", res.ProvisionStatus.ContainerInfo.InitPid))
		require.NoError(t, err)

		var addrs []net.Addr

		ni := res.ProvisionStatus.NetworkInfo

		lookingFor := map[string]struct{}{
			ni.Addresses[0].Canonical().String(): {},
			ni.Addresses[1].Canonical().String(): {},
		}

		err = x.Do(func(nn ns.NetNS) error {
			iface, ierr := net.InterfaceByName("eth0")
			if ierr != nil {
				return err
			}

			addrs, err = iface.Addrs()
			if err != nil {
				return err
			}

			spew.Dump(addrs)

			for _, addr := range addrs {
				if ipnet, ok := addr.(*net.IPNet); ok {
					delete(lookingFor, ipnet.String())
				}
			}

			return nil
		})

		require.NoError(t, err)

		assert.Empty(t, lookingFor, "not all addresess were bound")

		ch := make(chan *clog.Entry)

		err = cm.Logs(ctx, res, ch)
		require.NoError(t, err)

		var logs []*clog.Entry

		for ent := range ch {
			logs = append(logs, ent)
		}

		assert.Len(t, logs, 4)

		assert.Contains(t, logs[0].Data, "creating container")
		assert.Contains(t, logs[3].Data, "container running")
	})
}
