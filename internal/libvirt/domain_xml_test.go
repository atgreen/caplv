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
	"strings"
	"testing"
)

func baseParams() DomainXMLParams {
	return DomainXMLParams{
		Name:         "test-domain",
		UUID:         "abcd-1234",
		VCPUs:        2,
		MemoryKB:     2097152,
		Machine:      "q35",
		Firmware:     "bios",
		RootDiskPath: "/var/lib/libvirt/images/root.qcow2",
		RootDiskBus:  "virtio",
		NetworkType:  "bridge",
		NetworkName:  "br0",
		NetworkModel: "virtio",
	}
}

func TestGenerateDomainXML_UEFIMode(t *testing.T) {
	params := baseParams()
	params.Firmware = "uefi"
	params.FirmwarePath = "/usr/share/OVMF/OVMF_CODE.fd"
	params.NVRAMPath = "/var/lib/libvirt/qemu/nvram/test_VARS.fd"

	xml, err := GenerateDomainXML(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(xml, "<loader") {
		t.Error("UEFI XML should contain <loader element")
	}
	if !strings.Contains(xml, "<nvram") {
		t.Error("UEFI XML should contain <nvram element")
	}
	// When an explicit firmware path is provided, firmware="efi" should NOT
	// be set (it conflicts with the explicit <loader> on newer libvirt).
	if strings.Contains(xml, `firmware="efi"`) {
		t.Error("UEFI XML with explicit loader path should not contain firmware=\"efi\" attribute")
	}
}

func TestGenerateDomainXML_BIOSMode(t *testing.T) {
	params := baseParams()
	params.Firmware = "bios"

	xml, err := GenerateDomainXML(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(xml, "<loader") {
		t.Error("BIOS XML should not contain <loader element")
	}
	if strings.Contains(xml, "<nvram") {
		t.Error("BIOS XML should not contain <nvram element")
	}
}

func TestGenerateDomainXML_BridgeNetwork(t *testing.T) {
	params := baseParams()
	params.NetworkType = "bridge"
	params.NetworkName = "br0"

	xml, err := GenerateDomainXML(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(xml, `bridge="br0"`) {
		t.Error("bridge network XML should contain source bridge attribute")
	}
}

func TestGenerateDomainXML_NetworkType(t *testing.T) {
	params := baseParams()
	params.NetworkType = "network"
	params.NetworkName = "default"

	xml, err := GenerateDomainXML(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(xml, `network="default"`) {
		t.Error("network type XML should contain source network attribute")
	}
}

func TestGenerateDomainXML_WithMACAddress(t *testing.T) {
	params := baseParams()
	params.MACAddress = "52:54:00:ab:cd:ef"

	xml, err := GenerateDomainXML(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(xml, `<mac address="52:54:00:ab:cd:ef"`) {
		t.Error("XML should contain mac address element when MACAddress is set")
	}
}

func TestGenerateDomainXML_WithoutMACAddress(t *testing.T) {
	params := baseParams()
	params.MACAddress = ""

	xml, err := GenerateDomainXML(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(xml, "<mac") {
		t.Error("XML should not contain mac element when MACAddress is empty")
	}
}

func TestGenerateDomainXML_AdditionalDisks(t *testing.T) {
	params := baseParams()
	params.AdditionalDisks = []DiskParam{
		{Path: "/var/lib/libvirt/images/data1.qcow2", Bus: "virtio"},
		{Path: "/var/lib/libvirt/images/data2.qcow2", Bus: "sata"},
	}

	xml, err := GenerateDomainXML(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(xml, "data1.qcow2") {
		t.Error("XML should contain first additional disk path")
	}
	if !strings.Contains(xml, "data2.qcow2") {
		t.Error("XML should contain second additional disk path")
	}
}

func TestGenerateDomainXML_DirectKernelBoot(t *testing.T) {
	params := baseParams()
	params.KernelPath = "/var/lib/caplv/boot/abc/vmlinuz"
	params.InitrdPath = "/var/lib/caplv/boot/abc/initramfs.img"
	params.KernelCmdline = "ignition.platform.id=metal console=ttyS0"
	params.IgnitionPath = "/run/caplv/ignition/x.ign"

	out, err := GenerateDomainXML(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "<kernel>/var/lib/caplv/boot/abc/vmlinuz</kernel>") {
		t.Error("XML should contain <kernel> element with direct-boot kernel path")
	}
	if !strings.Contains(out, "<initrd>/var/lib/caplv/boot/abc/initramfs.img</initrd>") {
		t.Error("XML should contain <initrd> element with direct-boot initramfs path")
	}
	if !strings.Contains(out, "<cmdline>ignition.platform.id=metal console=ttyS0</cmdline>") {
		t.Error("XML should contain <cmdline> element with kernel args")
	}
	if strings.Contains(out, "<sysinfo") {
		t.Error("XML should NOT contain <sysinfo> when direct kernel boot is active")
	}
	if !strings.Contains(out, "<serial>ignition</serial>") {
		t.Error("XML should contain ignition virtio-blk disk with serial=ignition")
	}
	if !strings.Contains(out, `<source file="/run/caplv/ignition/x.ign"></source>`) {
		t.Error("XML should reference the ignition file as a disk source")
	}
	// The ignition disk must be read-only.
	idx := strings.Index(out, "<serial>ignition</serial>")
	if idx < 0 || !strings.Contains(out[idx:], "<readonly></readonly>") {
		t.Error("ignition disk should be <readonly/>")
	}
}

func TestGenerateDomainXML_FwCfgIgnitionWithoutDirectKernel(t *testing.T) {
	params := baseParams()
	params.IgnitionPath = "/run/caplv/ignition/x.ign"

	out, err := GenerateDomainXML(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, `<sysinfo type="fwcfg">`) {
		t.Error("fw_cfg sysinfo should be present when KernelPath is empty")
	}
	if strings.Contains(out, "<serial>ignition</serial>") {
		t.Error("ignition virtio-blk disk should not be added when KernelPath is empty")
	}
	if strings.Contains(out, "<kernel>") {
		t.Error("XML should not contain <kernel> element without direct-boot")
	}
}

func TestGenerateDomainXML_DirectKernelDiskLetters(t *testing.T) {
	params := baseParams()
	params.KernelPath = "/k"
	params.InitrdPath = "/i"
	params.KernelCmdline = "x"
	params.IgnitionPath = "/run/x.ign"
	params.AdditionalDisks = []DiskParam{
		{Path: "/data1.qcow2", Bus: "virtio"},
		{Path: "/data2.qcow2", Bus: "virtio"},
	}

	out, err := GenerateDomainXML(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Root disk vda, ignition disk vdb, then additional disks vdc, vdd.
	if !strings.Contains(out, `dev="vda"`) {
		t.Error("expected root disk on vda")
	}
	if !strings.Contains(out, `dev="vdb"`) {
		t.Error("expected ignition disk on vdb")
	}
	if !strings.Contains(out, `dev="vdc"`) {
		t.Error("expected first additional disk on vdc")
	}
	if !strings.Contains(out, `dev="vdd"`) {
		t.Error("expected second additional disk on vdd")
	}
}

func TestGenerateDomainXML_DomainName(t *testing.T) {
	params := baseParams()
	params.Name = "my-special-domain"

	xml, err := GenerateDomainXML(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(xml, "<name>my-special-domain</name>") {
		t.Error("XML should contain the domain name in <name> element")
	}
}
