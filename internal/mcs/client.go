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
)

const (
	// DefaultMCSEndpoint is the in-cluster Machine Config Server endpoint.
	// Port 22624 serves ignition configs over HTTP without client certificates.
	DefaultMCSEndpoint = "http://machine-config-server.openshift-machine-config-operator.svc:22624"

	// ignitionV3Accept is the Accept header to request ignition spec 3.x.
	ignitionV3Accept = "application/vnd.coreos.ignition+json;version=3.2.0"
)

// FetchWorkerIgnition fetches the worker ignition config from the
// OpenShift Machine Config Server. The MCS runs in-cluster and serves
// ignition configs for worker nodes.
//
// The MCS TLS cert is self-signed, so we skip verification. This is
// safe because we're connecting to an in-cluster service.
func FetchWorkerIgnition(ctx context.Context) ([]byte, error) {
	return FetchIgnition(ctx, DefaultMCSEndpoint, "worker")
}

// FetchIgnition fetches an ignition config for the given role from
// the specified MCS endpoint.
func FetchIgnition(ctx context.Context, endpoint, role string) ([]byte, error) {
	url := fmt.Sprintf("%s/config/%s", endpoint, role)

	client := &http.Client{
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

	resp, err := client.Do(req)
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

// IsAvailable checks whether the MCS is reachable in-cluster.
func IsAvailable(ctx context.Context) bool {
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, //nolint:gosec
			},
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, DefaultMCSEndpoint+"/healthz", nil)
	if err != nil {
		return false
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	// MCS may return 404 for /healthz but the connection succeeding is enough.
	return true
}
