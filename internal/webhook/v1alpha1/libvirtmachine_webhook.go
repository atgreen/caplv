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
	"fmt"
	"net"
	"reflect"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	infrastructurev1alpha1 "github.com/atgreen/caplv/api/v1alpha1"
)

// nolint:unused
// log is for logging in this package.
var libvirtmachinelog = logf.Log.WithName("libvirtmachine-resource")

// SetupLibvirtMachineWebhookWithManager registers the webhook for LibvirtMachine in the manager.
func SetupLibvirtMachineWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &infrastructurev1alpha1.LibvirtMachine{}).
		WithValidator(&LibvirtMachineCustomValidator{Client: mgr.GetClient()}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-infrastructure-cluster-x-k8s-io-v1alpha1-libvirtmachine,mutating=false,failurePolicy=fail,sideEffects=None,groups=infrastructure.cluster.x-k8s.io,resources=libvirtmachines,verbs=create;update,versions=v1alpha1,name=vlibvirtmachine-v1alpha1.kb.io,admissionReviewVersions=v1

// LibvirtMachineCustomValidator struct is responsible for validating the LibvirtMachine resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type LibvirtMachineCustomValidator struct {
	Client client.Reader
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type LibvirtMachine.
func (v *LibvirtMachineCustomValidator) ValidateCreate(ctx context.Context, obj *infrastructurev1alpha1.LibvirtMachine) (admission.Warnings, error) {
	libvirtmachinelog.Info("Validation for LibvirtMachine upon creation", "name", obj.GetName())

	var allErrs field.ErrorList

	// Validate addresses.
	addressesPath := field.NewPath("spec", "network", "addresses")
	if len(obj.Spec.Network.Addresses) == 0 {
		allErrs = append(allErrs, field.Required(addressesPath, "at least one address is required"))
	}
	for i, addr := range obj.Spec.Network.Addresses {
		if _, _, err := net.ParseCIDR(addr); err != nil {
			allErrs = append(allErrs, field.Invalid(
				addressesPath.Index(i),
				addr,
				fmt.Sprintf("invalid CIDR notation: %v", err),
			))
		}
	}

	// Reject full-clone when baseImagePool differs from storagePool.
	// virsh vol-clone cannot clone across pools.
	if obj.Spec.RootDisk.CloneStrategy == infrastructurev1alpha1.CloneStrategyFullClone {
		baseImagePool := obj.Spec.RootDisk.BaseImagePool
		if baseImagePool == "" {
			baseImagePool = obj.Spec.RootDisk.StoragePool
		}
		if baseImagePool != obj.Spec.RootDisk.StoragePool {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "rootDisk", "cloneStrategy"),
				string(obj.Spec.RootDisk.CloneStrategy),
				"full-clone is not supported when baseImagePool differs from storagePool; use copy-on-write instead",
			))
		}
	}

	// Enforce one machine per host.
	if v.Client != nil {
		if err := v.validateHostUniqueness(ctx, obj); err != nil {
			allErrs = append(allErrs, err)
		}
	}

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: infrastructurev1alpha1.GroupVersion.Group, Kind: "LibvirtMachine"},
			obj.Name,
			allErrs,
		)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type LibvirtMachine.
func (v *LibvirtMachineCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj *infrastructurev1alpha1.LibvirtMachine) (admission.Warnings, error) {
	libvirtmachinelog.Info("Validation for LibvirtMachine upon update", "name", newObj.GetName())

	var allErrs field.ErrorList

	// ProviderID transitions: only nil->value is allowed. Once set, it cannot
	// be changed or cleared.
	if oldObj.Spec.ProviderID != nil {
		if newObj.Spec.ProviderID == nil {
			allErrs = append(allErrs, field.Forbidden(
				field.NewPath("spec", "providerID"),
				"providerID cannot be cleared once set",
			))
		} else if *oldObj.Spec.ProviderID != *newObj.Spec.ProviderID {
			allErrs = append(allErrs, field.Forbidden(
				field.NewPath("spec", "providerID"),
				"providerID cannot be changed once set",
			))
		}
	}

	// Compare specs excluding providerID.
	oldSpec := oldObj.Spec.DeepCopy()
	newSpec := newObj.Spec.DeepCopy()
	oldSpec.ProviderID = nil
	newSpec.ProviderID = nil

	if !reflect.DeepEqual(oldSpec, newSpec) {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec"),
			"spec is immutable after creation (except providerID)",
		))
	}

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: infrastructurev1alpha1.GroupVersion.Group, Kind: "LibvirtMachine"},
			newObj.Name,
			allErrs,
		)
	}

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type LibvirtMachine.
func (v *LibvirtMachineCustomValidator) ValidateDelete(_ context.Context, _ *infrastructurev1alpha1.LibvirtMachine) (admission.Warnings, error) {
	return nil, nil
}

// validateHostUniqueness rejects creation if another LibvirtMachine in the
// same namespace already references the same host. Each host runs at most
// one CAPLV-managed VM.
func (v *LibvirtMachineCustomValidator) validateHostUniqueness(ctx context.Context, obj *infrastructurev1alpha1.LibvirtMachine) *field.Error {
	machineList := &infrastructurev1alpha1.LibvirtMachineList{}
	if err := v.Client.List(ctx, machineList, client.InNamespace(obj.Namespace)); err != nil {
		return field.InternalError(
			field.NewPath("spec", "hostRef"),
			fmt.Errorf("failed to list machines: %w", err),
		)
	}
	for i := range machineList.Items {
		existing := &machineList.Items[i]
		// Skip self (in case of re-validation).
		if existing.Name == obj.Name && existing.Namespace == obj.Namespace {
			continue
		}
		// Skip machines that are being deleted.
		if !existing.DeletionTimestamp.IsZero() {
			continue
		}
		if existing.Spec.HostRef.Name == obj.Spec.HostRef.Name {
			return field.Forbidden(
				field.NewPath("spec", "hostRef"),
				fmt.Sprintf("host %q is already in use by LibvirtMachine %q; each host supports at most one VM",
					obj.Spec.HostRef.Name, existing.Name),
			)
		}
	}
	return nil
}
