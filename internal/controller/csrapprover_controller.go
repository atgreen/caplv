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

package controller

import (
	"context"
	"fmt"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/atgreen/caplv/api/v1alpha1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// CSRApproverReconciler watches for pending CertificateSigningRequests and
// auto-approves those that match a CAPI Machine backed by a LibvirtMachine.
type CSRApproverReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=certificates.k8s.io,resources=certificatesigningrequests,verbs=get;list;watch
// +kubebuilder:rbac:groups=certificates.k8s.io,resources=certificatesigningrequests/approval,verbs=update
// +kubebuilder:rbac:groups=certificates.k8s.io,resources=signers,resourceNames=kubernetes.io/kube-apiserver-client-kubelet;kubernetes.io/kubelet-serving,verbs=approve
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtmachines,verbs=get;list;watch

func (r *CSRApproverReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("csr", req.Name)

	csr := &certificatesv1.CertificateSigningRequest{}
	if err := r.Get(ctx, req.NamespacedName, csr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Skip if already approved or denied.
	if isApprovedOrDenied(csr) {
		return ctrl.Result{}, nil
	}

	// Only handle kubelet client and serving CSRs.
	if csr.Spec.SignerName != "kubernetes.io/kube-apiserver-client-kubelet" &&
		csr.Spec.SignerName != "kubernetes.io/kubelet-serving" {
		return ctrl.Result{}, nil
	}

	// Extract the node name from the CSR.
	nodeName := nodeNameFromCSR(csr)
	if nodeName == "" {
		return ctrl.Result{}, nil
	}

	// Check if this node name matches a CAPI Machine backed by a LibvirtMachine.
	if !r.isLibvirtManagedNode(ctx, nodeName) {
		return ctrl.Result{}, nil
	}

	// Approve the CSR.
	log.Info("Auto-approving CSR for CAPLV-managed node", "node", nodeName, "signer", csr.Spec.SignerName)
	csr.Status.Conditions = append(csr.Status.Conditions, certificatesv1.CertificateSigningRequestCondition{
		Type:               certificatesv1.CertificateApproved,
		Status:             corev1.ConditionTrue,
		Reason:             "CAPLVAutoApproved",
		Message:            fmt.Sprintf("CSR auto-approved by CAPLV for managed node %s", nodeName),
		LastUpdateTime:     metav1.Now(),
	})

	if err := r.Client.SubResource("approval").Update(ctx, csr); err != nil {
		log.Error(err, "Failed to approve CSR")
		return ctrl.Result{}, err
	}

	log.Info("CSR approved", "node", nodeName)
	return ctrl.Result{}, nil
}

// isLibvirtManagedNode checks whether a node name corresponds to a CAPI Machine
// that is backed by a LibvirtMachine.
func (r *CSRApproverReconciler) isLibvirtManagedNode(ctx context.Context, nodeName string) bool {
	// List all Machines across all namespaces.
	machineList := &clusterv1.MachineList{}
	if err := r.List(ctx, machineList); err != nil {
		return false
	}

	for i := range machineList.Items {
		machine := &machineList.Items[i]

		// Check if the infrastructure ref points to a LibvirtMachine.
		if machine.Spec.InfrastructureRef.GroupVersionKind().Kind != "LibvirtMachine" {
			continue
		}

		// Look up the LibvirtMachine to get the domain name.
		lm := &infrav1.LibvirtMachine{}
		lmKey := client.ObjectKey{
			Namespace: machine.Spec.InfrastructureRef.Namespace,
			Name:      machine.Spec.InfrastructureRef.Name,
		}
		if err := r.Get(ctx, lmKey, lm); err != nil {
			continue
		}

		// The domain name is <namespace>-<cluster>-<machine> which is
		// also the hostname we inject into ignition.
		domainName := fmt.Sprintf("%s-%s-%s", lm.Namespace, machine.Labels["cluster.x-k8s.io/cluster-name"], lm.Name)
		if domainName == nodeName {
			return true
		}
	}

	return false
}

// nodeNameFromCSR extracts the node name from a CSR's username or common name.
// Kubelet client CSRs use username "system:node:<nodename>".
// Kubelet serving CSRs use username "system:node:<nodename>".
func nodeNameFromCSR(csr *certificatesv1.CertificateSigningRequest) string {
	const prefix = "system:node:"
	if len(csr.Spec.Username) > len(prefix) && csr.Spec.Username[:len(prefix)] == prefix {
		return csr.Spec.Username[len(prefix):]
	}
	return ""
}

func isApprovedOrDenied(csr *certificatesv1.CertificateSigningRequest) bool {
	for _, c := range csr.Status.Conditions {
		if c.Type == certificatesv1.CertificateApproved || c.Type == certificatesv1.CertificateDenied {
			return true
		}
	}
	return false
}

func (r *CSRApproverReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&certificatesv1.CertificateSigningRequest{}).
		Named("csrapprover").
		Complete(r)
}
