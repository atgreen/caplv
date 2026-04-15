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
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// VirshClient implements Client using virsh commands over SSH.
type VirshClient struct {
	sshClient *ssh.Client
	// localMode is true when running against local libvirt (no SSH).
	localMode bool
}

// NewVirshClient creates a new VirshClient that executes virsh commands over SSH.
func NewVirshClient(sshClient *ssh.Client) *VirshClient {
	return &VirshClient{sshClient: sshClient}
}

// NewLocalVirshClient creates a new VirshClient that executes virsh commands locally.
func NewLocalVirshClient() *VirshClient {
	return &VirshClient{localMode: true}
}

func (c *VirshClient) runVirsh(ctx context.Context, args ...string) (string, error) {
	cmdArgs := append([]string{"virsh", "-c", "qemu:///system"}, args...)
	cmd := strings.Join(cmdArgs, " ")

	if c.localMode {
		// For local mode, would use exec.CommandContext.
		// Not implemented in Phase 1 — SSH is the primary path.
		return "", fmt.Errorf("local mode not implemented")
	}

	session, err := c.sshClient.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	// Set deadline via context.
	done := make(chan error, 1)
	go func() {
		done <- session.Run(cmd)
	}()

	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGTERM)
		return "", ctx.Err()
	case err := <-done:
		if err != nil {
			stderrStr := strings.TrimSpace(stderr.String())
			if stderrStr != "" {
				return "", ClassifyVirshError(stderrStr, args[0], getResourceArg(args))
			}
			return "", fmt.Errorf("virsh %s: %w", args[0], err)
		}
	}

	return strings.TrimSpace(stdout.String()), nil
}

func getResourceArg(args []string) string {
	if len(args) > 1 {
		return args[len(args)-1]
	}
	return ""
}

// Ping checks connectivity to the libvirt host.
func (c *VirshClient) Ping(ctx context.Context) error {
	_, err := c.runVirsh(ctx, "version")
	return err
}

// GetNodeInfo returns host hardware information from virsh nodeinfo.
func (c *VirshClient) GetNodeInfo(ctx context.Context) (*NodeInfo, error) {
	output, err := c.runVirsh(ctx, "nodeinfo")
	if err != nil {
		return nil, err
	}
	info := &NodeInfo{}
	var cores, threads, sockets int64
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// Strip units (e.g., "65390 KiB" -> "65390")
		val = strings.Fields(val)[0]
		switch key {
		case "CPU socket(s)":
			sockets, _ = strconv.ParseInt(val, 10, 32)
		case "Core(s) per socket":
			cores, _ = strconv.ParseInt(val, 10, 32)
		case "Thread(s) per core":
			threads, _ = strconv.ParseInt(val, 10, 32)
		case "Memory size":
			info.MemoryKB, _ = strconv.ParseInt(val, 10, 64)
		case "CPU(s)":
			// Total CPUs reported directly — use as fallback
			cpus, _ := strconv.ParseInt(val, 10, 32)
			info.CPUs = int32(cpus)
		}
	}
	// Prefer computed value if all components are available.
	if sockets > 0 && cores > 0 && threads > 0 {
		info.CPUs = int32(sockets * cores * threads)
	}
	return info, nil
}

// DomainExists returns true if a domain with the given name exists.
func (c *VirshClient) DomainExists(ctx context.Context, name string) (bool, error) {
	_, err := c.runVirsh(ctx, "dominfo", name)
	if err != nil {
		if IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// GetDomain retrieves information about a domain by name.
func (c *VirshClient) GetDomain(ctx context.Context, name string) (*DomainInfo, error) {
	output, err := c.runVirsh(ctx, "dominfo", name)
	if err != nil {
		return nil, err
	}
	info := &DomainInfo{Name: name}
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "UUID":
			info.UUID = val
		case "State":
			info.State = val
		}
	}
	return info, nil
}

// DefineDomain defines a new domain from the given XML definition.
func (c *VirshClient) DefineDomain(ctx context.Context, xmlDef string) (*DomainInfo, error) {
	// Write XML to temp file on remote host, then virsh define it.
	tmpFile := fmt.Sprintf("/tmp/caplv-domain-%d.xml", time.Now().UnixNano())

	// Upload XML via SSH.
	session, err := c.sshClient.NewSession()
	if err != nil {
		return nil, fmt.Errorf("ssh session: %w", err)
	}
	session.Stdin = strings.NewReader(xmlDef)
	if err := session.Run(fmt.Sprintf("cat > %s", tmpFile)); err != nil {
		session.Close()
		return nil, fmt.Errorf("upload domain XML: %w", err)
	}
	session.Close()

	// Define the domain.
	_, err = c.runVirsh(ctx, "define", tmpFile)
	if err != nil {
		return nil, err
	}

	// Clean up temp file via SSH.
	cleanSession, _ := c.sshClient.NewSession()
	if cleanSession != nil {
		_ = cleanSession.Run(fmt.Sprintf("rm -f %s", tmpFile))
		cleanSession.Close()
	}

	return c.GetDomain(ctx, extractDomainNameFromXML(xmlDef))
}

func extractDomainNameFromXML(xml string) string {
	// Simple extraction — look for <name>...</name>.
	start := strings.Index(xml, "<name>")
	end := strings.Index(xml, "</name>")
	if start >= 0 && end > start {
		return xml[start+6 : end]
	}
	return ""
}

// StartDomain starts a defined domain.
func (c *VirshClient) StartDomain(ctx context.Context, name string) error {
	_, err := c.runVirsh(ctx, "start", name)
	return err
}

// DestroyDomain forcibly stops a running domain.
func (c *VirshClient) DestroyDomain(ctx context.Context, name string) error {
	_, err := c.runVirsh(ctx, "destroy", name)
	if IsNotFound(err) {
		return nil // already off or gone
	}
	return err
}

// UndefineDomain removes a domain definition.
func (c *VirshClient) UndefineDomain(ctx context.Context, name string) error {
	_, err := c.runVirsh(ctx, "undefine", name, "--nvram")
	if IsNotFound(err) {
		return nil
	}
	return err
}

// VolumeExists returns true if a volume with the given name exists in the pool.
func (c *VirshClient) VolumeExists(ctx context.Context, pool, name string) (bool, error) {
	_, err := c.runVirsh(ctx, "vol-info", name, "--pool", pool)
	if err != nil {
		if IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// CreateVolumeFromBackingStore creates a new qcow2 volume backed by an existing volume.
func (c *VirshClient) CreateVolumeFromBackingStore(ctx context.Context, pool, name, backingPath string, sizeBytes int64) error {
	const bytesPerMB = 1024 * 1024
	sizeMB := sizeBytes / bytesPerMB
	_, err := c.runVirsh(ctx, "vol-create-as", pool, name,
		fmt.Sprintf("%dM", sizeMB),
		"--format", "qcow2",
		"--backing-vol-format", "qcow2",
		"--backing-vol", backingPath)
	return err
}

// CloneVolume clones an existing volume within the same pool.
func (c *VirshClient) CloneVolume(ctx context.Context, pool, sourceName, targetName string) error {
	_, err := c.runVirsh(ctx, "vol-clone", sourceName, targetName, "--pool", pool)
	return err
}

// CreateVolume creates a new qcow2 volume in the specified pool.
func (c *VirshClient) CreateVolume(ctx context.Context, pool, name string, sizeBytes int64) error {
	const bytesPerMB = 1024 * 1024
	sizeMB := sizeBytes / bytesPerMB
	_, err := c.runVirsh(ctx, "vol-create-as", pool, name, fmt.Sprintf("%dM", sizeMB), "--format", "qcow2")
	return err
}

// UploadVolumeFromBytes uploads raw bytes to a new volume in the specified pool.
func (c *VirshClient) UploadVolumeFromBytes(ctx context.Context, pool, name string, data []byte) error {
	tmpFile := fmt.Sprintf("/tmp/caplv-upload-%d", time.Now().UnixNano())

	// Upload data via SSH.
	session, err := c.sshClient.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session: %w", err)
	}
	session.Stdin = bytes.NewReader(data)
	if err := session.Run(fmt.Sprintf("cat > %s", tmpFile)); err != nil {
		session.Close()
		return fmt.Errorf("upload data: %w", err)
	}
	session.Close()

	// Create raw volume for the ISO.
	sizeBytes := int64(len(data))
	_, err = c.runVirsh(ctx, "vol-create-as", pool, name,
		fmt.Sprintf("%d", sizeBytes),
		"--format", "raw")
	if err != nil {
		return err
	}

	// Upload to the volume.
	_, err = c.runVirsh(ctx, "vol-upload", name, tmpFile, "--pool", pool)

	// Clean up temp file.
	cleanSession, _ := c.sshClient.NewSession()
	if cleanSession != nil {
		_ = cleanSession.Run(fmt.Sprintf("rm -f %s", tmpFile))
		cleanSession.Close()
	}

	return err
}

// DeleteVolume deletes a volume from the specified pool.
func (c *VirshClient) DeleteVolume(ctx context.Context, pool, name string) error {
	_, err := c.runVirsh(ctx, "vol-delete", name, "--pool", pool)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// GetVolumePath returns the filesystem path of a volume.
func (c *VirshClient) GetVolumePath(ctx context.Context, pool, name string) (string, error) {
	return c.runVirsh(ctx, "vol-path", name, "--pool", pool)
}

// Close closes the underlying SSH connection.
func (c *VirshClient) Close() error {
	if c.sshClient != nil {
		return c.sshClient.Close()
	}
	return nil
}
