# CAPLV — *EXPERIMENTAL* Cluster API Provider for LibVirt

CAPLV is an *EXPERIMENTAL* [Cluster API](https://cluster-api.sigs.k8s.io/)
infrastructure provider that provisions KVM virtual machines on
libvirt hosts. It is built exclusively for
[5-Spot](https://github.com/RBC/5-spot), which schedules OpenShift
worker nodes on and off physical RHEL/KVM infrastructure based on
time-of-day rules.

## How It Works

```
5-Spot: "It's 9am Monday — schedule is active"
  → Creates CAPI Machine + CAPLV LibvirtMachine
    → CAPLV connects to libvirt host over SSH
      → Clones RHCOS base image, creates ignition ISO
        → Defines and starts KVM domain
          → VM boots, joins OpenShift cluster as worker

5-Spot: "It's 5pm Friday — schedule is inactive"
  → Deletes CAPI Machine
    → CAPLV destroys domain, cleans up disks and ISOs
```

## Custom Resources

| CRD | Purpose |
|-----|---------|
| **LibvirtHost** | Reusable connection details for a libvirt hypervisor (URI, SSH key, host key fingerprint, OVMF paths) |
| **LibvirtCluster** | CAPI contract stub — sets `status.ready: true` immediately. No infrastructure provisioned. |
| **LibvirtMachine** | Core resource — represents a single KVM VM with static IP, UEFI firmware, and ignition bootstrap |

## Example

### Register a libvirt host

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtHost
metadata:
  name: rhel-host-01
spec:
  uri: "qemu+ssh://root@rhel-host-01.example.com/system"
  secretRef:
    name: rhel-host-01-ssh-key
  hostKeyFingerprint: "SHA256:abc123..."
```

### Schedule a worker via 5-Spot

```yaml
apiVersion: 5spot.finos.org/v1alpha1
kind: ScheduledMachine
metadata:
  name: weekday-worker-01
spec:
  clusterName: my-ocp-cluster
  schedule:
    daysOfWeek: ["mon-fri"]
    hoursOfDay: ["9-17"]
    timezone: "America/Toronto"
    enabled: true
  infrastructureSpec:
    apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
    kind: LibvirtMachine
    spec:
      hostRef:
        name: rhel-host-01
      domain:
        vcpus: 4
        memoryMB: 8192
      rootDisk:
        size: "100Gi"
        storagePool: "default"
        baseImage: "rhcos-4.14.qcow2"
      network:
        type: "bridge"
        name: "br0"
        addresses:
          - "192.168.1.50/24"
        gateway: "192.168.1.1"
        dns:
          nameservers:
            - "192.168.1.1"
      bootstrapFormat: "ignition"
```

## Key Design Decisions

- **virsh-over-SSH** — no CGo, no `libvirt-dev` dependency. Pure Go binary in a
  distroless container. The libvirt `Client` interface allows swapping to native
  bindings later.
- **Static IPs required** — every VM must declare its network identity upfront.
  No DHCP. This matches 5-Spot's model of predetermined machine configurations.
- **Bootstrap pass-through** — CAPLV does not modify ignition or cloud-init data.
  Network configuration is the bootstrap provider's responsibility.
- **Deterministic artifact naming** — domain names, disk volumes, and ISOs are
  named `<namespace>-<cluster>-<machine>`. Artifacts can be rediscovered after a
  controller crash without relying on status writes.
- **Immutable spec** — all spec fields are frozen after creation. To change VM
  configuration, delete and recreate. Enforced by admission webhook.
- **Finalizer safety** — the finalizer is never removed until cleanup is confirmed
  on the libvirt host. If the host is unreachable, the resource stays and a
  `CleanupStalled` condition is surfaced.

## Architecture

```
┌─────────────┐     ┌──────────────────┐     ┌─────────────────┐
│   5-Spot    │────▶│   CAPI Core      │────▶│     CAPLV       │
│  Scheduler  │     │  (Machine CR)    │     │  Controller     │
└─────────────┘     └──────────────────┘     └────────┬────────┘
                                                      │ SSH
                                              ┌───────▼─────────┐
                                              │  libvirt host   │
                                              │  (RHEL + KVM)   │
                                              │                 │
                                              │  ┌───────────┐  │
                                              │  │  KVM VM   │  │
                                              │  │  (RHCOS)  │  │
                                              │  └───────────┘  │
                                              └─────────────────┘
```

## Prerequisites

- Go 1.25+
- Access to a Kubernetes cluster with CAPI installed
- libvirt hosts reachable over SSH from the management cluster
- RHCOS (or other) qcow2 base images pre-staged in libvirt storage pools
- OVMF firmware packages installed on libvirt hosts (for UEFI boot)

## Development

```bash
# Build
make build

# Run tests
make test

# Generate CRDs and deepcopy
make manifests generate

# Build container image
make docker-build IMG=ghcr.io/green/caplv:latest

# Deploy to cluster
make deploy IMG=ghcr.io/green/caplv:latest
```

## Project Structure

```
api/v1alpha1/          CRD type definitions (LibvirtHost, LibvirtCluster, LibvirtMachine)
internal/
  controller/          Reconcilers for all three CRDs
  libvirt/             Client interface, virsh-over-SSH implementation, domain XML generation
  iso/                 Ignition and cloud-init ISO creation (pure Go, go-diskfs)
  ssh/                 SSH client helper with host key verification
  scope/               MachineScope — gathers reconciliation context
  webhook/             Admission webhook (immutable spec, required static IP)
config/                Kubernetes manifests (CRDs, RBAC, deployment, webhook)
docs/                  PRD and design documentation
```

## License

Copyright 2026 Anthony Green.

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.
