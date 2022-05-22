package guest

import (
	"bufio"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/yamux"
	"github.com/lab47/isle/guestapi"
	"golang.org/x/sys/unix"
)

type portMonitor struct {
	id      *guestapi.ResourceId
	log     hclog.Logger
	sess    *yamux.Session
	api     guestapi.HostAPIClient
	network *guestapi.NetworkInfo
	target  string
	path    string
}

func (pm *portMonitor) start(ctx *ResourceContext) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	ports := map[int64]struct{}{}

	sigCh := make(chan os.Signal, 1)

	signal.Notify(sigCh, unix.SIGHUP)

	defer signal.Reset(unix.SIGHUP)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pm.readPorts(ctx, ports)
		case <-sigCh:
			pm.readPorts(ctx, ports)
		}
	}
}

func (pm *portMonitor) readPorts(ctx *ResourceContext, ports map[int64]struct{}) {
	f, err := os.Open(pm.path)
	if err != nil {
		pm.log.Error("error reading tcp listing", "error", err, "path", pm.path)
		return
	}

	defer f.Close()

	br := bufio.NewReader(f)

	// discard header line
	br.ReadString('\n')

	curPorts := map[int64]struct{}{}

	var changes bool

	for {
		line, err := br.ReadString('\n')
		if err != nil {
			break
		}

		parts := strings.Fields(line)

		local := parts[1]
		remote := parts[2]

		if remote != "00000000:0000" {
			continue
		}

		colon := strings.IndexByte(local, ':')
		if colon == -1 {
			continue
		}

		addr := local[:colon]
		port := local[colon+1:]

		if addr != "00000000" {
			continue
		}

		numPort, err := strconv.ParseInt(port, 16, 64)
		if err != nil {
			pm.log.Error("error parsing port", "error", err, "port", port)
			continue
		}

		curPorts[numPort] = struct{}{}

		if _, ok := ports[numPort]; ok {
			continue
		}

		changes = true

		pm.log.Info("requesting port to be forwarded", "port", numPort)

		_, err = pm.api.StartPortForward(ctx, &guestapi.StartPortForwardReq{
			Port: int32(numPort),
			Key:  pm.target,
		})

		if err != nil {
			pm.log.Error("error setting up port forwarding", "error", err)
			continue
		}

		pm.network.Ports = append(pm.network.Ports, int32(numPort))

		pm.log.Info("confirmed port being forwarded", "port", numPort)
		ports[numPort] = struct{}{}
	}

	for p := range ports {
		if _, ok := curPorts[p]; !ok {
			// cancel the forwarder

			delete(ports, p)
			changes = true

			_, err = pm.api.StartPortForward(ctx, &guestapi.StartPortForwardReq{
				Port: int32(p),
				Key:  pm.target,
			})

			if err != nil {
				pm.log.Error("host reported error canceling port", "error", err)
				continue
			}

			pm.log.Info("canceled port forward with host")
		}
	}

	if changes {
		var portList []int32

		for k := range ports {
			portList = append(portList, int32(k))
		}

		sort.Slice(portList, func(i, j int) bool {
			return portList[i] < portList[j]
		})

		pm.network.Ports = portList

		ctx.UpdateProvision(ctx, pm.id, &guestapi.ProvisionStatus{
			NetworkInfo: pm.network,
		})
	}
}
