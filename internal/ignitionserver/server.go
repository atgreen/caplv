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

// Package ignitionserver manages the lifecycle of per-machine ignition
// HTTP servers on libvirt hosts. Instead of embedding large ignition
// configs in QEMU fw_cfg (which has O(n²) read performance), a tiny
// pointer config is placed in fw_cfg that redirects ignition to fetch
// the full config from a local HTTP server on the host.
package ignitionserver

import (
	"encoding/json"
	"fmt"
	"hash/crc32"
)

const (
	// ServerBinaryPath is where the ignition-server binary is cached on the host.
	ServerBinaryPath = "/run/caplv/ignition-server"
	// LocalBinaryPath is where the binary lives in the CAPLV container.
	LocalBinaryPath = "/ignition-server"
)

// Port returns a unique port for the given machine name in the ephemeral range.
func Port(machineName string) int {
	return 49152 + int(crc32.ChecksumIEEE([]byte(machineName))%16383)
}

// PointerIgnition returns a minimal ignition config that redirects to
// an HTTP server on the host for the full config.
func PointerIgnition(hostIP string, port int) ([]byte, error) {
	pointer := map[string]any{
		"ignition": map[string]any{
			"version": "3.2.0",
			"config": map[string]any{
				"replace": map[string]any{
					"source": fmt.Sprintf("http://%s:%d/config", hostIP, port),
				},
			},
		},
	}
	return json.Marshal(pointer)
}

// StartCommand returns a shell command to launch the ignition-server on the host.
func StartCommand(ignitionFilePath string, port int) string {
	return fmt.Sprintf("nohup %s %s %d > /dev/null 2>&1 & echo $!",
		ServerBinaryPath, ignitionFilePath, port)
}

// StopCommand returns a command to kill the ignition server for a machine.
func StopCommand(port int) string {
	return fmt.Sprintf("pkill -f 'ignition-server.*%d' 2>/dev/null || true", port)
}

