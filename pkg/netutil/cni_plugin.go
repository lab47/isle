/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package netutil

type CNIPlugin interface {
	GetPluginType() string
}

type IPAMRange struct {
	Subnet     string `json:"subnet"`
	RangeStart string `json:"rangeStart,omitempty"`
	RangeEnd   string `json:"rangeEnd,omitempty"`
	Gateway    string `json:"gateway,omitempty"`
	IPRange    string `json:"ipRange,omitempty"`
}

type IPAMRoute struct {
	Dst     string `json:"dst,omitempty"`
	GW      string `json:"gw,omitempty"`
	Gateway string `json:"gateway,omitempty"`
}

type isolationConfig struct {
	PluginType string `json:"type"`
}

func newIsolationPlugin() *isolationConfig {
	return &isolationConfig{
		PluginType: "isolation",
	}
}

func (*isolationConfig) GetPluginType() string {
	return "isolation"
}

// bridgeConfig describes the bridge plugin
type bridgeConfig struct {
	PluginType   string                 `json:"type"`
	BrName       string                 `json:"bridge,omitempty"`
	IsGW         bool                   `json:"isGateway,omitempty"`
	IsDefaultGW  bool                   `json:"isDefaultGateway,omitempty"`
	ForceAddress bool                   `json:"forceAddress,omitempty"`
	IPMasq       bool                   `json:"ipMasq,omitempty"`
	MTU          int                    `json:"mtu,omitempty"`
	HairpinMode  bool                   `json:"hairpinMode,omitempty"`
	PromiscMode  bool                   `json:"promiscMode,omitempty"`
	Vlan         int                    `json:"vlan,omitempty"`
	IPAM         map[string]interface{} `json:"ipam"`
	Capabilities map[string]bool        `json:"capabilities,omitempty"`
}

func newBridgePlugin(bridgeName string) *bridgeConfig {
	return &bridgeConfig{
		PluginType:   "bridge",
		BrName:       bridgeName,
		Capabilities: map[string]bool{"ips": true, "mac": true},
	}
}

func (*bridgeConfig) GetPluginType() string {
	return "bridge"
}

// vlanConfig describes the macvlan/ipvlan config
type vlanConfig struct {
	PluginType string                 `json:"type"`
	Master     string                 `json:"master"`
	Mode       string                 `json:"mode,omitempty"`
	MTU        int                    `json:"mtu,omitempty"`
	IPAM       map[string]interface{} `json:"ipam"`
}

func newVLANPlugin(pluginType string) *vlanConfig {
	return &vlanConfig{
		PluginType: pluginType,
	}
}

func (c *vlanConfig) GetPluginType() string {
	return c.PluginType
}

// portMapConfig describes the portmapping plugin
type portMapConfig struct {
	PluginType   string          `json:"type"`
	Capabilities map[string]bool `json:"capabilities"`
}

func newPortMapPlugin() *portMapConfig {
	return &portMapConfig{
		PluginType: "portmap",
		Capabilities: map[string]bool{
			"portMappings": true,
		},
	}
}

func (*portMapConfig) GetPluginType() string {
	return "portmap"
}

// firewallConfig describes the firewall plugin
type firewallConfig struct {
	PluginType string `json:"type"`
	Backend    string `json:"backend,omitempty"`
}

func newFirewallPlugin() *firewallConfig {
	return &firewallConfig{
		PluginType: "firewall",
	}
}

func (*firewallConfig) GetPluginType() string {
	return "firewall"
}

// tuningConfig describes the tuning plugin
type tuningConfig struct {
	PluginType string `json:"type"`
}

func newTuningPlugin() *tuningConfig {
	return &tuningConfig{
		PluginType: "tuning",
	}
}

func (*tuningConfig) GetPluginType() string {
	return "tuning"
}

// https://github.com/containernetworking/plugins/blob/v1.0.1/plugins/ipam/host-local/backend/allocator/config.go#L47-L56
type hostLocalIPAMConfig struct {
	Type        string        `json:"type"`
	Routes      []IPAMRoute   `json:"routes,omitempty"`
	ResolveConf string        `json:"resolveConf,omitempty"`
	DataDir     string        `json:"dataDir,omitempty"`
	Ranges      [][]IPAMRange `json:"ranges,omitempty"`
}

func newHostLocalIPAMConfig() *hostLocalIPAMConfig {
	return &hostLocalIPAMConfig{
		Type: "host-local",
	}
}

// https://github.com/containernetworking/plugins/blob/v1.1.0/plugins/ipam/dhcp/main.go#L43-L54
type dhcpIPAMConfig struct {
	Type             string `json:"type"`
	DaemonSocketPath string `json:"daemonSocketPath,omitempty"`
}

func newDHCPIPAMConfig() *dhcpIPAMConfig {
	return &dhcpIPAMConfig{
		Type: "dhcp",
	}
}

type StaticAddress struct {
	Address string `json:"address"`
	Gateway string `json:"gateway"`
}

type staticIPAMConfig struct {
	Type      string          `json:"static"`
	Addresses []StaticAddress `json:"addresses"`
	Routes    []IPAMRoute     `json:"routes,omitempty"`
}

func newStaticIPAMConfig() *staticIPAMConfig {
	return &staticIPAMConfig{
		Type: "static",
	}
}
