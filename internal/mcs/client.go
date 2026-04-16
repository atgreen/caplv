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

// Package mcs provides a client for the OpenShift Machine Config Server.
// When CAPLV runs on an OpenShift cluster, it can fetch worker ignition
// configs directly from the in-cluster MCS, eliminating the need for
// users to manually create bootstrap secrets.
package mcs

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// ignitionV3Accept is the Accept header to request ignition spec 3.x.
	ignitionV3Accept = "application/vnd.coreos.ignition+json;version=3.2.0"

	mcsNamespace   = "openshift-machine-config-operator"
	mcsServiceName = "machine-config-server"
)

// FetchWorkerIgnition fetches the worker ignition config from the
// OpenShift Machine Config Server. It discovers the MCS endpoint
// from the Kubernetes Endpoints resource to bypass ClusterIP routing
// issues on SNO clusters.
func FetchWorkerIgnition(ctx context.Context, k8sClient client.Client) ([]byte, error) {
	endpoints := &corev1.Endpoints{}
	key := types.NamespacedName{
		Namespace: mcsNamespace,
		Name:      mcsServiceName,
	}
	if err := k8sClient.Get(ctx, key, endpoints); err != nil {
		return nil, fmt.Errorf("failed to get MCS endpoints: %w", err)
	}

	// Find the MCS pod IP from the endpoints.
	for _, subset := range endpoints.Subsets {
		for _, addr := range subset.Addresses {
			// Try HTTP on port 22624 first (unauthenticated), then HTTPS on 22623.
			for _, endpoint := range []string{
				fmt.Sprintf("http://%s:22624", addr.IP),
				fmt.Sprintf("https://%s:22623", addr.IP),
			} {
				data, err := FetchIgnition(ctx, endpoint, "worker")
				if err == nil {
					return data, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("failed to fetch ignition from any MCS endpoint")
}

// FetchIgnition fetches an ignition config for the given role from
// the specified MCS endpoint.
func FetchIgnition(ctx context.Context, endpoint, role string) ([]byte, error) {
	url := fmt.Sprintf("%s/config/%s", endpoint, role)

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, //nolint:gosec // MCS uses self-signed certs in-cluster
			},
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", ignitionV3Accept)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch ignition from MCS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("MCS returned status %d: %s", resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read MCS response: %w", err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("MCS returned empty ignition config")
	}

	return data, nil
}
