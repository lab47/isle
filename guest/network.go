package guest

import (
	"encoding/hex"
	"net"
	"sync"

	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/guestapi"
	"github.com/samber/do"
)

type IPNetworkManager struct {
	log             hclog.Logger
	containerSchema *Schema

	clusterId string

	mu sync.Mutex
}

func (m *IPNetworkManager) Init(ctx *ResourceContext) error {
	s, err := ctx.SetSchema("container", "ipnetwork", &guestapi.IPNetwork{}, "name")
	if err != nil {
		return err
	}

	m.containerSchema = s

	return nil
}

func (m *IPNetworkManager) Bootstrap(ctx *ResourceContext) (*guestapi.ResourceId, error) {
	err := m.Init(ctx)
	if err != nil {
		return nil, err
	}

	if len(m.clusterId) != 10 {
		m.clusterId = newHexId(5)
		m.log.Debug("assigned random new cluster-id", "cluster-id", m.clusterId)
	}

	m.log.Info("bootstrapping network manager", "cluster-id", m.clusterId)

	key, err := m.containerSchema.Key("name", "default")
	if err != nil {
		return nil, err
	}

	resources, err := ctx.FetchByIndex(key)
	if err == nil {
		if len(resources) == 1 {
			return resources[0].Id, nil
		}
	}

	v6net, err := IPv6Base(m.clusterId, newHexId(2))
	if err != nil {
		return nil, err
	}

	// create the default network if it doesn't exist.

	ipn := &guestapi.IPNetwork{
		Name:        "default",
		Ipv4Block:   guestapi.MustParseCIDR("10.47.0.0/24"),
		Ipv4Gateway: guestapi.MustParseCIDR("10.47.0.1/32"),
		Ipv6Block:   guestapi.ToIPAddress(v6net),
		Ipv6Gateway: guestapi.FromNetIP(FirstAddress(v6net)),
	}

	res, err := m.Create(ctx, ipn)
	if err != nil {
		return nil, err
	}

	return res.Id, nil
}

func BootstrapIPM(i *do.Injector) (*IPNetworkManager, error) {
	ctx, err := do.Invoke[*ResourceContext](i)
	if err != nil {
		return nil, err
	}

	log, err := do.Invoke[hclog.Logger](i)
	if err != nil {
		return nil, err
	}

	clusterId, _ := do.InvokeNamed[string](i, "cluster-id")

	ipm := &IPNetworkManager{
		log:       log,
		clusterId: clusterId,
	}

	resid, err := ipm.Bootstrap(ctx)
	if err != nil {
		return nil, err
	}

	do.ProvideNamedValue(i, "default-network", resid)

	return ipm, nil
}

func (m *IPNetworkManager) Create(ctx *ResourceContext, network *guestapi.IPNetwork) (*guestapi.Resource, error) {
	// Assign the first address to the gateway if there is no gateway set
	if network.Ipv4Gateway == nil {
		cidr := network.Ipv4Block.Canonical()

		ip := cidr.IP
		ip[len(ip)-1] = 1

		network.Ipv4Gateway = &guestapi.IPAddress{
			Address: ip,
		}
	}

	// Assign the first address to the gateway if there is no gateway set
	if network.Ipv6Gateway == nil {
		cidr := network.Ipv6Block.Canonical()

		ip := cidr.IP
		ip[len(ip)-1] = 1

		network.Ipv6Gateway = &guestapi.IPAddress{
			Address: ip,
		}
	}

	network.Data = &guestapi.IPNetwork_LiveData{
		Allocated: make(map[string]*guestapi.ResourceId),
	}

	id := m.containerSchema.NewId()

	network.Data.Allocated[network.Ipv4Gateway.Canonical().IP.String()] = id

	prov := &guestapi.ProvisionStatus{
		Status: guestapi.ProvisionStatus_RUNNING,
	}

	return ctx.Set(ctx, id, network, prov)
}

func (m *IPNetworkManager) Update(ctx *ResourceContext, res *guestapi.Resource) (*guestapi.Resource, error) {
	return nil, ErrImmutable
}

func (m *IPNetworkManager) Read(ctx *ResourceContext, id *guestapi.ResourceId) (*guestapi.Resource, error) {
	return ctx.Fetch(id)
}

func (m *IPNetworkManager) Delete(ctx *ResourceContext, res *guestapi.Resource, cont *guestapi.IPNetwork) error {
	_, err := ctx.Delete(ctx, res.Id)
	return err
}

func (m *IPNetworkManager) nextAddress(network *guestapi.IPNetwork, user *guestapi.ResourceId) (*net.IPNet, error) {
	cidr := network.Ipv4Block.Canonical()

	ones, bits := cidr.Mask.Size()
	if ones == bits {
		return cidr, nil
	}

	last := &cidr.IP[len(cidr.IP)-1]
	*last = 1

	for network.Data.Allocated[cidr.IP.String()] != nil {
		*last = *last + 1
	}

	network.Data.Allocated[cidr.IP.String()] = user

	return cidr, nil
}

func IPv6Base(clusterId, subnetId string) (*net.IPNet, error) {
	ip := make(net.IP, net.IPv6len)
	ip[0] = 0xfd

	data, err := hex.DecodeString(clusterId)
	if err != nil {
		return nil, err
	}

	copy(ip[1:], data)

	v6subnetAddr := make(net.IP, net.IPv6len)
	copy(v6subnetAddr, ip)

	data, err = hex.DecodeString(subnetId)
	if err != nil {
		return nil, err
	}

	copy(v6subnetAddr[6:], data[:2])

	ipnet := &net.IPNet{
		IP:   v6subnetAddr,
		Mask: net.CIDRMask(64, net.IPv6len*8),
	}

	return ipnet, nil
}

func FirstAddress(n *net.IPNet) net.IP {
	ip := make(net.IP, len(n.IP))
	copy(ip, n.IP)

	ip[len(ip)-1] = 1

	return ip
}

type Allocation struct {
	Address *net.IPNet
	Gateway net.IP
}

func (m *IPNetworkManager) Allocate(ctx *ResourceContext, id *guestapi.ResourceId, userId *guestapi.ResourceId) ([]*Allocation, error) {
	var network guestapi.IPNetwork

	res, err := FetchAs(ctx, id, &network)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	cidr6 := network.Ipv6Block.Canonical()

	ones, _ := cidr6.Mask.Size()

	shift := ones / 8

	ip6 := cidr6.IP

	// If there is enough room for the timestamp, start at the beginning.
	// Otherwise just use the random portion.
	if shift > 10 {
		copy(ip6[shift:], userId.UniqueId[6:])
	} else {
		copy(ip6[shift:], userId.UniqueId)
	}

	cidr6.IP = ip6

	addr4, err := m.nextAddress(&network, userId)
	if err != nil {
		return nil, err
	}

	_, err = ctx.Set(ctx, id, &network, res.ProvisionStatus)
	if err != nil {
		return nil, err
	}

	allocations := []*Allocation{
		{
			Address: addr4,
			Gateway: network.Ipv4Gateway.NetIP(),
		},
		{
			Address: cidr6,
			Gateway: network.Ipv6Gateway.NetIP(),
		},
	}

	return allocations, nil
}

func (m *IPNetworkManager) Deallocate(ctx *ResourceContext, id *guestapi.ResourceId, address *net.IPNet) error {
	var network guestapi.IPNetwork

	res, err := FetchAs(ctx, id, &network)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	delete(network.Data.Allocated, address.String())

	_, err = ctx.Set(ctx, id, &network, res.ProvisionStatus)
	if err != nil {
		return err
	}

	return nil
}
