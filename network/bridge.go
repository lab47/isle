package network

import (
	"context"
	"crypto/sha512"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/hashicorp/go-hclog"

	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/plugins/pkg/ip"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/utils/sysctl"
	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
)

func bridgeByName(name string) (*netlink.Bridge, error) {
	l, err := netlink.LinkByName(name)
	if err != nil {
		return nil, fmt.Errorf("could not lookup %q: %v", name, err)
	}
	br, ok := l.(*netlink.Bridge)
	if !ok {
		return nil, fmt.Errorf("%q already exists but is not a bridge", name)
	}
	return br, nil
}

func ensureBridge(brName string, mtu int, promiscMode, vlanFiltering bool) (*netlink.Bridge, error) {
	br := &netlink.Bridge{
		LinkAttrs: netlink.LinkAttrs{
			Name: brName,
			MTU:  mtu,
			// Let kernel use default txqueuelen; leaving it unset
			// means 0, and a zero-length TX queue messes up FIFO
			// traffic shapers which use TX queue length as the
			// default packet limit
			TxQLen: -1,
		},
	}
	if vlanFiltering {
		br.VlanFiltering = &vlanFiltering
	}

	err := netlink.LinkAdd(br)
	if err != nil && err != syscall.EEXIST {
		return nil, fmt.Errorf("could not add %q: %v", brName, err)
	}

	if promiscMode {
		if err := netlink.SetPromiscOn(br); err != nil {
			return nil, fmt.Errorf("could not set promiscuous mode on %q: %v", brName, err)
		}
	}

	// Re-fetch link to read all attributes and if it already existed,
	// ensure it's really a bridge with similar configuration
	br, err = bridgeByName(brName)
	if err != nil {
		return nil, err
	}

	// we want to own the routes for this interface
	_, _ = sysctl.Sysctl(fmt.Sprintf("net/ipv6/conf/%s/accept_ra", brName), "0")

	if err := netlink.LinkSetUp(br); err != nil {
		return nil, err
	}

	return br, nil
}

func setupVeth(netns ns.NetNS, br *netlink.Bridge, ifName string, mtu int, hairpinMode bool, vlanID int, mac string) (*current.Interface, *current.Interface, error) {
	contIface := &current.Interface{}
	hostIface := &current.Interface{}

	err := netns.Do(func(hostNS ns.NetNS) error {
		// create the veth pair in the container and move host end into host netns
		hostVeth, containerVeth, err := ip.SetupVeth(ifName, mtu, mac, hostNS)
		if err != nil {
			return err
		}
		contIface.Name = containerVeth.Name
		contIface.Mac = containerVeth.HardwareAddr.String()
		contIface.Sandbox = netns.Path()
		hostIface.Name = hostVeth.Name
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	// need to lookup hostVeth again as its index has changed during ns move
	hostVeth, err := netlink.LinkByName(hostIface.Name)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to lookup %q: %v", hostIface.Name, err)
	}
	hostIface.Mac = hostVeth.Attrs().HardwareAddr.String()

	// connect host veth end to the bridge
	if err := netlink.LinkSetMaster(hostVeth, br); err != nil {
		return nil, nil, fmt.Errorf("failed to connect %q to bridge %v: %v", hostVeth.Attrs().Name, br.Attrs().Name, err)
	}

	// set hairpin mode
	if err = netlink.LinkSetHairpin(hostVeth, hairpinMode); err != nil {
		return nil, nil, fmt.Errorf("failed to setup hairpin mode for %v: %v", hostVeth.Attrs().Name, err)
	}

	if vlanID != 0 {
		err = netlink.BridgeVlanAdd(hostVeth, uint16(vlanID), true, true, false, true)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to setup vlan tag on interface %q: %v", hostIface.Name, err)
		}
	}

	return hostIface, contIface, nil
}

type NetworkConfig struct {
	Address net.IPNet
	Gateway net.IP
}

type IPAM interface {
	IPForMac(string) ([]NetworkConfig, error)
}

type BridgeConfig struct {
	Name        string
	MTU         int
	Vlan        int
	PromiscMode bool
	MAC         string
	Id          string
	IPAM        IPAM
}

func setupBridge(n *BridgeConfig) (*netlink.Bridge, *current.Interface, error) {
	vlanFiltering := n.Vlan != 0

	// create bridge if necessary
	br, err := ensureBridge(n.Name, n.MTU, n.PromiscMode, vlanFiltering)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create bridge %q: %v", n.Name, err)
	}

	return br, &current.Interface{
		Name: br.Attrs().Name,
		Mac:  br.Attrs().HardwareAddr.String(),
	}, nil
}

func formatChain(id string) string {
	output := sha512.Sum512([]byte(id))
	return fmt.Sprintf("ISLE-%x", output)[:28]
}

func netnsPath(pid int) string {
	return fmt.Sprintf("/proc/%d/ns/net", pid)
}

func ConfigureProcess(ctx context.Context, log hclog.Logger, pid int, bc *BridgeConfig) ([]NetworkConfig, error) {
	br, brInterface, err := setupBridge(bc)
	if err != nil {
		return nil, err
	}

	path := netnsPath(pid)

	netns, err := ns.GetNS(path)
	if err != nil {
		return nil, err
	}

	log.Trace("configuring netns", "path", path, "pid", pid)

	hostInterface, containerInterface, err := setupVeth(
		netns, br, "eth0", bc.MTU, true, bc.Vlan, bc.MAC,
	)
	if err != nil {
		return nil, err
	}

	log.Info("configured veth", "host-iface", hostInterface.Name, "cont-iface", containerInterface.Name)

	addresses, err := bc.IPAM.IPForMac(bc.MAC)
	if err != nil {
		return nil, err
	}

	if err := netns.Do(func(_ ns.NetNS) error {
		_, _ = sysctl.Sysctl(fmt.Sprintf("net/ipv6/conf/%s/accept_dad", "eth0"), "0")
		_, _ = sysctl.Sysctl(fmt.Sprintf("net/ipv4/conf/%s/arp_notify", "eth0"), "1")

		// Add the IP to the interface
		if err := configureIface("eth0", addresses); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// check bridge port state
	retries := []int{0, 50, 500, 1000, 1000}
	for idx, sleep := range retries {
		time.Sleep(time.Duration(sleep) * time.Millisecond)

		hostVeth, err := netlink.LinkByName(hostInterface.Name)
		if err != nil {
			return nil, err
		}
		if hostVeth.Attrs().OperState == netlink.OperUp {
			break
		}

		if idx == len(retries)-1 {
			return nil, fmt.Errorf("bridge port in error state: %s", hostVeth.Attrs().OperState)
		}
	}

	// Refetch the bridge to get all updated attributes
	br, err = bridgeByName(bc.Name)
	if err != nil {
		return nil, err
	}

	hw := br.Attrs().HardwareAddr
	log.Trace("configuring bridge with gateway addresses", "mac", hw.String())
	err = configureGW(br, addresses)
	if err != nil {
		return nil, err
	}

	chain := formatChain(bc.Id)
	comment := fmt.Sprintf("id: %q", bc.Id)

	for _, ac := range addresses {
		if err = ip.SetupIPMasq(&ac.Address, chain, comment); err != nil {
			return nil, err
		}
	}

	// Refetch the bridge since its MAC address may change when the first
	// veth is added or after its IP address is set
	br, err = bridgeByName(br.Name)
	if err != nil {
		return nil, err
	}
	brInterface.Mac = br.Attrs().HardwareAddr.String()

	return addresses, nil
}

func Delete(ctx context.Context, log hclog.Logger, pid int, id string) error {
	var ipnets []*net.IPNet

	err := ns.WithNetNSPath(netnsPath(pid), func(_ ns.NetNS) error {
		var err error
		ipnets, err = ip.DelLinkByNameAddr("eth0")
		if err != nil && err == ip.ErrLinkNotFound {
			return nil
		}
		return err
	})

	if err != nil {
		_, ok := err.(ns.NSPathNotExistErr)
		if !ok {
			return err
		}
	}

	chain := formatChain(id)
	comment := fmt.Sprintf("id: %q", id)

	for _, ipn := range ipnets {
		if err := ip.TeardownIPMasq(ipn, chain, comment); err != nil {
			return err
		}
	}

	return nil
}

const (
	// Note: use slash as separator so we can have dots in interface name (VLANs)
	DisableIPv6SysctlTemplate = "net/ipv6/conf/%s/disable_ipv6"
)

func enableIPv6(ifName string) error {
	// Enabled IPv6 for loopback "lo" and the interface
	// being configured
	for _, iface := range [2]string{"lo", ifName} {
		ipv6SysctlValueName := fmt.Sprintf(DisableIPv6SysctlTemplate, iface)

		// Read current sysctl value
		value, err := sysctl.Sysctl(ipv6SysctlValueName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ipam_linux: failed to read sysctl %q: %v\n", ipv6SysctlValueName, err)
			continue
		}
		if value == "0" {
			continue
		}

		// Write sysctl to enable IPv6
		_, err = sysctl.Sysctl(ipv6SysctlValueName, "0")
		if err != nil {
			return fmt.Errorf("failed to enable IPv6 for interface %q (%s=%s): %v", iface, ipv6SysctlValueName, value, err)
		}
	}

	return nil
}

func configureIface(ifName string, addresses []NetworkConfig) error {
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return fmt.Errorf("failed to lookup %q: %v", ifName, err)
	}

	err = enableIPv6(ifName)
	if err != nil {
		return errors.Wrapf(err, "unable to enable ipv6")
	}

	for _, ac := range addresses {
		addr := &netlink.Addr{IPNet: &ac.Address, Label: ""}
		if err = netlink.AddrAdd(link, addr); err != nil {
			return fmt.Errorf("failed to add IP addr %v to %q: %v", ac.Address, ifName, err)
		}
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("failed to set %q UP: %v", ifName, err)
	}

	ip.SettleAddresses(ifName, 10)

	for _, ac := range addresses {
		routeIsV4 := ac.Gateway.To4() != nil
		var dst *net.IPNet

		if routeIsV4 {
			_, dst, _ = net.ParseCIDR("0.0.0.0/0")
		} else {
			_, dst, _ = net.ParseCIDR("::/0")
		}

		net.ParseCIDR("0.0.0.0/0")
		route := netlink.Route{
			Dst:       dst,
			LinkIndex: link.Attrs().Index,
			Gw:        ac.Gateway,
		}

		if err = netlink.RouteAddEcmp(&route); err != nil {
			return fmt.Errorf("failed to add route '%v via %v dev %v': %v", dst, ac.Gateway, ifName, err)
		}
	}

	return nil
}

const SETTLE_INTERVAL = 50 * time.Millisecond

// SettleAddresses waits for all addresses on a link to leave tentative state.
// This is particularly useful for ipv6, where all addresses need to do DAD.
// There is no easy way to wait for this as an event, so just loop until the
// addresses are no longer tentative.
// If any addresses are still tentative after timeout seconds, then error.
func SettleAddresses(ifName string, timeout int) error {
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return fmt.Errorf("failed to retrieve link: %v", err)
	}

	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	for {
		addrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)
		if err != nil {
			return fmt.Errorf("could not list addresses: %v", err)
		}

		if len(addrs) == 0 {
			return nil
		}

		ok := true
		for _, addr := range addrs {
			if addr.Flags&(syscall.IFA_F_TENTATIVE|syscall.IFA_F_DADFAILED) > 0 {
				ok = false
				break // Break out of the `range addrs`, not the `for`
			}
		}

		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("link %s still has tentative addresses after %d seconds",
				ifName,
				timeout)
		}

		time.Sleep(SETTLE_INTERVAL)
	}
}

func configureGW(br netlink.Link, addresses []NetworkConfig) error {
	for _, ac := range addresses {
		gwIP := &net.IPNet{
			IP:   ip.NextIP(ac.Address.IP.Mask(ac.Address.Mask)),
			Mask: ac.Address.Mask,
		}

		var family int

		if gwIP.IP.To4() != nil {
			family = netlink.FAMILY_V4
		} else {
			family = netlink.FAMILY_V6
		}

		err := ensureAddr(br, family, gwIP, false)
		if err != nil {
			return err
		}
		if family == netlink.FAMILY_V4 {
			err = ip.EnableIP4Forward()
		} else {
			err = ip.EnableIP6Forward()
		}

		if err != nil {
			return err
		}
	}

	return nil
}

func ensureAddr(br netlink.Link, family int, ipn *net.IPNet, forceAddress bool) error {
	addrs, err := netlink.AddrList(br, family)
	if err != nil && err != syscall.ENOENT {
		return fmt.Errorf("could not get list of IP addresses: %v", err)
	}

	ipnStr := ipn.String()
	for _, a := range addrs {

		// string comp is actually easiest for doing IPNet comps
		if a.IPNet.String() == ipnStr {
			return nil
		}

		// Multiple IPv6 addresses are allowed on the bridge if the
		// corresponding subnets do not overlap. For IPv4 or for
		// overlapping IPv6 subnets, reconfigure the IP address if
		// forceAddress is true, otherwise throw an error.
		if family == netlink.FAMILY_V4 || a.Contains(ipn.IP) || ipn.Contains(a.IP) {
			if forceAddress {
				if err = deleteAddr(br, a.IPNet); err != nil {
					return err
				}
			} else {
				return fmt.Errorf("%q already has an IP address different from %v", br.Attrs().Name, ipnStr)
			}
		}
	}

	addr := &netlink.Addr{IPNet: ipn, Label: ""}
	if err := netlink.AddrAdd(br, addr); err != nil && err != syscall.EEXIST {
		return fmt.Errorf("could not add IP address to %q: %v", br.Attrs().Name, err)
	}

	// Set the bridge's MAC to itself. Otherwise, the bridge will take the
	// lowest-numbered mac on the bridge, and will change as ifs churn
	if err := netlink.LinkSetHardwareAddr(br, br.Attrs().HardwareAddr); err != nil {
		return fmt.Errorf("could not set bridge's mac: %v (%v)", err, br.Attrs().HardwareAddr)
	}

	return nil
}

func deleteAddr(br netlink.Link, ipn *net.IPNet) error {
	addr := &netlink.Addr{IPNet: ipn, Label: ""}

	if err := netlink.AddrDel(br, addr); err != nil {
		return fmt.Errorf("could not remove IP address from %q: %v", br.Attrs().Name, err)
	}

	return nil
}
