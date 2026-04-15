# CAPLV — *EXPERIMENTAL* Cluster API Provider for LibVirt

CAPLV is an *EXPERIMENTAL* [Cluster API](https://cluster-api.sigs.k8s.io/)
infrastructure provider that provisions KVM virtual machines on libvirt
hosts. It is built exclusively for
[5-Spot](https://github.com/RBC/5-spot), which schedules OpenShift worker
nodes on and off physical RHEL/KVM infrastructure based on time-of-day
rules.

## How It Works

```
5-Spot: schedule becomes active
  → Creates CAPI Machine + CAPLV LibvirtMachine
    → CAPLV connects to libvirt host over SSH
      → Clones RHCOS base image, creates bootstrap artifact
        → Defines and starts KVM domain
          → VM boots, joins OpenShift cluster as worker

5-Spot: schedule becomes inactive
  → Deletes CAPI Machine
    → CAPLV destroys domain, cleans up disks and ISOs
```

Each libvirt host runs exactly one CAPLV-managed VM. Hundreds or thousands
of hosts can come online simultaneously — the controller runs up to 50
concurrent reconcilers by default (`--max-concurrent-reconciles`), each
operating against a different host over its own SSH connection.

## Custom Resources

| CRD | Purpose |
|-----|---------|
| **LibvirtHost** | Reusable connection details for a libvirt hypervisor (URI, SSH key, host key fingerprint, OVMF paths). Periodically verified via SSH + `virsh version`. |
| **LibvirtCluster** | CAPI contract stub — sets `status.ready: true` immediately. No infrastructure provisioned. |
| **LibvirtMachine** | Core resource — a single KVM VM with static IP, UEFI firmware, and ignition or cloud-init bootstrap. |

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
        storagePool: "ephemeral"
        baseImagePool: "default"
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

### Ephemeral storage with tmpfs

VMs are ephemeral — created and destroyed on demand by 5-Spot schedules.
To keep all per-VM artifacts in RAM:

```bash
# On each libvirt host (one-time setup):
mkdir -p /run/caplv-ephemeral
mount -t tmpfs -o size=120G tmpfs /run/caplv-ephemeral
virsh pool-define-as ephemeral dir --target /run/caplv-ephemeral
virsh pool-start ephemeral
virsh pool-autostart ephemeral
```

Then set `storagePool: "ephemeral"` and `baseImagePool: "default"` in the
LibvirtMachine spec. The persistent base image stays on disk (read-only);
the CoW overlay, bootstrap ISO, and NVRAM all live in RAM.

## Key Design Decisions

- **virsh-over-SSH** — pure Go binary (`CGO_ENABLED=0`), distroless container.
  No `libvirt-dev`, no CGo. The `Client` interface allows swapping to native
  libvirt bindings later.
- **Static IPs required** — every VM must declare its network identity upfront.
  No DHCP. This matches 5-Spot's model of predetermined machine configurations.
- **Parallel at scale** — 50 concurrent reconcilers by default. Each host runs
  one VM, so there are no contention issues across parallel provisions.
- **Ephemeral storage** — optional tmpfs-backed storage pools keep all per-VM
  artifacts in RAM. `baseImagePool` separates the persistent base image from
  ephemeral CoW overlays.
- **Bootstrap pass-through** — CAPLV does not modify ignition or cloud-init data.
  Network configuration is the bootstrap provider's responsibility. Phase 1 uses
  an attached ignition ISO; the target OpenShift flow is RHCOS live installer
  with `coreos-installer` semantics (Phase 1.5).
- **Deterministic artifact naming** — domain names, disk volumes, and ISOs are
  named `<namespace>-<cluster>-<machine>`. Artifacts can be rediscovered after a
  controller crash without relying on status writes.
- **Immutable spec** — all spec fields are frozen after creation. To change VM
  configuration, delete and recreate. Enforced by admission webhook.
- **Finalizer safety** — the finalizer is never removed until cleanup is confirmed
  on the libvirt host. If the host is unreachable, the resource stays and a
  `CleanupStalled` condition is surfaced for operator intervention.

## Architecture

```
┌─────────────┐     ┌──────────────────┐     ┌─────────────────┐
│   5-Spot    │────▶│   CAPI Core      │────▶│     CAPLV       │
│  Scheduler  │     │  (Machine CR)    │     │  (50 workers)   │
└─────────────┘     └──────────────────┘     └────────┬────────┘
                                                      │ SSH x N
                                              ┌───────▼─────────┐
                                              │  libvirt hosts   │
                                              │  (RHEL + KVM)    │
                                              │                  │
                                              │  1 VM per host   │
                                              │  tmpfs optional  │
                                              └──────────────────┘
```

## Host Storage Layout

```
/var/lib/libvirt/images/            (persistent pool "default")
  └── rhcos-4.14.qcow2             ← pre-staged by operator (read-only)

/run/caplv-ephemeral/               (tmpfs pool "ephemeral", optional)
  ├── ns-cluster-worker01-root.qcow2       ← CoW overlay (CAPLV creates/deletes)
  └── ns-cluster-worker01-bootstrap.iso    ← ignition ISO (CAPLV creates/deletes)

/var/lib/libvirt/qemu/nvram/
  └── ns-cluster-worker01_VARS.fd   ← UEFI NVRAM (libvirt manages)
```

If not using tmpfs, all artifacts go into the persistent pool.

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

# Build container image (pure Go, no CGo)
make docker-build IMG=ghcr.io/atgreen/caplv:latest

# Deploy to cluster
make deploy IMG=ghcr.io/atgreen/caplv:latest
```

## Project Structure

```
api/v1alpha1/          CRD type definitions (LibvirtHost, LibvirtCluster, LibvirtMachine)
internal/
  controller/          Reconcilers for all three CRDs (50 concurrent machine workers)
  libvirt/             Client interface, virsh-over-SSH, domain XML generation
  iso/                 Ignition and cloud-init ISO creation (pure Go, go-diskfs)
  ssh/                 SSH client with host key verification
  scope/               MachineScope — gathers reconciliation context, artifact naming
  webhook/             Admission webhook (immutable spec, required static IP)
config/                Kubernetes manifests (CRDs, RBAC, deployment, webhook)
docs/                  PRD and design documentation
```

## License

Copyright 2026 Anthony Green.

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.
