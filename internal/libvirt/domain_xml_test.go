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
	if !strings.Contains(xml, `firmware="efi"`) {
		t.Error("UEFI XML should contain firmware=\"efi\" attribute")
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
