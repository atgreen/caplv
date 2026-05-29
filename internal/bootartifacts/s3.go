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

// S3Resolver is a stub. The real implementation will pull kernel+initramfs
// from an S3-compatible object store.
type S3Resolver struct{}

// NewS3Resolver returns the stub S3 resolver.
func NewS3Resolver() *S3Resolver { return &S3Resolver{} }

// Resolve always returns an error until S3 support is implemented.
func (r *S3Resolver) Resolve(_ context.Context, _ infrav1.BootArtifactsSource) (*Artifacts, error) {
	return nil, fmt.Errorf("S3 source not yet implemented")
}
