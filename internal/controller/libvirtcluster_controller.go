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
	"net"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/atgreen/caplv/api/v1alpha1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/patch"
)

const (
	clusterRequeueInterval       = 60 * time.Second
	controlPlaneDialTimeout      = 5 * time.Second
)

// LibvirtClusterReconciler reconciles a LibvirtCluster object.
type LibvirtClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters;clusters/status,verbs=get;list;watch

// Reconcile checks that the control plane endpoint is reachable and marks
// the LibvirtCluster as ready.
func (r *LibvirtClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("cluster", req.Name, "namespace", req.Namespace)
	log.Info("Reconciling LibvirtCluster")
	ctx = logf.IntoContext(ctx, log)

	// Fetch the LibvirtCluster resource.
	libvirtCluster := &infrav1.LibvirtCluster{}
	if err := r.Get(ctx, req.NamespacedName, libvirtCluster); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Create patch helper for deferred status patch.
	patchHelper, err := patch.NewHelper(libvirtCluster, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer func() {
		if patchErr := patchHelper.Patch(ctx, libvirtCluster); patchErr != nil {
			log.Error(patchErr, "Failed to patch LibvirtCluster")
		}
	}()

	// Fetch the owner Cluster.
	cluster, err := util.GetOwnerCluster(ctx, r.Client, libvirtCluster.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if cluster == nil {
		log.Info("Waiting for Cluster controller to set OwnerRef on LibvirtCluster")
		return ctrl.Result{}, nil
	}

	// If cluster is paused, return without requeueing.
	if annotations.IsPaused(cluster, libvirtCluster) {
		log.Info("LibvirtCluster or owning Cluster is paused, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// Check control plane endpoint reachability via TCP dial.
	endpoint := libvirtCluster.Spec.ControlPlaneEndpoint
	addr := fmt.Sprintf("%s:%d", endpoint.Host, endpoint.Port)

	conn, err := net.DialTimeout("tcp", addr, controlPlaneDialTimeout)
	if err != nil {
		log.Info("Control plane endpoint not reachable", "address", addr, "error", err)
		libvirtCluster.Status.Ready = false
		apimeta.SetStatusCondition(&libvirtCluster.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "ControlPlaneUnreachable",
			Message:            fmt.Sprintf("TCP dial to %s failed: %v", addr, err),
			ObservedGeneration: libvirtCluster.Generation,
		})
		return ctrl.Result{RequeueAfter: clusterRequeueInterval}, nil
	}
	conn.Close()
	log.Info("Control plane endpoint reachable", "address", addr)

	// Control plane is reachable.
	libvirtCluster.Status.Ready = true
	apimeta.SetStatusCondition(&libvirtCluster.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             infrav1.ReasonClusterReady,
		Message:            fmt.Sprintf("Control plane endpoint %s is reachable", addr),
		ObservedGeneration: libvirtCluster.Generation,
	})

	return ctrl.Result{RequeueAfter: clusterRequeueInterval}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *LibvirtClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.LibvirtCluster{}).
		Named("libvirtcluster").
		Complete(r)
}
