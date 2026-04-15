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
	"time"

	gossh "golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/atgreen/caplv/api/v1alpha1"
	"github.com/atgreen/caplv/internal/iso"
	"github.com/atgreen/caplv/internal/libvirt"
	"github.com/atgreen/caplv/internal/scope"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/patch"
)

const (
	hostNotReadyRequeueInterval      = 30 * time.Second
	bootstrapNotReadyRequeueInterval = 10 * time.Second
	cleanupStalledRequeueInterval    = 60 * time.Second
	memoryMBToKBMultiplier           = 1024
)

const defaultMaxConcurrentReconciles = 50

// LibvirtMachineReconciler reconciles a LibvirtMachine object.
type LibvirtMachineReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	SSHClientFactory     SSHClientFactory
	LibvirtClientFactory LibvirtClientFactory
	ISOBuilder           iso.Builder
	// MaxConcurrentReconciles is the number of machines reconciled in parallel.
	// Each reconcile targets a different libvirt host over its own SSH connection.
	// Default: 50.
	MaxConcurrentReconciles int
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtmachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtmachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtmachines/finalizers,verbs=update
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirthosts,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=libvirtclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines;machines/status,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters;clusters/status,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile handles the lifecycle of a LibvirtMachine.
func (r *LibvirtMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("machine", req.Name, "namespace", req.Namespace)
	log.Info("Reconciling LibvirtMachine")
	ctx = logf.IntoContext(ctx, log)

	// Fetch LibvirtMachine.
	libvirtMachine := &infrav1.LibvirtMachine{}
	if err := r.Get(ctx, req.NamespacedName, libvirtMachine); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Create patch helper for deferred status patch.
	patchHelper, err := patch.NewHelper(libvirtMachine, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer func() {
		if patchErr := patchHelper.Patch(ctx, libvirtMachine); patchErr != nil {
			log.Error(patchErr, "Failed to patch LibvirtMachine")
		}
	}()

	// Fetch owner Machine.
	machine, err := util.GetOwnerMachine(ctx, r.Client, libvirtMachine.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if machine == nil {
		log.Info("Waiting for Machine controller to set OwnerRef on LibvirtMachine")
		return ctrl.Result{}, nil
	}

	// Fetch owner Cluster.
	cluster, err := util.GetClusterFromMetadata(ctx, r.Client, libvirtMachine.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}

	// If cluster is paused, return without requeueing.
	if annotations.IsPaused(cluster, libvirtMachine) {
		log.Info("LibvirtMachine or owning Cluster is paused, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// Fetch LibvirtCluster from Cluster's InfrastructureRef.
	libvirtCluster := &infrav1.LibvirtCluster{}
	infraRef := cluster.Spec.InfrastructureRef
	if infraRef == nil {
		return ctrl.Result{}, fmt.Errorf("cluster %s/%s has no InfrastructureRef", cluster.Namespace, cluster.Name)
	}
	infraKey := types.NamespacedName{
		Namespace: libvirtMachine.Namespace,
		Name:      infraRef.Name,
	}
	if err := r.Get(ctx, infraKey, libvirtCluster); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get LibvirtCluster: %w", err)
	}

	// Fetch LibvirtHost from spec.hostRef.
	libvirtHost := &infrav1.LibvirtHost{}
	hostKey := types.NamespacedName{
		Namespace: libvirtMachine.Namespace,
		Name:      libvirtMachine.Spec.HostRef.Name,
	}
	if err := r.Get(ctx, hostKey, libvirtHost); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get LibvirtHost: %w", err)
	}

	// Dispatch to delete or normal reconciliation.
	if !libvirtMachine.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, libvirtMachine, machine, cluster, libvirtCluster, libvirtHost)
	}
	return r.reconcileNormal(ctx, libvirtMachine, machine, cluster, libvirtCluster, libvirtHost)
}

// reconcileNormal handles creating and configuring the libvirt domain.
func (r *LibvirtMachineReconciler) reconcileNormal(
	ctx context.Context,
	libvirtMachine *infrav1.LibvirtMachine,
	machine *clusterv1.Machine,
	cluster *clusterv1.Cluster,
	libvirtCluster *infrav1.LibvirtCluster,
	libvirtHost *infrav1.LibvirtHost,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Add finalizer if not present.
	if !controllerutil.ContainsFinalizer(libvirtMachine, infrav1.MachineFinalizer) {
		controllerutil.AddFinalizer(libvirtMachine, infrav1.MachineFinalizer)
	}

	// Check LibvirtHost readiness.
	if !libvirtHost.Status.Ready {
		log.Info("LibvirtHost is not ready, requeueing", "host", libvirtHost.Name)
		apimeta.SetStatusCondition(&libvirtMachine.Status.Conditions, metav1.Condition{
			Type:               infrav1.HostReachableForMachineCondition,
			Status:             metav1.ConditionFalse,
			Reason:             infrav1.ReasonHostNotReady,
			Message:            "LibvirtHost is not ready",
			ObservedGeneration: libvirtMachine.Generation,
		})
		return ctrl.Result{RequeueAfter: hostNotReadyRequeueInterval}, nil
	}

	// Fetch SSH secret and create clients.
	sshClient, libvirtClient, err := r.createClients(ctx, libvirtHost)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create clients: %w", err)
	}
	defer sshClient.Close()
	defer libvirtClient.Close()

	// Attach logger to VirshClient if applicable.
	if vc, ok := libvirtClient.(*libvirt.VirshClient); ok {
		vc.WithLogger(log)
	}

	// Check bootstrap data readiness.
	if machine.Spec.Bootstrap.DataSecretName == nil {
		log.Info("Bootstrap data not yet available, requeueing")
		apimeta.SetStatusCondition(&libvirtMachine.Status.Conditions, metav1.Condition{
			Type:               infrav1.BootstrapDataReadyCondition,
			Status:             metav1.ConditionFalse,
			Reason:             infrav1.ReasonBootstrapDataNotReady,
			Message:            "Waiting for bootstrap data secret",
			ObservedGeneration: libvirtMachine.Generation,
		})
		return ctrl.Result{RequeueAfter: bootstrapNotReadyRequeueInterval}, nil
	}

	// Build MachineScope.
	machineScope, err := scope.NewMachineScope(scope.MachineScopeParams{
		Client:         r.Client,
		Cluster:        cluster,
		Machine:        machine,
		LibvirtCluster: libvirtCluster,
		LibvirtMachine: libvirtMachine,
		LibvirtHost:    libvirtHost,
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create machine scope: %w", err)
	}

	// Resolve auto-sizing: if vcpus or memoryMB are zero, use host capacity.
	resolvedVCPUs := libvirtMachine.Spec.Domain.VCPUs
	resolvedMemoryMB := libvirtMachine.Spec.Domain.MemoryMB
	if resolvedVCPUs == 0 || resolvedMemoryMB == 0 {
		if libvirtHost.Status.Capacity == nil {
			log.Info("Host capacity not yet discovered, requeueing")
			return ctrl.Result{RequeueAfter: hostNotReadyRequeueInterval}, nil
		}
		if resolvedVCPUs == 0 {
			resolvedVCPUs = libvirtHost.Status.Capacity.AvailableVCPUs
		}
		if resolvedMemoryMB == 0 {
			resolvedMemoryMB = libvirtHost.Status.Capacity.AvailableMemoryMB
		}
		if resolvedVCPUs <= 0 || resolvedMemoryMB <= 0 {
			log.Error(fmt.Errorf("insufficient resources: %d vCPUs, %d MB", resolvedVCPUs, resolvedMemoryMB),
				"Terminal error", "operation", "auto-sizing", "reason", infrav1.ReasonStorageInsufficient)
			return r.setTerminalError(libvirtMachine, infrav1.ReasonStorageInsufficient,
				fmt.Sprintf("host %s has insufficient available resources: %d vCPUs, %d MB",
					libvirtHost.Name, resolvedVCPUs, resolvedMemoryMB))
		}
		log.Info("Auto-sized VM from host capacity", "vcpus", resolvedVCPUs, "memoryMB", resolvedMemoryMB)
	}

	// Compute artifact names.
	domainName := machineScope.DomainName()
	rootDiskVolume := machineScope.RootDiskVolumeName()
	bootstrapISO := machineScope.BootstrapISOName()
	nvramPath := machineScope.NVRAMPath()

	// Enrich logger with host and domain context.
	log = log.WithValues("host", libvirtHost.Name, "domain", domainName)
	storagePool := libvirtMachine.Spec.RootDisk.StoragePool
	baseImagePool := libvirtMachine.Spec.RootDisk.BaseImagePool
	if baseImagePool == "" {
		baseImagePool = storagePool
	}

	// Step 0: Create ephemeral tmpfs pool if requested and not yet present.
	if libvirtMachine.Spec.RootDisk.EphemeralPool {
		ephPoolName := machineScope.EphemeralPoolName()
		ephPoolPath := machineScope.EphemeralPoolPath()
		poolExists, err := libvirtClient.PoolExists(ctx, ephPoolName)
		if err != nil {
			return r.handleLibvirtError(libvirtMachine, err, "checking ephemeral pool")
		}
		if !poolExists {
			log.Info("Creating ephemeral tmpfs pool", "pool", ephPoolName, "path", ephPoolPath)
			if err := libvirtClient.CreateTmpfsPool(ctx, ephPoolName, ephPoolPath); err != nil {
				log.Error(err, "Failed to create ephemeral tmpfs pool", "pool", ephPoolName)
				return r.handleLibvirtError(libvirtMachine, err, "creating ephemeral pool")
			}
			log.Info("Created ephemeral tmpfs pool", "pool", ephPoolName)
		}
		// Override storagePool to use the ephemeral pool for all VM artifacts.
		storagePool = ephPoolName
	}

	// Step 1: Create root disk if it does not exist.
	rootExists, err := libvirtClient.VolumeExists(ctx, storagePool, rootDiskVolume)
	if err != nil {
		return r.handleLibvirtError(libvirtMachine, err, "checking root disk")
	}
	if !rootExists {
		log.Info("Creating root disk volume", "volume", rootDiskVolume, "strategy", libvirtMachine.Spec.RootDisk.CloneStrategy)
		sizeBytes := libvirtMachine.Spec.RootDisk.Size.Value()
		diskStart := time.Now()

		switch libvirtMachine.Spec.RootDisk.CloneStrategy {
		case infrav1.CloneStrategyFullClone:
			if err := libvirtClient.CloneVolume(ctx, baseImagePool, libvirtMachine.Spec.RootDisk.BaseImage, rootDiskVolume); err != nil {
				if libvirt.IsNotFound(err) {
					log.Error(err, "Terminal error", "operation", "cloning root disk (full-clone)", "reason", infrav1.ReasonBaseImageNotFound)
					return r.setTerminalError(libvirtMachine, infrav1.ReasonBaseImageNotFound,
						fmt.Sprintf("Base image %q not found in pool %q", libvirtMachine.Spec.RootDisk.BaseImage, baseImagePool))
				}
				log.Error(err, "Failed to clone root disk (full-clone)", "volume", rootDiskVolume)
				return r.handleLibvirtError(libvirtMachine, err, "cloning root disk (full-clone)")
			}
		default: // copy-on-write
			backingPath, err := libvirtClient.GetVolumePath(ctx, baseImagePool, libvirtMachine.Spec.RootDisk.BaseImage)
			if err != nil {
				if libvirt.IsNotFound(err) {
					log.Error(err, "Terminal error", "operation", "getting base image path", "reason", infrav1.ReasonBaseImageNotFound)
					return r.setTerminalError(libvirtMachine, infrav1.ReasonBaseImageNotFound,
						fmt.Sprintf("Base image %q not found in pool %q", libvirtMachine.Spec.RootDisk.BaseImage, baseImagePool))
				}
				log.Error(err, "Failed to get base image path", "volume", rootDiskVolume)
				return r.handleLibvirtError(libvirtMachine, err, "getting base image path")
			}
			if err := libvirtClient.CreateVolumeFromBackingStore(ctx, storagePool, rootDiskVolume, backingPath, sizeBytes); err != nil {
				log.Error(err, "Failed to create root disk (copy-on-write)", "volume", rootDiskVolume)
				return r.handleLibvirtError(libvirtMachine, err, "creating root disk (copy-on-write)")
			}
		}
		log.Info("Created root disk volume", "volume", rootDiskVolume, "duration", time.Since(diskStart).String())
	}

	// Step 2: Prepare bootstrap data.
	ignitionFilePath := machineScope.IgnitionFilePath()
	bootstrapData, err := machineScope.GetBootstrapData(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get bootstrap data: %w", err)
	}

	switch libvirtMachine.Spec.BootstrapFormat {
	case infrav1.BootstrapFormatIgnition:
		// Write ignition JSON to host filesystem for fw_cfg delivery.
		log.Info("Writing ignition config to host", "path", ignitionFilePath)
		bootstrapStart := time.Now()
		if err := libvirtClient.WriteRemoteFile(ctx, ignitionFilePath, bootstrapData); err != nil {
			log.Error(err, "Failed to write ignition file", "path", ignitionFilePath)
			return r.handleLibvirtError(libvirtMachine, err, "writing ignition file")
		}
		log.Info("Wrote ignition config", "path", ignitionFilePath, "size", len(bootstrapData), "duration", time.Since(bootstrapStart).String())

	case infrav1.BootstrapFormatCloudInit:
		// Create NoCloud ISO and upload to storage pool.
		isoExists, err := libvirtClient.VolumeExists(ctx, storagePool, bootstrapISO)
		if err != nil {
			return r.handleLibvirtError(libvirtMachine, err, "checking bootstrap ISO")
		}
		if !isoExists {
			log.Info("Creating cloud-init ISO", "iso", bootstrapISO)
			bootstrapStart := time.Now()
			isoData, err := r.ISOBuilder.BuildCloudInitISO(bootstrapData, domainName, domainName)
			if err != nil {
				log.Error(err, "Terminal error", "operation", "building cloud-init ISO", "reason", infrav1.ReasonInvalidBootstrapData)
				return r.setTerminalError(libvirtMachine, infrav1.ReasonInvalidBootstrapData,
					fmt.Sprintf("failed to build cloud-init ISO: %v", err))
			}
			if err := libvirtClient.UploadVolumeFromBytes(ctx, storagePool, bootstrapISO, isoData); err != nil {
				log.Error(err, "Failed to upload cloud-init ISO", "iso", bootstrapISO)
				return r.handleLibvirtError(libvirtMachine, err, "uploading cloud-init ISO")
			}
			log.Info("Created cloud-init ISO", "iso", bootstrapISO, "duration", time.Since(bootstrapStart).String())
		}

	default:
		log.Error(fmt.Errorf("unsupported bootstrap format: %s", libvirtMachine.Spec.BootstrapFormat),
			"Terminal error", "operation", "preparing bootstrap", "reason", infrav1.ReasonInvalidBootstrapData)
		return r.setTerminalError(libvirtMachine, infrav1.ReasonInvalidBootstrapData,
			fmt.Sprintf("unsupported bootstrap format: %s", libvirtMachine.Spec.BootstrapFormat))
	}

	// Step 3: Create additional disks if they do not exist.
	var additionalDiskVolumes []string
	var additionalDiskParams []libvirt.DiskParam
	for _, disk := range libvirtMachine.Spec.AdditionalDisks {
		volName := fmt.Sprintf("%s-%s.qcow2", machineScope.ArtifactBaseName(), disk.Name)
		additionalDiskVolumes = append(additionalDiskVolumes, volName)

		exists, err := libvirtClient.VolumeExists(ctx, disk.StoragePool, volName)
		if err != nil {
			return r.handleLibvirtError(libvirtMachine, err, "checking additional disk")
		}
		if !exists {
			log.Info("Creating additional disk", "volume", volName)
			if err := libvirtClient.CreateVolume(ctx, disk.StoragePool, volName, disk.Size.Value()); err != nil {
				return r.handleLibvirtError(libvirtMachine, err, "creating additional disk")
			}
		}

		diskPath, err := libvirtClient.GetVolumePath(ctx, disk.StoragePool, volName)
		if err != nil {
			return r.handleLibvirtError(libvirtMachine, err, "getting additional disk path")
		}
		additionalDiskParams = append(additionalDiskParams, libvirt.DiskParam{
			Path: diskPath,
			Bus:  disk.Bus,
		})
	}

	// Step 4: Define domain if it does not exist.
	domainExists, err := libvirtClient.DomainExists(ctx, domainName)
	if err != nil {
		return r.handleLibvirtError(libvirtMachine, err, "checking domain")
	}
	if !domainExists {
		log.Info("Defining domain", "domain", domainName)
		defineStart := time.Now()
		rootDiskPath, err := libvirtClient.GetVolumePath(ctx, storagePool, rootDiskVolume)
		if err != nil {
			return r.handleLibvirtError(libvirtMachine, err, "getting root disk path")
		}

		xmlParams := libvirt.DomainXMLParams{
			Name:            domainName,
			VCPUs:           resolvedVCPUs,
			MemoryKB:        int64(resolvedMemoryMB) * memoryMBToKBMultiplier,
			Machine:         libvirtMachine.Spec.Domain.Machine,
			Firmware:        string(libvirtMachine.Spec.Domain.Firmware),
			FirmwarePath:    libvirtHost.Spec.FirmwarePath,
			NVRAMPath:       nvramPath,
			RootDiskPath:    rootDiskPath,
			RootDiskBus:     libvirtMachine.Spec.RootDisk.Bus,
			AdditionalDisks: additionalDiskParams,
			NetworkType:     string(libvirtMachine.Spec.Network.Type),
			NetworkName:     libvirtMachine.Spec.Network.Name,
			NetworkModel:    libvirtMachine.Spec.Network.Model,
			MACAddress:      libvirtMachine.Spec.Network.MACAddress,
		}

		switch libvirtMachine.Spec.BootstrapFormat {
		case infrav1.BootstrapFormatIgnition:
			xmlParams.IgnitionPath = ignitionFilePath
		case infrav1.BootstrapFormatCloudInit:
			isoPath, err := libvirtClient.GetVolumePath(ctx, storagePool, bootstrapISO)
			if err != nil {
				return r.handleLibvirtError(libvirtMachine, err, "getting cloud-init ISO path")
			}
			xmlParams.BootstrapISOPath = isoPath
		}

		xmlDef, err := libvirt.GenerateDomainXML(xmlParams)
		if err != nil {
			log.Error(err, "Terminal error", "operation", "generating domain XML", "reason", infrav1.ReasonSpecMismatch)
			return r.setTerminalError(libvirtMachine, infrav1.ReasonSpecMismatch,
				fmt.Sprintf("failed to generate domain XML: %v", err))
		}

		if _, err := libvirtClient.DefineDomain(ctx, xmlDef); err != nil {
			log.Error(err, "Failed to define domain", "domain", domainName)
			return r.handleLibvirtError(libvirtMachine, err, "defining domain")
		}
		log.Info("Defined domain", "domain", domainName, "duration", time.Since(defineStart).String())
	}

	// Step 5: Start domain if not running.
	domainInfo, err := libvirtClient.GetDomain(ctx, domainName)
	if err != nil {
		return r.handleLibvirtError(libvirtMachine, err, "getting domain info")
	}
	if domainInfo.State != "running" {
		log.Info("Starting domain", "domain", domainName, "currentState", domainInfo.State)
		startTime := time.Now()
		if err := libvirtClient.StartDomain(ctx, domainName); err != nil {
			log.Error(err, "Failed to start domain", "domain", domainName)
			return r.handleLibvirtError(libvirtMachine, err, "starting domain")
		}
		log.Info("Started domain", "domain", domainName, "duration", time.Since(startTime).String())
		// Re-fetch domain info after start.
		domainInfo, err = libvirtClient.GetDomain(ctx, domainName)
		if err != nil {
			return r.handleLibvirtError(libvirtMachine, err, "getting domain info after start")
		}
	}

	// Update status.
	libvirtMachine.Status.Addresses = machineScope.GetAddresses()
	providerID := machineScope.ProviderID()
	libvirtMachine.Spec.ProviderID = &providerID
	artifacts := &infrav1.ManagedArtifacts{
		DomainName:            domainName,
		RootDiskVolume:        rootDiskVolume,
		NVRAMPath:             nvramPath,
		AdditionalDiskVolumes: additionalDiskVolumes,
	}
	switch libvirtMachine.Spec.BootstrapFormat {
	case infrav1.BootstrapFormatIgnition:
		artifacts.IgnitionFile = ignitionFilePath
	case infrav1.BootstrapFormatCloudInit:
		artifacts.BootstrapISO = bootstrapISO
	}
	if libvirtMachine.Spec.RootDisk.EphemeralPool {
		artifacts.EphemeralPoolName = machineScope.EphemeralPoolName()
	}
	libvirtMachine.Status.ManagedArtifacts = artifacts
	libvirtMachine.Status.DomainState = domainInfo.State
	libvirtMachine.Status.DomainUUID = domainInfo.UUID
	libvirtMachine.Status.Ready = true

	apimeta.SetStatusCondition(&libvirtMachine.Status.Conditions, metav1.Condition{
		Type:               infrav1.InfrastructureReadyCondition,
		Status:             metav1.ConditionTrue,
		Reason:             infrav1.ReasonProvisioned,
		Message:            "Machine infrastructure is ready",
		ObservedGeneration: libvirtMachine.Generation,
	})

	log.Info("Machine infrastructure ready", "providerID", providerID)

	return ctrl.Result{}, nil
}

// reconcileDelete cleans up libvirt artifacts and removes the finalizer.
func (r *LibvirtMachineReconciler) reconcileDelete(
	ctx context.Context,
	libvirtMachine *infrav1.LibvirtMachine,
	_ *clusterv1.Machine,
	cluster *clusterv1.Cluster,
	libvirtCluster *infrav1.LibvirtCluster,
	libvirtHost *infrav1.LibvirtHost,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// If finalizer is not present, nothing to do.
	if !controllerutil.ContainsFinalizer(libvirtMachine, infrav1.MachineFinalizer) {
		return ctrl.Result{}, nil
	}

	// Check host reachability.
	if !libvirtHost.Status.Ready {
		log.Info("LibvirtHost is not reachable, stalling cleanup", "host", libvirtHost.Name)
		apimeta.SetStatusCondition(&libvirtMachine.Status.Conditions, metav1.Condition{
			Type:               infrav1.CleanupStalledCondition,
			Status:             metav1.ConditionTrue,
			Reason:             infrav1.ReasonCleanupStalled,
			Message:            "Cannot clean up: LibvirtHost is not reachable",
			ObservedGeneration: libvirtMachine.Generation,
		})
		return ctrl.Result{RequeueAfter: cleanupStalledRequeueInterval}, nil
	}

	// Create clients.
	sshClient, libvirtClient, err := r.createClients(ctx, libvirtHost)
	if err != nil {
		apimeta.SetStatusCondition(&libvirtMachine.Status.Conditions, metav1.Condition{
			Type:               infrav1.CleanupStalledCondition,
			Status:             metav1.ConditionTrue,
			Reason:             infrav1.ReasonCleanupStalled,
			Message:            "Cannot clean up: " + err.Error(),
			ObservedGeneration: libvirtMachine.Generation,
		})
		return ctrl.Result{RequeueAfter: cleanupStalledRequeueInterval}, nil
	}
	defer sshClient.Close()
	defer libvirtClient.Close()

	// Build scope for artifact names.
	machineScope, err := scope.NewMachineScope(scope.MachineScopeParams{
		Client:         r.Client,
		Cluster:        cluster,
		Machine:        &clusterv1.Machine{}, // Minimal; only used for artifact name computation.
		LibvirtCluster: libvirtCluster,
		LibvirtMachine: libvirtMachine,
		LibvirtHost:    libvirtHost,
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create machine scope for cleanup: %w", err)
	}

	domainName := machineScope.DomainName()
	rootDiskVolume := machineScope.RootDiskVolumeName()
	bootstrapISO := machineScope.BootstrapISOName()
	storagePool := libvirtMachine.Spec.RootDisk.StoragePool
	// If ephemeral pool was used, volumes live in the per-machine pool.
	if libvirtMachine.Spec.RootDisk.EphemeralPool {
		storagePool = machineScope.EphemeralPoolName()
	}

	// Enrich logger with host and domain context.
	log = log.WithValues("host", libvirtHost.Name, "domain", domainName)

	// Destroy domain (ignore not-found).
	log.Info("Destroying domain", "domain", domainName)
	if err := libvirtClient.DestroyDomain(ctx, domainName); err != nil && !libvirt.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to destroy domain: %w", err)
	}

	// Undefine domain (ignore not-found).
	log.Info("Undefining domain", "domain", domainName)
	if err := libvirtClient.UndefineDomain(ctx, domainName); err != nil && !libvirt.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to undefine domain: %w", err)
	}

	// Delete root disk volume.
	log.Info("Deleting root disk volume", "volume", rootDiskVolume)
	if err := libvirtClient.DeleteVolume(ctx, storagePool, rootDiskVolume); err != nil && !libvirt.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to delete root disk: %w", err)
	}

	// Delete bootstrap artifacts.
	switch libvirtMachine.Spec.BootstrapFormat {
	case infrav1.BootstrapFormatIgnition:
		ignitionFile := machineScope.IgnitionFilePath()
		log.Info("Deleting ignition file", "path", ignitionFile)
		if err := libvirtClient.DeleteRemoteFile(ctx, ignitionFile); err != nil {
			log.Error(err, "Failed to delete ignition file (non-fatal)", "path", ignitionFile)
		}
	case infrav1.BootstrapFormatCloudInit:
		log.Info("Deleting cloud-init ISO", "iso", bootstrapISO)
		if err := libvirtClient.DeleteVolume(ctx, storagePool, bootstrapISO); err != nil && !libvirt.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to delete cloud-init ISO: %w", err)
		}
	}

	// Delete additional disks.
	for _, disk := range libvirtMachine.Spec.AdditionalDisks {
		volName := fmt.Sprintf("%s-%s.qcow2", machineScope.ArtifactBaseName(), disk.Name)
		log.Info("Deleting additional disk", "volume", volName)
		if err := libvirtClient.DeleteVolume(ctx, disk.StoragePool, volName); err != nil && !libvirt.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to delete additional disk %s: %w", volName, err)
		}
	}

	// Destroy ephemeral tmpfs pool if managed by CAPLV.
	if libvirtMachine.Spec.RootDisk.EphemeralPool {
		ephPoolName := machineScope.EphemeralPoolName()
		log.Info("Destroying ephemeral pool and tmpfs", "pool", ephPoolName)
		if err := libvirtClient.DestroyPool(ctx, ephPoolName); err != nil && !libvirt.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to destroy ephemeral pool: %w", err)
		}
	}

	// Remove finalizer.
	controllerutil.RemoveFinalizer(libvirtMachine, infrav1.MachineFinalizer)

	return ctrl.Result{}, nil
}

// createClients creates SSH and libvirt clients for the given host.
func (r *LibvirtMachineReconciler) createClients(ctx context.Context, host *infrav1.LibvirtHost) (*gossh.Client, libvirt.Client, error) {
	if host.Spec.SecretRef == nil {
		return nil, nil, fmt.Errorf("LibvirtHost %s has no secretRef configured", host.Name)
	}

	secretNS := host.Spec.SecretRef.Namespace
	if secretNS == "" {
		secretNS = host.Namespace
	}
	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{Namespace: secretNS, Name: host.Spec.SecretRef.Name}
	if err := r.Get(ctx, secretKey, secret); err != nil {
		return nil, nil, fmt.Errorf("failed to get SSH secret: %w", err)
	}

	sshClient, err := r.SSHClientFactory(ctx, host, secret)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create SSH client: %w", err)
	}

	libvirtClient := r.LibvirtClientFactory(sshClient)
	return sshClient, libvirtClient, nil
}

// handleLibvirtError inspects the error and returns appropriate results.
func (r *LibvirtMachineReconciler) handleLibvirtError(
	libvirtMachine *infrav1.LibvirtMachine,
	err error,
	operation string,
) (ctrl.Result, error) {
	if libvirt.IsTerminal(err) {
		reason := infrav1.ReasonSpecMismatch
		msg := fmt.Sprintf("terminal error during %s: %v", operation, err)
		libvirtMachine.Status.FailureReason = &reason
		libvirtMachine.Status.FailureMessage = &msg
		libvirtMachine.Status.Ready = false
		return ctrl.Result{}, nil
	}
	return ctrl.Result{}, fmt.Errorf("error during %s: %w", operation, err)
}

// setTerminalError sets a terminal failure on the machine and stops reconciliation.
func (r *LibvirtMachineReconciler) setTerminalError(
	libvirtMachine *infrav1.LibvirtMachine,
	reason string,
	message string,
) (ctrl.Result, error) {
	libvirtMachine.Status.FailureReason = &reason
	libvirtMachine.Status.FailureMessage = &message
	libvirtMachine.Status.Ready = false
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *LibvirtMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	concurrency := r.MaxConcurrentReconciles
	if concurrency <= 0 {
		concurrency = defaultMaxConcurrentReconciles
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.LibvirtMachine{}).
		Watches(&clusterv1.Machine{}, handler.EnqueueRequestForOwner(
			mgr.GetScheme(), mgr.GetRESTMapper(), &infrav1.LibvirtMachine{},
		)).
		WithOptions(controller.Options{MaxConcurrentReconciles: concurrency}).
		Named("libvirtmachine").
		Complete(r)
}
