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

package ssh

import (
	"testing"
)

func TestParseLibvirtURI_StandardSSH(t *testing.T) {
	user, host, port, err := ParseLibvirtURI("qemu+ssh://root@host.example.com/system")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user != "root" {
		t.Errorf("expected user 'root', got %q", user)
	}
	if host != "host.example.com" {
		t.Errorf("expected host 'host.example.com', got %q", host)
	}
	if port != 22 {
		t.Errorf("expected port 22, got %d", port)
	}
}

func TestParseLibvirtURI_CustomPort(t *testing.T) {
	user, host, port, err := ParseLibvirtURI("qemu+ssh://admin@host:2222/system")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user != "admin" {
		t.Errorf("expected user 'admin', got %q", user)
	}
	if host != "host" {
		t.Errorf("expected host 'host', got %q", host)
	}
	if port != 2222 {
		t.Errorf("expected port 2222, got %d", port)
	}
}

func TestParseLibvirtURI_NotSSH(t *testing.T) {
	_, _, _, err := ParseLibvirtURI("qemu:///system")
	if err == nil {
		t.Error("expected error for non-SSH URI, got nil")
	}
}

func TestParseLibvirtURI_InvalidURI(t *testing.T) {
	_, _, _, err := ParseLibvirtURI("://not a valid uri")
	if err == nil {
		t.Error("expected error for invalid URI, got nil")
	}
}
