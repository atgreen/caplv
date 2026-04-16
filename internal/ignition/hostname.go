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
)

// InjectHostname merges a hostname file entry into an ignition config.
// It supports both ignition spec 2.x and 3.x formats.
func InjectHostname(ignitionData []byte, hostname string) ([]byte, error) {
	var config map[string]interface{}
	if err := json.Unmarshal(ignitionData, &config); err != nil {
		return nil, fmt.Errorf("failed to parse ignition config: %w", err)
	}

	ignSection, ok := config["ignition"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("ignition config missing 'ignition' section")
	}

	version, _ := ignSection["version"].(string)

	switch {
	case len(version) >= 1 && version[0] == '3':
		injectHostnameV3(config, hostname)
	case len(version) >= 1 && version[0] == '2':
		injectHostnameV2(config, hostname)
	default:
		return nil, fmt.Errorf("unsupported ignition version: %s", version)
	}

	return json.Marshal(config)
}

func injectHostnameV3(config map[string]interface{}, hostname string) {
	storage, ok := config["storage"].(map[string]interface{})
	if !ok {
		storage = map[string]interface{}{}
		config["storage"] = storage
	}

	files, ok := storage["files"].([]interface{})
	if !ok {
		files = []interface{}{}
	}

	// Remove any existing /etc/hostname entry.
	filtered := make([]interface{}, 0, len(files))
	for _, f := range files {
		fm, ok := f.(map[string]interface{})
		if ok {
			if path, _ := fm["path"].(string); path == "/etc/hostname" {
				continue
			}
		}
		filtered = append(filtered, f)
	}

	overwrite := true
	mode := 0o644
	filtered = append(filtered, map[string]interface{}{
		"path":      "/etc/hostname",
		"mode":      mode,
		"overwrite": overwrite,
		"contents": map[string]interface{}{
			"source": "data:," + hostname,
		},
	})

	storage["files"] = filtered
}

func injectHostnameV2(config map[string]interface{}, hostname string) {
	storage, ok := config["storage"].(map[string]interface{})
	if !ok {
		storage = map[string]interface{}{}
		config["storage"] = storage
	}

	files, ok := storage["files"].([]interface{})
	if !ok {
		files = []interface{}{}
	}

	// Remove any existing /etc/hostname entry.
	filtered := make([]interface{}, 0, len(files))
	for _, f := range files {
		fm, ok := f.(map[string]interface{})
		if ok {
			if path, _ := fm["path"].(string); path == "/etc/hostname" {
				continue
			}
		}
		filtered = append(filtered, f)
	}

	filtered = append(filtered, map[string]interface{}{
		"path": "/etc/hostname",
		"mode": 0o644,
		"filesystem": "root",
		"contents": map[string]interface{}{
			"source": "data:," + hostname,
		},
	})

	storage["files"] = filtered
}
