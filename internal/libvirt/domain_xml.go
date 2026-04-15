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

import "encoding/xml"

// DomainXMLParams holds the parameters for generating a libvirt domain XML definition.
type DomainXMLParams struct {
	Name             string
	UUID             string
	VCPUs            int32
	MemoryKB         int64
	Machine          string
	Firmware         string // "uefi" or "bios"
	FirmwarePath     string
	NVRAMPath        string
	RootDiskPath     string
	RootDiskBus      string
	BootstrapISOPath string
	AdditionalDisks  []DiskParam
	NetworkType      string // "bridge" or "network"
	NetworkName      string
	NetworkModel     string
	MACAddress       string
}

// DiskParam defines an additional disk to attach to the domain.
type DiskParam struct {
	Path string
	Bus  string
}

// XML struct types matching the libvirt domain XML schema.

type domainXML struct {
	XMLName  xml.Name        `xml:"domain"`
	Type     string          `xml:"type,attr"`
	Name     string          `xml:"name"`
	UUID     string          `xml:"uuid,omitempty"`
	Memory   domainMemory    `xml:"memory"`
	VCPU     domainVCPU      `xml:"vcpu"`
	OS       domainOS        `xml:"os"`
	Features domainFeatures  `xml:"features"`
	CPU      domainCPU       `xml:"cpu"`
	Devices  domainDevices   `xml:"devices"`
}

type domainMemory struct {
	Unit  string `xml:"unit,attr"`
	Value int64  `xml:",chardata"`
}

type domainVCPU struct {
	Placement string `xml:"placement,attr"`
	Value     int32  `xml:",chardata"`
}

type domainOS struct {
	Firmware string        `xml:"firmware,attr,omitempty"`
	Type     domainOSType  `xml:"type"`
	Loader   *domainLoader `xml:"loader,omitempty"`
	NVRAM    *domainNVRAM  `xml:"nvram,omitempty"`
	Boot     domainBoot    `xml:"boot"`
}

type domainOSType struct {
	Arch    string `xml:"arch,attr"`
	Machine string `xml:"machine,attr"`
	Value   string `xml:",chardata"`
}

type domainLoader struct {
	ReadOnly string `xml:"readonly,attr"`
	Type     string `xml:"type,attr"`
	Value    string `xml:",chardata"`
}

type domainNVRAM struct {
	Value string `xml:",chardata"`
}

type domainFeatures struct {
	ACPI *struct{} `xml:"acpi"`
	APIC *struct{} `xml:"apic"`
}

type domainCPU struct {
	Mode string `xml:"mode,attr"`
}

type domainDevices struct {
	Disks      []domainDisk      `xml:"disk"`
	Interfaces []domainInterface `xml:"interface"`
	Serials    []domainSerial    `xml:"serial"`
	Consoles   []domainConsole   `xml:"console"`
	Channels   []domainChannel   `xml:"channel"`
}

type domainDisk struct {
	Type     string            `xml:"type,attr"`
	Device   string            `xml:"device,attr"`
	Driver   domainDiskDriver  `xml:"driver"`
	Source   domainDiskSource  `xml:"source"`
	Target   domainDiskTarget  `xml:"target"`
	ReadOnly *struct{}         `xml:"readonly,omitempty"`
}

type domainDiskDriver struct {
	Name string `xml:"name,attr"`
	Type string `xml:"type,attr"`
}

type domainDiskSource struct {
	File string `xml:"file,attr"`
}

type domainDiskTarget struct {
	Dev string `xml:"dev,attr"`
	Bus string `xml:"bus,attr"`
}

type domainInterface struct {
	Type   string                `xml:"type,attr"`
	Source domainInterfaceSource `xml:"source"`
	Model  domainInterfaceModel  `xml:"model"`
	MAC    *domainInterfaceMAC   `xml:"mac,omitempty"`
}

type domainInterfaceSource struct {
	Bridge  string `xml:"bridge,attr,omitempty"`
	Network string `xml:"network,attr,omitempty"`
}

type domainInterfaceModel struct {
	Type string `xml:"type,attr"`
}

type domainInterfaceMAC struct {
	Address string `xml:"address,attr"`
}

type domainSerial struct {
	Type   string             `xml:"type,attr"`
	Target domainSerialTarget `xml:"target"`
}

type domainSerialTarget struct {
	Port string `xml:"port,attr"`
}

type domainConsole struct {
	Type   string              `xml:"type,attr"`
	Target domainConsoleTarget `xml:"target"`
}

type domainConsoleTarget struct {
	Type string `xml:"type,attr"`
	Port string `xml:"port,attr"`
}

type domainChannel struct {
	Type   string              `xml:"type,attr"`
	Target domainChannelTarget `xml:"target"`
}

type domainChannelTarget struct {
	Type string `xml:"type,attr"`
	Name string `xml:"name,attr"`
}

// GenerateDomainXML produces a valid libvirt domain XML definition from the given parameters.
func GenerateDomainXML(params DomainXMLParams) (string, error) {
	d := domainXML{
		Type: "kvm",
		Name: params.Name,
		UUID: params.UUID,
		Memory: domainMemory{
			Unit:  "KiB",
			Value: params.MemoryKB,
		},
		VCPU: domainVCPU{
			Placement: "static",
			Value:     params.VCPUs,
		},
		OS: buildOS(params),
		Features: domainFeatures{
			ACPI: &struct{}{},
			APIC: &struct{}{},
		},
		CPU: domainCPU{
			Mode: "host-passthrough",
		},
		Devices: buildDevices(params),
	}

	output, err := xml.MarshalIndent(d, "", "  ")
	if err != nil {
		return "", err
	}
	return xml.Header + string(output), nil
}

func buildOS(params DomainXMLParams) domainOS {
	osType := domainOSType{
		Arch:    "x86_64",
		Machine: params.Machine,
		Value:   "hvm",
	}

	os := domainOS{
		Type: osType,
		Boot: domainBoot{Dev: "hd"},
	}

	if params.Firmware == "uefi" {
		os.Firmware = "efi"
		os.Loader = &domainLoader{
			ReadOnly: "yes",
			Type:     "pflash",
			Value:    params.FirmwarePath,
		}
		if params.NVRAMPath != "" {
			os.NVRAM = &domainNVRAM{Value: params.NVRAMPath}
		}
	}

	return os
}

type domainBoot struct {
	Dev string `xml:"dev,attr"`
}

func buildDevices(params DomainXMLParams) domainDevices {
	devices := domainDevices{}

	// Root disk.
	diskDevLetterIdx := 0
	rootDev := virtioDevName(diskDevLetterIdx, params.RootDiskBus)
	devices.Disks = append(devices.Disks, domainDisk{
		Type:   "file",
		Device: "disk",
		Driver: domainDiskDriver{Name: "qemu", Type: "qcow2"},
		Source: domainDiskSource{File: params.RootDiskPath},
		Target: domainDiskTarget{Dev: rootDev, Bus: params.RootDiskBus},
	})
	diskDevLetterIdx++

	// Bootstrap ISO (cdrom).
	if params.BootstrapISOPath != "" {
		devices.Disks = append(devices.Disks, domainDisk{
			Type:     "file",
			Device:   "cdrom",
			Driver:   domainDiskDriver{Name: "qemu", Type: "raw"},
			Source:   domainDiskSource{File: params.BootstrapISOPath},
			Target:   domainDiskTarget{Dev: "sda", Bus: "sata"},
			ReadOnly: &struct{}{},
		})
	}

	// Additional disks.
	for _, disk := range params.AdditionalDisks {
		bus := disk.Bus
		if bus == "" {
			bus = "virtio"
		}
		dev := virtioDevName(diskDevLetterIdx, bus)
		devices.Disks = append(devices.Disks, domainDisk{
			Type:   "file",
			Device: "disk",
			Driver: domainDiskDriver{Name: "qemu", Type: "qcow2"},
			Source: domainDiskSource{File: disk.Path},
			Target: domainDiskTarget{Dev: dev, Bus: bus},
		})
		diskDevLetterIdx++
	}

	// Network interface.
	iface := domainInterface{
		Type:  params.NetworkType,
		Model: domainInterfaceModel{Type: params.NetworkModel},
	}
	if params.NetworkType == "bridge" {
		iface.Source = domainInterfaceSource{Bridge: params.NetworkName}
	} else {
		iface.Source = domainInterfaceSource{Network: params.NetworkName}
	}
	if params.MACAddress != "" {
		iface.MAC = &domainInterfaceMAC{Address: params.MACAddress}
	}
	devices.Interfaces = append(devices.Interfaces, iface)

	// Serial console.
	devices.Serials = append(devices.Serials, domainSerial{
		Type:   "pty",
		Target: domainSerialTarget{Port: "0"},
	})

	// Console.
	devices.Consoles = append(devices.Consoles, domainConsole{
		Type:   "pty",
		Target: domainConsoleTarget{Type: "serial", Port: "0"},
	})

	// QEMU guest agent channel.
	devices.Channels = append(devices.Channels, domainChannel{
		Type:   "unix",
		Target: domainChannelTarget{Type: "virtio", Name: "org.qemu.guest_agent.0"},
	})

	return devices
}

func virtioDevName(idx int, bus string) string {
	letter := string(rune('a' + idx))
	switch bus {
	case "virtio":
		return "vd" + letter
	case "sata", "scsi":
		return "sd" + letter
	default:
		return "vd" + letter
	}
}
