package guest

import (
	"context"
	"fmt"
	"net"

	"github.com/containernetworking/plugins/pkg/ip"
	"github.com/davecgh/go-spew/spew"
	"github.com/fxamacker/cbor/v2"
	"github.com/lab47/isle/network"
	"go.etcd.io/bbolt"
)

type IPInfo struct {
	Gateway bool   `cbor:"gateway"`
	IPv4    string `cbor:"ipv4"`
	IPv6    string `cbor:"ipv6"`
}

func (g *Guest) IPForMac(mac string) ([]network.NetworkConfig, error) {
	ctx := context.Background()

	var info IPInfo

	err := g.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("ips"))
		if b == nil {
			return nil
		}

		v := b.Get([]byte(mac))
		if v != nil {
			return cbor.Unmarshal(v, &info)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	if info.IPv4 != "" {
		ip4, ipv4cidr, err := net.ParseCIDR(info.IPv4)
		if err != nil {
			return nil, err
		}

		ipv4cidr.IP = ip4

		ip6, ipv6cidr, err := net.ParseCIDR(info.IPv6)
		if err != nil {
			return nil, err
		}

		ipv6cidr.IP = ip6

		ipv4 := network.NetworkConfig{
			Address: *ipv4cidr,
			Gateway: g.gw4,
		}

		ipv6 := network.NetworkConfig{
			Address: *ipv6cidr,
			Gateway: g.gw6,
		}

		return []network.NetworkConfig{ipv4, ipv6}, nil
	}

	g.L.Trace("allocating new ip", "mac", mac)

	networks, err := g.allocIP(ctx, mac)
	if err != nil {
		return nil, err
	}

	spew.Dump(networks)

	return networks, nil
}

func (g *Guest) allocIP(ctx context.Context, mac string) ([]network.NetworkConfig, error) {
	start, curNet, err := net.ParseCIDR(g.lastUsedIP)
	if err != nil {
		return nil, err
	}

	g.L.Trace("last reserved ip", "ip", start.String())

	nextIP := start

	for {
		nextIP = ip.NextIP(nextIP)

		if !curNet.Contains(nextIP) {
			nextIP = firstIP(curNet)
		}

		g.L.Trace("trying ip", "ip", nextIP.String())

		if nextIP.Equal(start) {
			return nil, fmt.Errorf("exhausted IPs")
		}

		curNet.IP = nextIP

		if _, ok := g.usedIps[curNet.String()]; !ok {
			break
		}
	}

	curNet.IP = nextIP
	ipv4 := network.NetworkConfig{
		Address: *curNet,
		Gateway: g.gw4,
	}

	_, cidr6, err := net.ParseCIDR(g.v6net)
	if err != nil {
		return nil, err
	}

	ones, _ := cidr6.Mask.Size()

	shift := ones / 8

	hw, err := net.ParseMAC(mac)
	if err != nil {
		return nil, err
	}

	copy(cidr6.IP[shift:], hw)

	ipv6 := network.NetworkConfig{
		Address: *cidr6,
		Gateway: g.gw6,
	}

	g.lastUsedIP = curNet.String()

	return []network.NetworkConfig{ipv4, ipv6}, nil
}

func firstIP(n *net.IPNet) net.IP {
	ip := make(net.IP, len(n.IP))
	copy(ip, n.IP)

	ip[len(ip)-1] = 1

	return ip
}

func lastIP(subnet net.IPNet) net.IP {
	var end net.IP
	for i := 0; i < len(subnet.IP); i++ {
		end = append(end, subnet.IP[i]|^subnet.Mask[i])
	}
	if subnet.IP.To4() != nil {
		end[3]--
	}

	return end
}
