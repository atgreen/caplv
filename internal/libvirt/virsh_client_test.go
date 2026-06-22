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
