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
	"bufio"
	"compress/gzip"
	"io"
)

// maybeGunzipStream peeks the first two bytes; if they're the gzip magic
// (0x1f 0x8b) the stream is wrapped in a gzip.Reader, otherwise the
// (buffered) original is returned. Either way the returned Reader yields
// the decompressed payload — which is what we hash and stage on the host.
func maybeGunzipStream(r io.Reader) (io.Reader, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	head, err := br.Peek(2)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(head) < 2 || head[0] != 0x1f || head[1] != 0x8b {
		return br, nil
	}
	return gzip.NewReader(br)
}
