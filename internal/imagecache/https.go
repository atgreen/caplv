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

package imagecache

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	infrav1 "github.com/atgreen/caplv/api/v1alpha1"
)

// HTTPSResolver streams a qcow2 over HTTPS (or HTTP — the controller does
// not refuse plain HTTP for base images, since some on-prem Artifactory
// instances run TLS termination upstream).
type HTTPSResolver struct {
	Client *http.Client
}

// NewHTTPSResolver returns an HTTPSResolver with a long fetch timeout sized
// for ~1 GB qcow2 payloads on cold caches.
func NewHTTPSResolver() *HTTPSResolver {
	return &HTTPSResolver{
		Client: &http.Client{Timeout: 30 * time.Minute},
	}
}

// Open issues the GET and returns the response body. Caller closes.
func (r *HTTPSResolver) Open(ctx context.Context, src infrav1.BaseImageSource, creds *Credentials) (io.ReadCloser, error) {
	if src.HTTPS == nil {
		return nil, fmt.Errorf("https source not set")
	}
	url := src.HTTPS.URL
	if !strings.HasPrefix(strings.ToLower(url), "http://") &&
		!strings.HasPrefix(strings.ToLower(url), "https://") {
		return nil, fmt.Errorf("baseImage URL %q is not http(s)", url)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if creds != nil && (creds.Username != "" || creds.Password != "") {
		auth := base64.StdEncoding.EncodeToString([]byte(creds.Username + ":" + creds.Password))
		req.Header.Set("Authorization", "Basic "+auth)
	}

	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("get %s: unexpected status %d", url, resp.StatusCode)
	}
	return resp.Body, nil
}
