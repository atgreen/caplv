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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/atgreen/caplv/api/v1alpha1"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = infrav1.AddToScheme(s)
	return s
}

func testMachineSpec() infrav1.LibvirtMachineSpec {
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

// --- ValidateCreate: address validation ---

func TestValidateCreate_ValidMachine(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	obj := &infrav1.LibvirtMachine{Spec: testMachineSpec()}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateCreate_NoAddresses(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.Network.Addresses = nil
	obj := &infrav1.LibvirtMachine{Spec: spec}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err == nil {
		t.Error("expected error for missing addresses")
	}
}

func TestValidateCreate_InvalidCIDR(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.Network.Addresses = []string{"not-a-cidr"}
	obj := &infrav1.LibvirtMachine{Spec: spec}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err == nil {
		t.Error("expected error for invalid CIDR")
	}
}

func TestValidateCreate_NoPrefixLength(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.Network.Addresses = []string{"192.168.1.50"}
	obj := &infrav1.LibvirtMachine{Spec: spec}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err == nil {
		t.Error("expected error for address without prefix length")
	}
}

func TestValidateCreate_MultipleAddresses(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.Network.Addresses = []string{"192.168.1.50/24", "10.0.0.5/16"}
	obj := &infrav1.LibvirtMachine{Spec: spec}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// --- ValidateCreate: full-clone cross-pool rejection ---

func TestValidateCreate_FullCloneSamePool(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.RootDisk.CloneStrategy = infrav1.CloneStrategyFullClone
	// baseImagePool unset, defaults to storagePool — same pool, allowed.
	obj := &infrav1.LibvirtMachine{Spec: spec}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err != nil {
		t.Errorf("expected no error for full-clone same pool, got: %v", err)
	}
}

func TestValidateCreate_FullCloneCrossPool_Rejected(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.RootDisk.CloneStrategy = infrav1.CloneStrategyFullClone
	spec.RootDisk.StoragePool = "ephemeral"
	spec.RootDisk.BaseImagePool = "default"
	obj := &infrav1.LibvirtMachine{Spec: spec}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err == nil {
		t.Error("expected error for full-clone with cross-pool")
	}
}

func TestValidateCreate_CopyOnWriteCrossPool_Allowed(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.RootDisk.CloneStrategy = infrav1.CloneStrategyCopyOnWrite
	spec.RootDisk.StoragePool = "ephemeral"
	spec.RootDisk.BaseImagePool = "default"
	obj := &infrav1.LibvirtMachine{Spec: spec}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err != nil {
		t.Errorf("expected no error for copy-on-write cross-pool, got: %v", err)
	}
}

func TestValidateCreate_RejectsInjectedStoragePool(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.RootDisk.StoragePool = "default; printf INJECTED"
	obj := &infrav1.LibvirtMachine{Spec: spec}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err == nil {
		t.Error("expected error for storagePool with shell metacharacters")
	}
}

func TestValidateCreate_RejectsInjectedAdditionalDiskFields(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.AdditionalDisks = []infrav1.AdditionalDiskSpec{{
		Name:        "data; printf INJECTED",
		StoragePool: "default",
		Size:        resource.MustParse("10Gi"),
	}}
	obj := &infrav1.LibvirtMachine{Spec: spec}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err == nil {
		t.Error("expected error for additional disk name with shell metacharacters")
	}
}

// --- ValidateCreate: host uniqueness ---

func TestValidateCreate_HostAlreadyInUse_Rejected(t *testing.T) {
	s := testScheme()
	existing := &infrav1.LibvirtMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "existing-vm", Namespace: "default"},
		Spec:       testMachineSpec(),
	}
	k8sClient := fake.NewClientBuilder().WithScheme(s).WithObjects(existing).Build()

	v := &LibvirtMachineCustomValidator{Client: k8sClient}
	newObj := &infrav1.LibvirtMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "new-vm", Namespace: "default"},
		Spec:       testMachineSpec(), // same hostRef: host-01
	}
	_, err := v.ValidateCreate(context.Background(), newObj)
	if err == nil {
		t.Error("expected error: host already in use")
	}
}

func TestValidateCreate_DifferentHost_Allowed(t *testing.T) {
	s := testScheme()
	existing := &infrav1.LibvirtMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "existing-vm", Namespace: "default"},
		Spec:       testMachineSpec(), // hostRef: host-01
	}
	k8sClient := fake.NewClientBuilder().WithScheme(s).WithObjects(existing).Build()

	v := &LibvirtMachineCustomValidator{Client: k8sClient}
	spec := testMachineSpec()
	spec.HostRef.Name = "host-02" // different host
	newObj := &infrav1.LibvirtMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "new-vm", Namespace: "default"},
		Spec:       spec,
	}
	_, err := v.ValidateCreate(context.Background(), newObj)
	if err != nil {
		t.Errorf("expected no error for different host, got: %v", err)
	}
}

func TestValidateCreate_DeletingMachineDoesNotBlock(t *testing.T) {
	s := testScheme()
	now := metav1.Now()
	existing := &infrav1.LibvirtMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "deleting-vm",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{"test"}, // needed for DeletionTimestamp to be set
		},
		Spec: testMachineSpec(), // same host
	}
	k8sClient := fake.NewClientBuilder().WithScheme(s).WithObjects(existing).Build()

	v := &LibvirtMachineCustomValidator{Client: k8sClient}
	newObj := &infrav1.LibvirtMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "new-vm", Namespace: "default"},
		Spec:       testMachineSpec(),
	}
	_, err := v.ValidateCreate(context.Background(), newObj)
	if err != nil {
		t.Errorf("expected no error: deleting machine should not block, got: %v", err)
	}
}

func TestValidateCreate_NoClient_SkipsHostCheck(t *testing.T) {
	v := &LibvirtMachineCustomValidator{} // nil client
	obj := &infrav1.LibvirtMachine{Spec: testMachineSpec()}
	_, err := v.ValidateCreate(context.Background(), obj)
	if err != nil {
		t.Errorf("expected no error with nil client, got: %v", err)
	}
}

// --- ValidateUpdate ---

func TestValidateUpdate_RejectSpecChange(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	oldObj := &infrav1.LibvirtMachine{Spec: testMachineSpec()}
	newSpec := testMachineSpec()
	newSpec.Domain.VCPUs = 8
	newObj := &infrav1.LibvirtMachine{Spec: newSpec}
	_, err := v.ValidateUpdate(context.Background(), oldObj, newObj)
	if err == nil {
		t.Error("expected error for spec change")
	}
}

func TestValidateUpdate_AllowProviderIDSetFromNil(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	oldObj := &infrav1.LibvirtMachine{Spec: testMachineSpec()}
	newSpec := testMachineSpec()
	newSpec.ProviderID = ptr.To("libvirt:///host/domain")
	newObj := &infrav1.LibvirtMachine{Spec: newSpec}
	_, err := v.ValidateUpdate(context.Background(), oldObj, newObj)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateUpdate_RejectProviderIDChange(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	oldSpec := testMachineSpec()
	oldSpec.ProviderID = ptr.To("libvirt:///host/domain-old")
	oldObj := &infrav1.LibvirtMachine{Spec: oldSpec}
	newSpec := testMachineSpec()
	newSpec.ProviderID = ptr.To("libvirt:///host/domain-new")
	newObj := &infrav1.LibvirtMachine{Spec: newSpec}
	_, err := v.ValidateUpdate(context.Background(), oldObj, newObj)
	if err == nil {
		t.Error("expected error for providerID change")
	}
}

func TestValidateUpdate_RejectProviderIDClearing(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	oldSpec := testMachineSpec()
	oldSpec.ProviderID = ptr.To("libvirt:///host/domain")
	oldObj := &infrav1.LibvirtMachine{Spec: oldSpec}
	newObj := &infrav1.LibvirtMachine{Spec: testMachineSpec()}
	_, err := v.ValidateUpdate(context.Background(), oldObj, newObj)
	if err == nil {
		t.Error("expected error for clearing providerID")
	}
}

func TestValidateUpdate_AllowNoOpWithProviderID(t *testing.T) {
	v := &LibvirtMachineCustomValidator{}
	spec := testMachineSpec()
	spec.ProviderID = ptr.To("libvirt:///host/domain")
	oldObj := &infrav1.LibvirtMachine{Spec: spec}
	newObj := &infrav1.LibvirtMachine{Spec: *spec.DeepCopy()}
	_, err := v.ValidateUpdate(context.Background(), oldObj, newObj)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}
