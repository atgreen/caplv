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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SecretReference is a reference to a Kubernetes Secret.
type SecretReference struct {
	// name is the name of the Secret.
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// namespace is the namespace of the Secret.
	// If not specified, the namespace of the referencing resource is used.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// LibvirtHostSpec defines the desired state of LibvirtHost.
type LibvirtHostSpec struct {
	// uri is the libvirt connection URI (e.g. qemu+ssh://user@host/system).
	// +required
	// +kubebuilder:validation:MinLength=1
	URI string `json:"uri"`

	// secretRef is a reference to a Secret containing the SSH private key
	// for authenticating to the libvirt host.
	// +optional
	SecretRef *SecretReference `json:"secretRef,omitempty"`

	// hostKeyFingerprint is the SSH host key fingerprint (SHA256:...) used to
	// verify the identity of the remote host.
	// +optional
	HostKeyFingerprint string `json:"hostKeyFingerprint,omitempty"`

	// firmwarePath overrides the OVMF firmware path on the host.
	// +optional
	// +kubebuilder:default="/usr/share/OVMF/OVMF_CODE.secboot.fd"
	FirmwarePath string `json:"firmwarePath,omitempty"`

	// nvramTemplatePath overrides the NVRAM template path on the host.
	// +optional
	// +kubebuilder:default="/usr/share/OVMF/OVMF_VARS.fd"
	NVRAMTemplatePath string `json:"nvramTemplatePath,omitempty"`
}

// LibvirtHostStatus defines the observed state of LibvirtHost.
type LibvirtHostStatus struct {
	// ready indicates whether the host is reachable and usable.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// lastChecked is the timestamp of the last connectivity check.
	// +optional
	LastChecked *metav1.Time `json:"lastChecked,omitempty"`

	// conditions represent the current state of the LibvirtHost resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="URI",type="string",JSONPath=".spec.uri"
// +kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// LibvirtHost is the Schema for the libvirthosts API.
// It represents a reusable libvirt host connection configuration.
type LibvirtHost struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of LibvirtHost.
	// +required
	Spec LibvirtHostSpec `json:"spec"`

	// status defines the observed state of LibvirtHost.
	// +optional
	Status LibvirtHostStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// LibvirtHostList contains a list of LibvirtHost.
type LibvirtHostList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []LibvirtHost `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LibvirtHost{}, &LibvirtHostList{})
}
