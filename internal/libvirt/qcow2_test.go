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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeQcow2Header(t *testing.T, version uint32, virtualSize uint64) string {
	t.Helper()
	hdr := make([]byte, 64)
	binary.BigEndian.PutUint32(hdr[0:4], qcow2Magic)
	binary.BigEndian.PutUint32(hdr[4:8], version)
	binary.BigEndian.PutUint64(hdr[24:32], virtualSize)
	path := filepath.Join(t.TempDir(), "test.qcow2")
	if err := os.WriteFile(path, hdr, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestQcow2VirtualSize(t *testing.T) {
	const want = uint64(16 * 1024 * 1024 * 1024) // 16 GB
	path := writeQcow2Header(t, 3, want)
	got, err := Qcow2VirtualSize(path)
	if err != nil {
		t.Fatalf("Qcow2VirtualSize: %v", err)
	}
	if uint64(got) != want {
		t.Errorf("virtual size: got %d, want %d", got, want)
	}
}

func TestQcow2VirtualSize_RejectsNonQcow2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raw.bin")
	if err := os.WriteFile(path, make([]byte, 64), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Qcow2VirtualSize(path)
	if err == nil || !strings.Contains(err.Error(), "not a qcow2") {
		t.Errorf("expected not-qcow2 error, got %v", err)
	}
}

func TestQcow2VirtualSize_RejectsUnsupportedVersion(t *testing.T) {
	path := writeQcow2Header(t, 1, 1024)
	_, err := Qcow2VirtualSize(path)
	if err == nil || !strings.Contains(err.Error(), "unsupported qcow2 version") {
		t.Errorf("expected unsupported-version error, got %v", err)
	}
}
