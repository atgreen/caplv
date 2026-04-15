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
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	gossh "golang.org/x/crypto/ssh"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/atgreen/caplv/api/v1alpha1"
	"github.com/atgreen/caplv/internal/libvirt"
	"sigs.k8s.io/cluster-api/util/patch"
)

const hostRequeueInterval = 60 * time.Second

// SSHClientFactory is a function that creates an SSH client from a LibvirtHost and Secret.
type SSHClientFactory func(ctx context.Context, host *infrav1.LibvirtHost, secret *corev1.Secret) (*gossh.Client, error)

// LibvirtClientFactory is a function that creates a libvirt Client from an SSH client.
type LibvirtClientFactory func(sshClient *gossh.Client) libvirt.Client

// LibvirtHostReconciler reconciles a LibvirtHost object.
type LibvirtHostReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	SSHClientFactory     SSHClientFactory
	LibvirtClientFactory LibvirtClientFactory
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirthosts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirthosts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirthosts/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile performs a connectivity check against the libvirt host and updates status.
func (r *LibvirtHostReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the LibvirtHost resource.
	host := &infrav1.LibvirtHost{}
	if err := r.Get(ctx, req.NamespacedName, host); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Create patch helper for deferred status patch.
	patchHelper, err := patch.NewHelper(host, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer func() {
		if patchErr := patchHelper.Patch(ctx, host); patchErr != nil {
			log.Error(patchErr, "Failed to patch LibvirtHost")
		}
	}()

	// Always update LastChecked.
	now := metav1.Now()
	host.Status.LastChecked = &now

	// secretRef is required — a host without credentials cannot be verified.
	if host.Spec.SecretRef == nil {
		host.Status.Ready = false
		apimeta.SetStatusCondition(&host.Status.Conditions, metav1.Condition{
			Type:               infrav1.HostReachableCondition,
			Status:             metav1.ConditionFalse,
			Reason:             infrav1.ReasonConnectionFailed,
			Message:            "spec.secretRef is required for SSH connectivity",
			ObservedGeneration: host.Generation,
		})
		return ctrl.Result{RequeueAfter: hostRequeueInterval}, nil
	}

	// Fetch the SSH secret.
	secretNS := host.Spec.SecretRef.Namespace
	if secretNS == "" {
		secretNS = host.Namespace
	}
	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{Namespace: secretNS, Name: host.Spec.SecretRef.Name}
	if err := r.Get(ctx, secretKey, secret); err != nil {
		log.Error(err, "Failed to get SSH secret", "secret", secretKey)
		host.Status.Ready = false
		apimeta.SetStatusCondition(&host.Status.Conditions, metav1.Condition{
			Type:               infrav1.HostReachableCondition,
			Status:             metav1.ConditionFalse,
			Reason:             infrav1.ReasonConnectionFailed,
			Message:            "Failed to get SSH secret: " + err.Error(),
			ObservedGeneration: host.Generation,
		})
		return ctrl.Result{RequeueAfter: hostRequeueInterval}, nil
	}

	// Create SSH client.
	sshClient, err := r.SSHClientFactory(ctx, host, secret)
	if err != nil {
		log.Error(err, "Failed to create SSH client")
		host.Status.Ready = false
		apimeta.SetStatusCondition(&host.Status.Conditions, metav1.Condition{
			Type:               infrav1.HostReachableCondition,
			Status:             metav1.ConditionFalse,
			Reason:             infrav1.ReasonConnectionFailed,
			Message:            "SSH connection failed: " + err.Error(),
			ObservedGeneration: host.Generation,
		})
		return ctrl.Result{RequeueAfter: hostRequeueInterval}, nil
	}
	if sshClient != nil {
		defer sshClient.Close()
	}

	// Verify libvirt is usable by running virsh version over SSH.
	libvirtClient := r.LibvirtClientFactory(sshClient)
	defer libvirtClient.Close()

	if err := libvirtClient.Ping(ctx); err != nil {
		log.Error(err, "Libvirt connectivity check failed")
		host.Status.Ready = false
		apimeta.SetStatusCondition(&host.Status.Conditions, metav1.Condition{
			Type:               infrav1.HostReachableCondition,
			Status:             metav1.ConditionFalse,
			Reason:             infrav1.ReasonConnectionFailed,
			Message:            "Libvirt connectivity check failed: " + err.Error(),
			ObservedGeneration: host.Generation,
		})
		return ctrl.Result{RequeueAfter: hostRequeueInterval}, nil
	}

	// SSH + libvirt both verified.
	host.Status.Ready = true
	apimeta.SetStatusCondition(&host.Status.Conditions, metav1.Condition{
		Type:               infrav1.HostReachableCondition,
		Status:             metav1.ConditionTrue,
		Reason:             infrav1.ReasonConnectionSucceeded,
		Message:            "SSH and libvirt connectivity verified",
		ObservedGeneration: host.Generation,
	})

	return ctrl.Result{RequeueAfter: hostRequeueInterval}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *LibvirtHostReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.LibvirtHost{}).
		Named("libvirthost").
		Complete(r)
}
