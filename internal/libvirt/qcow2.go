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

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

const qcow2Magic uint32 = 0x514649fb // "QFI\xfb"

// Qcow2VirtualSize reads the qcow2 header of path and returns the virtual
// disk size in bytes. The header layout (per the qcow2 spec, fields we use):
//
//	bytes  0- 3  magic uint32 BE  ("QFI\xfb")
//	bytes  4- 7  version uint32 BE  (2 or 3)
//	bytes 24-31  size    uint64 BE  virtual disk size
func Qcow2VirtualSize(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var hdr [32]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return 0, fmt.Errorf("read header %s: %w", path, err)
	}
	if magic := binary.BigEndian.Uint32(hdr[0:4]); magic != qcow2Magic {
		return 0, fmt.Errorf("%s is not a qcow2 (magic 0x%08x)", path, magic)
	}
	if version := binary.BigEndian.Uint32(hdr[4:8]); version != 2 && version != 3 {
		return 0, fmt.Errorf("%s has unsupported qcow2 version %d", path, version)
	}
	size := binary.BigEndian.Uint64(hdr[24:32])
	if size > 1<<62 {
		return 0, fmt.Errorf("%s declares implausible virtual size %d", path, size)
	}
	return int64(size), nil
}
