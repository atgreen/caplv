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
)

// InjectMachineMetadata merges hostname and providerID into an ignition config.
// The hostname is written to /etc/hostname. The providerID is written to a
// kubelet systemd drop-in that sets --provider-id, enabling CAPI to correlate
// the Node with the Machine object.
// Supports both ignition spec 2.x and 3.x formats.
func InjectMachineMetadata(ignitionData []byte, hostname, providerID string) ([]byte, error) {
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

	if hostname != "" {
		injectFile(config, "/etc/hostname", "data:,"+hostname, 0o644, isV3)
	}

	if providerID != "" {
		dropinContents := fmt.Sprintf("[Service]\nEnvironment=\"KUBELET_PROVIDERID_FLAG=--provider-id=%s\"\n", providerID)
		injectFile(config, "/etc/systemd/system/kubelet.service.d/90-provider-id.conf",
			"data:,"+url.PathEscape(dropinContents), 0o644, isV3)
		injectKubeletProviderIDEnv(config, providerID, isV3)
	}

	return json.Marshal(config)
}

// InjectHostname is a convenience wrapper for backward compatibility.
func InjectHostname(ignitionData []byte, hostname string) ([]byte, error) {
	return InjectMachineMetadata(ignitionData, hostname, "")
}

// injectKubeletProviderIDEnv adds a kubelet environment drop-in that includes
// the provider-id flag. On OpenShift, kubelet reads its flags from environment
// files in /etc/kubernetes/kubelet-env, but the actual mechanism varies. The
// systemd drop-in approach is the most reliable across versions.
func injectKubeletProviderIDEnv(config map[string]any, providerID string, isV3 bool) {
	// Also inject a node-annotations file that the MCO can pick up.
	// This ensures the providerID annotation is set even if kubelet
	// doesn't process the flag.
	annotationContents := fmt.Sprintf(`KUBELET_PROVIDER_ID=%s`, providerID)
	injectFile(config, "/etc/kubernetes/caplv-provider-id",
		"data:,"+url.PathEscape(annotationContents), 0o644, isV3)
}

func injectFile(config map[string]any, path, source string, mode int, isV3 bool) {
	storage := ensureStorage(config)
	files := getFiles(storage)

	// Remove any existing entry at this path.
	filtered := make([]any, 0, len(files))
	for _, f := range files {
		fm, ok := f.(map[string]any)
		if ok {
			if p, _ := fm["path"].(string); p == path {
				continue
			}
		}
		filtered = append(filtered, f)
	}

	entry := map[string]any{
		"path": path,
		"mode": mode,
		"contents": map[string]any{
			"source": source,
		},
	}

	if isV3 {
		entry["overwrite"] = true
	} else {
		entry["filesystem"] = "root"
	}

	filtered = append(filtered, entry)
	storage["files"] = filtered
}

func ensureStorage(config map[string]any) map[string]any {
	storage, ok := config["storage"].(map[string]any)
	if !ok {
		storage = map[string]any{}
		config["storage"] = storage
	}
	return storage
}

func getFiles(storage map[string]any) []any {
	files, ok := storage["files"].([]any)
	if !ok {
		return []any{}
	}
	return files
}
