/*
Copyright 2026 Anthony Green.

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

package ignition

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// NetworkConfig holds the parameters needed to generate static network
// configuration for an OpenShift worker node using OVN-Kubernetes.
type NetworkConfig struct {
	// Addresses in CIDR notation, e.g. ["192.168.122.100/24"]
	Addresses []string
	// Gateway is the default route, e.g. "192.168.122.1"
	Gateway string
	// DNS servers, e.g. ["192.168.122.1"]
	DNSServers []string
	// DNS search domains
	DNSSearch []string
}

// InjectStaticNetwork merges OVN-compatible static network configuration
// into an ignition config. On OpenShift with OVN-Kubernetes, the physical
// NIC is enslaved to an OVS bridge (br-ex), so the static IP must be
// configured on br-ex rather than the physical interface.
//
// This injects a NetworkManager dispatcher script that reconfigures br-ex
// with the static IP after OVN sets up the bridge.
func InjectStaticNetwork(ignitionData []byte, net NetworkConfig) ([]byte, error) {
	if len(net.Addresses) == 0 {
		return ignitionData, nil
	}

	var config map[string]any
	if err := json.Unmarshal(ignitionData, &config); err != nil {
		return nil, fmt.Errorf("failed to parse ignition config: %w", err)
	}

	ignSection, ok := config["ignition"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ignition config missing 'ignition' section")
	}

	version, _ := ignSection["version"].(string)
	var isV3 bool
	switch {
	case len(version) >= 1 && version[0] == '3':
		isV3 = true
	case len(version) >= 1 && version[0] == '2':
		isV3 = false
	default:
		return nil, fmt.Errorf("unsupported ignition version: %s", version)
	}

	nmConn := generateBrExNMConnection(net)
	injectFile(config,
		"/etc/NetworkManager/system-connections/br-ex-static.nmconnection",
		"data:,"+url.PathEscape(nmConn), 0o600, isV3)

	return json.Marshal(config)
}

// generateBrExNMConnection creates an NMConnection keyfile that configures
// br-ex with static addressing. OVN-Kubernetes creates br-ex as an
// ovs-interface; this connection profile overrides its DHCP configuration.
func generateBrExNMConnection(net NetworkConfig) string {
	var b strings.Builder

	b.WriteString("[connection]\n")
	b.WriteString("id=br-ex-static\n")
	b.WriteString("type=ovs-interface\n")
	b.WriteString("interface-name=br-ex\n")
	b.WriteString("master=br-ex\n")
	b.WriteString("slave-type=ovs-port\n")
	b.WriteString("autoconnect=true\n")
	b.WriteString("autoconnect-priority=100\n")
	b.WriteString("\n")

	b.WriteString("[ovs-interface]\n")
	b.WriteString("type=internal\n")
	b.WriteString("\n")

	b.WriteString("[ipv4]\n")
	b.WriteString("method=manual\n")
	for i, addr := range net.Addresses {
		b.WriteString(fmt.Sprintf("address%d=%s\n", i+1, addr))
	}
	if net.Gateway != "" {
		b.WriteString(fmt.Sprintf("gateway=%s\n", net.Gateway))
	}
	if len(net.DNSServers) > 0 {
		b.WriteString(fmt.Sprintf("dns=%s;\n", strings.Join(net.DNSServers, ";")))
	}
	if len(net.DNSSearch) > 0 {
		b.WriteString(fmt.Sprintf("dns-search=%s;\n", strings.Join(net.DNSSearch, ";")))
	}
	b.WriteString("\n")

	b.WriteString("[ipv6]\n")
	b.WriteString("method=disabled\n")

	return b.String()
}
