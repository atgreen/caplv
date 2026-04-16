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
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	gossh "golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"

	infrav1 "github.com/atgreen/caplv/api/v1alpha1"
)

const defaultSSHPort = 22

// NewSSHClient creates an SSH client connection to the libvirt host using credentials
// from the provided Secret.
func NewSSHClient(ctx context.Context, host *infrav1.LibvirtHost, secret *corev1.Secret) (*gossh.Client, error) {
	user, hostname, port, err := ParseLibvirtURI(host.Spec.URI)
	if err != nil {
		return nil, fmt.Errorf("parsing libvirt URI: %w", err)
	}

	privateKeyBytes, ok := secret.Data["ssh-privatekey"]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s missing 'ssh-privatekey' key", secret.Namespace, secret.Name)
	}

	signer, err := gossh.ParsePrivateKey(privateKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("parsing SSH private key: %w", err)
	}

	config := &gossh.ClientConfig{
		User: user,
		Auth: []gossh.AuthMethod{
			gossh.PublicKeys(signer),
		},
		HostKeyCallback: VerifyHostKey(host.Spec.HostKeyFingerprint),
	}

	addr := net.JoinHostPort(hostname, strconv.Itoa(port))

	// Use a dialer that respects context cancellation.
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dialing %s: %w", addr, err)
	}

	sshConn, chans, reqs, err := gossh.NewClientConn(conn, addr, config)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("SSH handshake with %s: %w", addr, err)
	}

	return gossh.NewClient(sshConn, chans, reqs), nil
}

// VerifyHostKey returns an SSH HostKeyCallback that verifies the remote host key
// fingerprint matches the expected SHA256 fingerprint. If expectedFingerprint is
// empty, any host key is accepted (insecure).
func VerifyHostKey(expectedFingerprint string) gossh.HostKeyCallback {
	if expectedFingerprint == "" {
		return gossh.InsecureIgnoreHostKey()
	}
	return func(hostname string, remote net.Addr, key gossh.PublicKey) error {
		actual := gossh.FingerprintSHA256(key)
		if actual != expectedFingerprint {
			return fmt.Errorf("host key mismatch for %s: expected %s, got %s", hostname, expectedFingerprint, actual)
		}
		return nil
	}
}

// ParseLibvirtURI parses a libvirt connection URI into its SSH components.
// Supported formats:
//   - qemu+ssh://root@host/system
//   - qemu+ssh://root@host:2222/system
//
// Returns an error for non-SSH URIs (e.g. qemu:///system).
func ParseLibvirtURI(uri string) (user, host string, port int, err error) {
	if !strings.Contains(uri, "+ssh://") {
		return "", "", 0, fmt.Errorf("not an SSH URI: %s", uri)
	}

	parsed, err := url.Parse(uri)
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid URI %q: %w", uri, err)
	}

	host = parsed.Hostname()
	if host == "" {
		return "", "", 0, fmt.Errorf("no host in URI %q", uri)
	}

	port = defaultSSHPort
	if parsed.Port() != "" {
		port, err = strconv.Atoi(parsed.Port())
		if err != nil {
			return "", "", 0, fmt.Errorf("invalid port in URI %q: %w", uri, err)
		}
	}

	user = "root" // default
	if parsed.User != nil && parsed.User.Username() != "" {
		user = parsed.User.Username()
	}

	return user, host, port, nil
}
