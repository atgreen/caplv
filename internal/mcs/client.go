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
// worker MachineConfig directly from the Kubernetes API, eliminating the
// need for users to manually create bootstrap secrets.
package mcs

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var machineConfigPoolGVR = schema.GroupVersionResource{
	Group:    "machineconfiguration.openshift.io",
	Version:  "v1",
	Resource: "machineconfigpools",
}

var machineConfigGVK = schema.GroupVersionKind{
	Group:   "machineconfiguration.openshift.io",
	Version: "v1",
	Kind:    "MachineConfig",
}

// FetchWorkerIgnition reads the rendered worker ignition config from the
// OpenShift MachineConfig API. It looks up the current rendered worker
// MachineConfig name from the worker MachineConfigPool, then extracts
// the ignition config from spec.config.
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

	// Extract the rendered config name from status.configuration.name.
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

	// Extract spec.config (the ignition config).
	config, found, err := unstructured.NestedMap(mc.Object, "spec", "config")
	if err != nil || !found {
		return nil, fmt.Errorf("rendered MachineConfig %s has no spec.config", renderedName)
	}

	// Marshal the ignition config to JSON.
	data, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal ignition config: %w", err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("rendered MachineConfig produced empty ignition config")
	}

	return data, nil
}
