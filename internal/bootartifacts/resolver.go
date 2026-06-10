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

package bootartifacts

import (
	"context"
	"fmt"

	infrav1 "github.com/atgreen/caplv/api/v1alpha1"
)

// Artifacts holds the fetched kernel and initramfs bytes plus their sha256 hex digests.
type Artifacts struct {
	KernelBytes     []byte
	KernelSHA256    string
	InitramfsBytes  []byte
	InitramfsSHA256 string
}

// Credentials carries the resolved pull secrets for one Resolve call. The
// controller is responsible for reading the referenced Kubernetes Secret and
// constructing this struct; resolvers never touch the kube client.
//
// A nil Credentials value means "anonymous". Fields are interpreted per
// transport:
//   - OCI: Username/Password (registry basic auth or token).
//   - S3:  AccessKeyID/SecretAccessKey/SessionToken.
//   - HTTPS: not used today (digests are sufficient).
type Credentials struct {
	Username        string
	Password        string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// Resolver fetches Artifacts described by a BootArtifactsSource.
type Resolver interface {
	Resolve(ctx context.Context, src infrav1.BootArtifactsSource, creds *Credentials) (*Artifacts, error)
}

// MultiResolver dispatches Resolve to the transport-specific implementation
// indicated by src.Type.
type MultiResolver struct {
	HTTPS Resolver
	OCI   Resolver
	S3    Resolver
}

// NewMultiResolver returns a MultiResolver wired with the default
// implementations for each supported transport.
func NewMultiResolver() *MultiResolver {
	return &MultiResolver{
		HTTPS: NewHTTPSResolver(),
		OCI:   NewOCIResolver(),
		S3:    NewS3Resolver(),
	}
}

// Resolve dispatches to the configured per-type Resolver.
func (m *MultiResolver) Resolve(ctx context.Context, src infrav1.BootArtifactsSource, creds *Credentials) (*Artifacts, error) {
	switch src.Type {
	case infrav1.BootArtifactsSourceHTTPS:
		if src.HTTPS == nil {
			return nil, fmt.Errorf("bootArtifacts.source.https is required when type=HTTPS")
		}
		return m.HTTPS.Resolve(ctx, src, creds)
	case infrav1.BootArtifactsSourceOCI:
		if src.OCI == nil {
			return nil, fmt.Errorf("bootArtifacts.source.oci is required when type=OCI")
		}
		return m.OCI.Resolve(ctx, src, creds)
	case infrav1.BootArtifactsSourceS3:
		if src.S3 == nil {
			return nil, fmt.Errorf("bootArtifacts.source.s3 is required when type=S3")
		}
		return m.S3.Resolve(ctx, src, creds)
	default:
		return nil, fmt.Errorf("unsupported bootArtifacts source type %q", src.Type)
	}
}
