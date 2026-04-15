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

package v1alpha1

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	infrav1 "github.com/atgreen/caplv/api/v1alpha1"
)

func validMachineSpec() infrav1.LibvirtMachineSpec {
	return infrav1.LibvirtMachineSpec{
		HostRef: corev1.LocalObjectReference{Name: "host-01"},
		Domain: infrav1.DomainSpec{
			VCPUs:    4,
			MemoryMB: 8192,
		},
		RootDisk: infrav1.RootDiskSpec{
			Size:        resource.MustParse("100Gi"),
			StoragePool: "default",
			BaseImage:   "rhcos-4.14.qcow2",
		},
		Network: infrav1.NetworkSpec{
			Type:      infrav1.NetworkTypeBridge,
			Name:      "br0",
			Addresses: []string{"192.168.1.50/24"},
		},
		BootstrapFormat: infrav1.BootstrapFormatIgnition,
	}
}

var _ = Describe("LibvirtMachine Webhook", func() {
	var validator LibvirtMachineCustomValidator
	ctx := context.Background()

	BeforeEach(func() {
		validator = LibvirtMachineCustomValidator{}
	})

	Context("ValidateCreate", func() {
		It("should accept a valid machine with addresses", func() {
			obj := &infrav1.LibvirtMachine{Spec: validMachineSpec()}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should reject a machine with no addresses", func() {
			spec := validMachineSpec()
			spec.Network.Addresses = nil
			obj := &infrav1.LibvirtMachine{Spec: spec}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least one address"))
		})

		It("should reject a machine with invalid CIDR", func() {
			spec := validMachineSpec()
			spec.Network.Addresses = []string{"not-a-cidr"}
			obj := &infrav1.LibvirtMachine{Spec: spec}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid CIDR"))
		})

		It("should reject an address without prefix length", func() {
			spec := validMachineSpec()
			spec.Network.Addresses = []string{"192.168.1.50"}
			obj := &infrav1.LibvirtMachine{Spec: spec}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
		})

		It("should accept multiple valid addresses", func() {
			spec := validMachineSpec()
			spec.Network.Addresses = []string{"192.168.1.50/24", "10.0.0.5/16"}
			obj := &infrav1.LibvirtMachine{Spec: spec}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("ValidateUpdate", func() {
		It("should reject any spec change", func() {
			oldObj := &infrav1.LibvirtMachine{Spec: validMachineSpec()}
			newSpec := validMachineSpec()
			newSpec.Domain.VCPUs = 8
			newObj := &infrav1.LibvirtMachine{Spec: newSpec}
			_, err := validator.ValidateUpdate(ctx, oldObj, newObj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("immutable"))
		})

		It("should allow providerID to be set from nil", func() {
			oldObj := &infrav1.LibvirtMachine{Spec: validMachineSpec()}
			newSpec := validMachineSpec()
			newSpec.ProviderID = ptr.To("libvirt:///host/domain")
			newObj := &infrav1.LibvirtMachine{Spec: newSpec}
			_, err := validator.ValidateUpdate(ctx, oldObj, newObj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should reject providerID change once set", func() {
			oldSpec := validMachineSpec()
			oldSpec.ProviderID = ptr.To("libvirt:///host/domain-old")
			oldObj := &infrav1.LibvirtMachine{Spec: oldSpec}
			newSpec := validMachineSpec()
			newSpec.ProviderID = ptr.To("libvirt:///host/domain-new")
			newObj := &infrav1.LibvirtMachine{Spec: newSpec}
			_, err := validator.ValidateUpdate(ctx, oldObj, newObj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("cannot be changed"))
		})

		It("should reject clearing providerID once set", func() {
			oldSpec := validMachineSpec()
			oldSpec.ProviderID = ptr.To("libvirt:///host/domain")
			oldObj := &infrav1.LibvirtMachine{Spec: oldSpec}
			newObj := &infrav1.LibvirtMachine{Spec: validMachineSpec()} // providerID is nil
			_, err := validator.ValidateUpdate(ctx, oldObj, newObj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("cannot be cleared"))
		})

		It("should allow no-op update with same providerID", func() {
			spec := validMachineSpec()
			spec.ProviderID = ptr.To("libvirt:///host/domain")
			oldObj := &infrav1.LibvirtMachine{Spec: spec}
			newObj := &infrav1.LibvirtMachine{Spec: *spec.DeepCopy()}
			_, err := validator.ValidateUpdate(ctx, oldObj, newObj)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("ValidateDelete", func() {
		It("should always succeed", func() {
			obj := &infrav1.LibvirtMachine{Spec: validMachineSpec()}
			_, err := validator.ValidateDelete(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
