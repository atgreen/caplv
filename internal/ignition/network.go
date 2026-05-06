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
// OVN's configure-ovs script creates its own br-ex connection profile,
// overriding any NMConnection file we drop in. To work around this, we
// inject a oneshot systemd unit that waits for br-ex to appear, then
// reconfigures the existing OVN-managed connection with static addressing.
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

	script := generateStaticIPScript(net)
	injectFile(config,
		"/usr/local/bin/caplv-static-ip.sh",
		"data:,"+url.PathEscape(script), 0o755, isV3)

	unit := generateStaticIPUnit()
	injectSystemdUnit(config, "caplv-static-ip.service", unit, true, isV3)

	return json.Marshal(config)
}

// generateStaticIPScript creates a shell script that modifies the
// OVN-managed br-ex connection to use static addressing.
func generateStaticIPScript(net NetworkConfig) string {
	var b strings.Builder

	b.WriteString("#!/bin/bash\n")
	b.WriteString("# CAPLV: Reconfigure br-ex with static IP after OVN setup.\n")
	b.WriteString("# Wait for br-ex to be created by OVN's configure-ovs.\n")
	b.WriteString("for i in $(seq 1 120); do\n")
	b.WriteString("  if nmcli -t -f NAME con show --active 2>/dev/null | grep -q ovs-if-br-ex; then\n")
	b.WriteString("    break\n")
	b.WriteString("  fi\n")
	b.WriteString("  sleep 2\n")
	b.WriteString("done\n\n")

	b.WriteString("# Find the OVN-managed br-ex connection name\n")
	b.WriteString("BREX_CON=$(nmcli -t -f NAME con show | grep -m1 ovs-if-br-ex)\n")
	b.WriteString("if [ -z \"$BREX_CON\" ]; then\n")
	b.WriteString("  echo 'caplv-static-ip: br-ex connection not found, exiting'\n")
	b.WriteString("  exit 1\n")
	b.WriteString("fi\n\n")

	b.WriteString("# Reconfigure with static IP\n")
	b.WriteString("nmcli con mod \"$BREX_CON\" ipv4.method manual \\\n")

	addrs := make([]string, len(net.Addresses))
	copy(addrs, net.Addresses)
	b.WriteString(fmt.Sprintf("  ipv4.addresses \"%s\"", strings.Join(addrs, ",")))

	if net.Gateway != "" {
		b.WriteString(fmt.Sprintf(" \\\n  ipv4.gateway \"%s\"", net.Gateway))
	}
	if len(net.DNSServers) > 0 {
		b.WriteString(fmt.Sprintf(" \\\n  ipv4.dns \"%s\"", strings.Join(net.DNSServers, ",")))
	}
	if len(net.DNSSearch) > 0 {
		b.WriteString(fmt.Sprintf(" \\\n  ipv4.dns-search \"%s\"", strings.Join(net.DNSSearch, ",")))
	}
	b.WriteString("\n\n")

	b.WriteString("# Apply the changes\n")
	b.WriteString("nmcli con up \"$BREX_CON\"\n")
	b.WriteString("echo \"caplv-static-ip: br-ex configured with static IP\"\n")

	return b.String()
}

// generateStaticIPUnit creates a systemd unit that runs the static IP
// script after the network is online.
func generateStaticIPUnit() string {
	return `[Unit]
Description=CAPLV: Configure br-ex with static IP
After=NetworkManager.service ovs-configuration.service
Wants=NetworkManager.service

[Service]
Type=oneshot
ExecStart=/usr/local/bin/caplv-static-ip.sh
RemainAfterExit=true

[Install]
WantedBy=multi-user.target
`
}

// injectSystemdUnit adds a systemd unit to the ignition config.
func injectSystemdUnit(config map[string]any, name, contents string, enabled, isV3 bool) {
	systemd, ok := config["systemd"].(map[string]any)
	if !ok {
		systemd = map[string]any{}
		config["systemd"] = systemd
	}

	units, _ := systemd["units"].([]any)

	// Remove any existing unit with the same name.
	filtered := make([]any, 0, len(units))
	for _, u := range units {
		um, ok := u.(map[string]any)
		if ok {
			if n, _ := um["name"].(string); n == name {
				continue
			}
		}
		filtered = append(filtered, u)
	}

	entry := map[string]any{
		"name":     name,
		"enabled":  enabled,
		"contents": contents,
	}

	filtered = append(filtered, entry)
	systemd["units"] = filtered
}
