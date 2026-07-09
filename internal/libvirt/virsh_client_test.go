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

import "testing"

func TestShellQuoteEscapesMetacharacters(t *testing.T) {
	got := shellJoin("virsh", "vol-info", "root.qcow2", "--pool", "default; printf INJECTED")
	want := "'virsh' 'vol-info' 'root.qcow2' '--pool' 'default; printf INJECTED'"
	if got != want {
		t.Fatalf("shellJoin() = %q, want %q", got, want)
	}
}

func TestShellQuoteEscapesSingleQuotes(t *testing.T) {
	got := shellQuote("pool'$(id)")
	want := `'pool'\''$(id)'`
	if got != want {
		t.Fatalf("shellQuote() = %q, want %q", got, want)
	}
}

func TestLocalConnectionURI(t *testing.T) {
	tests := []struct {
		uri     string
		want    string
		wantErr bool
	}{
		{uri: "qemu+ssh://caplv@host.example.com/system", want: ConnURISystem},
		{uri: "qemu+ssh://caplv@host.example.com:2222/system", want: ConnURISystem},
		{uri: "qemu+ssh://caplv@host.example.com/session", want: ConnURISession},
		{uri: "qemu+ssh://caplv@host.example.com", want: ConnURISystem},
		{uri: "qemu+ssh://caplv@host.example.com/", want: ConnURISystem},
		{uri: "qemu+ssh://caplv@host.example.com/sessions", wantErr: true},
		{uri: "qemu+ssh://caplv@host.example.com/foo", wantErr: true},
	}
	for _, tt := range tests {
		got, err := LocalConnectionURI(tt.uri)
		if tt.wantErr {
			if err == nil {
				t.Errorf("LocalConnectionURI(%q) expected error, got %q", tt.uri, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("LocalConnectionURI(%q) unexpected error: %v", tt.uri, err)
			continue
		}
		if got != tt.want {
			t.Errorf("LocalConnectionURI(%q) = %q, want %q", tt.uri, got, tt.want)
		}
	}
}

func TestNewVirshClientDefaultsToSystem(t *testing.T) {
	c := NewVirshClient(nil, "")
	if c.connURI != ConnURISystem {
		t.Fatalf("connURI = %q, want %q", c.connURI, ConnURISystem)
	}
	c = NewVirshClient(nil, ConnURISession)
	if c.connURI != ConnURISession {
		t.Fatalf("connURI = %q, want %q", c.connURI, ConnURISession)
	}
}
