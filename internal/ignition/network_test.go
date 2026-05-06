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
	"net/url"
	"strings"
	"testing"
)

func TestInjectStaticNetwork(t *testing.T) {
	input := testIgnitionV3Input
	net := NetworkConfig{
		Addresses:  []string{"192.168.122.100/24"},
		Gateway:    "192.168.122.1",
		DNSServers: []string{"192.168.122.1"},
	}

	result, err := InjectStaticNetwork([]byte(input), net)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]any
	if err := json.Unmarshal(result, &config); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	files := config["storage"].(map[string]any)["files"].([]any)
	found := false
	for _, f := range files {
		fm := f.(map[string]any)
		if fm["path"] == "/etc/NetworkManager/system-connections/br-ex-static.nmconnection" {
			contents := fm["contents"].(map[string]any)
			source := contents["source"].(string)
			decoded, err := url.PathUnescape(strings.TrimPrefix(source, "data:,"))
			if err != nil {
				t.Fatalf("failed to decode source: %v", err)
			}
			if !strings.Contains(decoded, "address1=192.168.122.100/24") {
				t.Errorf("expected address in nmconnection, got:\n%s", decoded)
			}
			if !strings.Contains(decoded, "gateway=192.168.122.1") {
				t.Errorf("expected gateway in nmconnection, got:\n%s", decoded)
			}
			if !strings.Contains(decoded, "method=manual") {
				t.Errorf("expected method=manual in nmconnection, got:\n%s", decoded)
			}
			if !strings.Contains(decoded, "interface-name=br-ex") {
				t.Errorf("expected interface-name=br-ex in nmconnection, got:\n%s", decoded)
			}
			if mode, ok := fm["mode"].(float64); !ok || int(mode) != 0o600 {
				t.Errorf("expected mode 0600, got %v", fm["mode"])
			}
			found = true
		}
	}
	if !found {
		t.Error("expected br-ex-static.nmconnection file in ignition output")
	}
}

func TestInjectStaticNetworkNoAddresses(t *testing.T) {
	input := testIgnitionV3Input
	net := NetworkConfig{}

	result, err := InjectStaticNetwork([]byte(input), net)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should return unchanged
	if string(result) != input {
		t.Error("expected unchanged output when no addresses provided")
	}
}

func TestInjectStaticNetworkMultipleAddresses(t *testing.T) {
	input := testIgnitionV3Input
	net := NetworkConfig{
		Addresses: []string{"192.168.122.100/24", "10.0.0.100/8"},
		Gateway:   "192.168.122.1",
	}

	result, err := InjectStaticNetwork([]byte(input), net)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config map[string]any
	if err := json.Unmarshal(result, &config); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	files := config["storage"].(map[string]any)["files"].([]any)
	for _, f := range files {
		fm := f.(map[string]any)
		if fm["path"] == "/etc/NetworkManager/system-connections/br-ex-static.nmconnection" {
			contents := fm["contents"].(map[string]any)
			source := contents["source"].(string)
			decoded, _ := url.PathUnescape(strings.TrimPrefix(source, "data:,"))
			if !strings.Contains(decoded, "address1=192.168.122.100/24") {
				t.Error("missing first address")
			}
			if !strings.Contains(decoded, "address2=10.0.0.100/8") {
				t.Error("missing second address")
			}
			return
		}
	}
	t.Error("nmconnection file not found")
}
