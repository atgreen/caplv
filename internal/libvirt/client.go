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

package libvirt

import "context"

// DomainInfo holds basic information about a libvirt domain.
type DomainInfo struct {
	Name  string
	UUID  string
	State string // "running", "shutoff", "paused", "crashed"
}

// NodeInfo holds host hardware information from virsh nodeinfo.
type NodeInfo struct {
	CPUs     int32 // total CPU threads (cores * threads * sockets)
	MemoryKB int64 // total memory in KB
}

// Client defines the interface for interacting with a libvirt host.
type Client interface {
	Ping(ctx context.Context) error
	GetNodeInfo(ctx context.Context) (*NodeInfo, error)
	DomainExists(ctx context.Context, name string) (bool, error)
	GetDomain(ctx context.Context, name string) (*DomainInfo, error)
	DefineDomain(ctx context.Context, xmlDef string) (*DomainInfo, error)
	StartDomain(ctx context.Context, name string) error
	DestroyDomain(ctx context.Context, name string) error
	UndefineDomain(ctx context.Context, name string) error
	VolumeExists(ctx context.Context, pool, name string) (bool, error)
	CreateVolumeFromBackingStore(ctx context.Context, pool, name, backingPath string, sizeBytes int64) error
	CloneVolume(ctx context.Context, pool, sourceName, targetName string) error
	CreateVolume(ctx context.Context, pool, name string, sizeBytes int64) error
	UploadVolumeFromBytes(ctx context.Context, pool, name string, data []byte) error
	DeleteVolume(ctx context.Context, pool, name string) error
	GetVolumePath(ctx context.Context, pool, name string) (string, error)
	Close() error
}
