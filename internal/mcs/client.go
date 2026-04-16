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

// Package mcs provides a client for fetching OpenShift worker ignition
// configs. When CAPLV runs on an OpenShift cluster, it reads the rendered
// worker MachineConfig from the Kubernetes API and wraps it into a
// bootstrap ignition config that the Machine Config Daemon can consume.
package mcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var machineConfigGVK = schema.GroupVersionKind{
	Group:   "machineconfiguration.openshift.io",
	Version: "v1",
	Kind:    "MachineConfig",
}

// FetchWorkerIgnition reads the rendered worker MachineConfig from the
// OpenShift API and wraps it into a bootstrap ignition config that the
// Machine Config Daemon can consume on first boot.
//
// The MCS normally serves this via HTTPS on port 22623, but that
// endpoint is only reachable from nodes (not pods). Instead, we read
// the MachineConfig API directly and construct the same ignition
// structure the MCS would serve.
func FetchWorkerIgnition(ctx context.Context, k8sClient client.Client) ([]byte, error) {
	// Get the worker MachineConfigPool to find the rendered config name.
	mcp := &unstructured.Unstructured{}
	mcp.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "machineconfiguration.openshift.io",
		Version: "v1",
		Kind:    "MachineConfigPool",
	})
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: "worker"}, mcp); err != nil {
		return nil, fmt.Errorf("failed to get worker MachineConfigPool: %w", err)
	}

	// Extract the rendered config name.
	renderedName, found, err := unstructured.NestedString(mcp.Object, "status", "configuration", "name")
	if err != nil || !found || renderedName == "" {
		return nil, fmt.Errorf("worker MachineConfigPool has no rendered configuration")
	}

	// Get the rendered MachineConfig.
	mc := &unstructured.Unstructured{}
	mc.SetGroupVersionKind(machineConfigGVK)
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: renderedName}, mc); err != nil {
		return nil, fmt.Errorf("failed to get rendered MachineConfig %s: %w", renderedName, err)
	}

	// Extract spec.config (the ignition config portion).
	specConfig, found, err := unstructured.NestedMap(mc.Object, "spec", "config")
	if err != nil || !found {
		return nil, fmt.Errorf("rendered MachineConfig %s has no spec.config", renderedName)
	}

	// Marshal the full MachineConfig for embedding.
	mcJSON, err := json.Marshal(mc.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal MachineConfig: %w", err)
	}

	// Build the bootstrap ignition config that wraps the MachineConfig.
	// This replicates what the MCS serves: the base ignition config from
	// spec.config, plus two extra files that trigger the MCD first-boot flow.
	ignition := specConfig

	// Ensure storage.files exists.
	storage, _ := ignition["storage"].(map[string]any)
	if storage == nil {
		storage = map[string]any{}
		ignition["storage"] = storage
	}

	files, _ := storage["files"].([]any)

	// Add /etc/ignition-machine-config-encapsulated.json — contains the
	// full MachineConfig. The MCD and other first-boot services (like
	// rhcos-fips) read this to determine configuration.
	files = append(files, map[string]any{
		"path":      "/etc/ignition-machine-config-encapsulated.json",
		"mode":      0o420,
		"overwrite": true,
		"contents": map[string]any{
			"source": "data:," + url.PathEscape(string(mcJSON)),
		},
	})

	// Add /etc/mcs-machine-config-content.json — duplicate of the above,
	// used by the MCD pull service as an alternative source.
	files = append(files, map[string]any{
		"path":      "/etc/mcs-machine-config-content.json",
		"mode":      0o420,
		"overwrite": true,
		"contents": map[string]any{
			"source": "data:," + url.PathEscape(string(mcJSON)),
		},
	})

	storage["files"] = files

	data, err := json.Marshal(ignition)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal bootstrap ignition: %w", err)
	}

	return data, nil
}
