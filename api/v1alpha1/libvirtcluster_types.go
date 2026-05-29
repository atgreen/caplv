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
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// LibvirtClusterSpec defines the desired state of LibvirtCluster.
type LibvirtClusterSpec struct {
	// controlPlaneEndpoint represents the endpoint used to communicate with the control plane.
	// +required
	ControlPlaneEndpoint clusterv1.APIEndpoint `json:"controlPlaneEndpoint"`

	// bootArtifacts, when set, switches first-boot ignition delivery from
	// QEMU fw_cfg to libvirt direct-kernel-boot plus a virtio-blk ignition
	// disk. The kernel's qemu_fw_cfg driver does O(n²) offset reads, which
	// burns seconds of wall-clock time for multi-MB ignition payloads.
	// +optional
	BootArtifacts *BootArtifactsSpec `json:"bootArtifacts,omitempty"`
}

// BootArtifactsSpec configures direct-kernel-boot of first-boot artifacts.
type BootArtifactsSpec struct {
	// HostPath is the directory on each libvirt host where kernel/initramfs are cached.
	// Files land at <HostPath>/<sha256>/vmlinuz and <HostPath>/<sha256>/initramfs.img.
	// +kubebuilder:validation:Required
	HostPath string `json:"hostPath"`

	// KernelArgs is the cmdline appended to the direct-boot kernel. The user is
	// responsible for setting an ignition.config.url that reads from the virtio-blk
	// ignition disk (serial=ignition), e.g.
	//   "ignition.platform.id=metal ignition.config.url=oem:/dev/disk/by-id/virtio-ignition console=ttyS0"
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	KernelArgs string `json:"kernelArgs"`

	// Source describes where to fetch the kernel and initramfs from.
	// +kubebuilder:validation:Required
	Source BootArtifactsSource `json:"source"`
}

// BootArtifactsSourceType enumerates supported artifact transports.
type BootArtifactsSourceType string

const (
	BootArtifactsSourceHTTPS BootArtifactsSourceType = "HTTPS"
	BootArtifactsSourceOCI   BootArtifactsSourceType = "OCI"
	BootArtifactsSourceS3    BootArtifactsSourceType = "S3"
)

// BootArtifactsSource selects one transport-specific source.
type BootArtifactsSource struct {
	// +kubebuilder:validation:Enum=HTTPS;OCI;S3
	// +kubebuilder:validation:Required
	Type BootArtifactsSourceType `json:"type"`

	// +optional
	HTTPS *HTTPSBootArtifactsSource `json:"https,omitempty"`
	// +optional
	OCI *OCIBootArtifactsSource `json:"oci,omitempty"`
	// +optional
	S3 *S3BootArtifactsSource `json:"s3,omitempty"`
}

// HTTPSBootArtifactsSource fetches kernel+initramfs over HTTPS.
type HTTPSBootArtifactsSource struct {
	// +kubebuilder:validation:Required
	KernelURL string `json:"kernelURL"`
	// +kubebuilder:validation:Required
	InitramfsURL string `json:"initramfsURL"`
	// +optional
	KernelSHA256 string `json:"kernelSHA256,omitempty"`
	// +optional
	InitramfsSHA256 string `json:"initramfsSHA256,omitempty"`
}

// OCIBootArtifactsSource fetches kernel+initramfs from an OCI artifact.
type OCIBootArtifactsSource struct {
	// +kubebuilder:validation:Required
	Reference string `json:"reference"`
}

// S3BootArtifactsSource fetches kernel+initramfs from an S3-compatible store.
type S3BootArtifactsSource struct {
	// +kubebuilder:validation:Required
	Endpoint string `json:"endpoint"`
	// +kubebuilder:validation:Required
	Bucket string `json:"bucket"`
	// +kubebuilder:validation:Required
	KernelKey string `json:"kernelKey"`
	// +kubebuilder:validation:Required
	InitramfsKey string `json:"initramfsKey"`
	// +optional
	KernelSHA256 string `json:"kernelSHA256,omitempty"`
	// +optional
	InitramfsSHA256 string `json:"initramfsSHA256,omitempty"`
}

// LibvirtClusterStatus defines the observed state of LibvirtCluster.
type LibvirtClusterStatus struct {
	// ready indicates whether the cluster infrastructure is ready.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// conditions represent the current state of the LibvirtCluster resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="Endpoint",type="string",JSONPath=".spec.controlPlaneEndpoint.host"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// LibvirtCluster is the Schema for the libvirtclusters API.
// It fulfills the Cluster API infrastructure cluster contract.
type LibvirtCluster struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of LibvirtCluster.
	// +required
	Spec LibvirtClusterSpec `json:"spec"`

	// status defines the observed state of LibvirtCluster.
	// +optional
	Status LibvirtClusterStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// LibvirtClusterList contains a list of LibvirtCluster.
type LibvirtClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []LibvirtCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LibvirtCluster{}, &LibvirtClusterList{})
}
