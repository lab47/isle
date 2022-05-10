package guestapi

import "net"

func ToIPAddress(ipnet *net.IPNet) *IPAddress {
	ones, _ := ipnet.Mask.Size()

	return &IPAddress{
		Address: ipnet.IP,
		Mask:    int32(ones),
	}
}

func FromNetIP(ip net.IP) *IPAddress {
	return &IPAddress{
		Address: ip,
	}
}

func ParseCIDR(cidr string) (*IPAddress, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}

	return ToIPAddress(ipnet), nil
}

func MustParseCIDR(cidr string) *IPAddress {
	ip, err := ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}

	return ip
}

func (ip *IPAddress) Canonical() *net.IPNet {
	return &net.IPNet{
		IP:   net.IP(ip.Address),
		Mask: net.CIDRMask(int(ip.Mask), len(ip.Address)*8),
	}
}

func (ip *IPAddress) NetIP() net.IP {
	return net.IP(ip.Address)
}
