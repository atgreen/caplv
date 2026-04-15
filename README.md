# CAPLV — *EXPERIMENTAL* Cluster API Provider for LibVirt

CAPLV is an *EXPERIMENTAL* [Cluster API](https://cluster-api.sigs.k8s.io/)
infrastructure provider that provisions KVM virtual machines on libvirt
hosts. It is built exclusively for
[5-Spot](https://github.com/RBC/5-spot), which schedules OpenShift worker
nodes on and off physical RHEL/KVM infrastructure based on time-of-day
rules.

The target hosts are machines with incumbent workloads — DR standby
systems, batch processing servers, or desktops that sit idle outside
business hours. CAPLV is designed to be minimally disruptive to these
hosts: worker-node VMs are fully ephemeral and, when backed by a
tmpfs storage pool, won't touch persistent storage on the device at all.

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
| **LibvirtCluster** | CAPI contract resource — verifies the control plane endpoint is reachable (TCP dial) before reporting ready. No infrastructure provisioned. |
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
        storagePool: "vm-disks"
        baseImagePool: "default"
        baseImage: "rhcos-4.14.qcow2"
        ephemeralPool: true
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
To avoid touching persistent storage on hosts with incumbent workloads,
set `ephemeralPool: true`:

```yaml
rootDisk:
  storagePool: "vm-disks"        # CAPLV creates this as tmpfs on demand
  baseImagePool: "default"       # persistent pool with pre-staged base image
  baseImage: "rhcos-4.14.qcow2"
  ephemeralPool: true
```

**No host setup required.** CAPLV creates a per-machine tmpfs mount and
libvirt storage pool when the VM is provisioned, and tears both down when
the VM is deleted. RAM is only consumed while the VM exists. The host's
persistent storage is never touched.

### MachineHealthCheck (recommended)

When the designed deletion path is followed (5-Spot → CAPI → CAPLV),
CAPI drains pods and deletes the `Node` object from OpenShift before the
VM is destroyed. Everything cleans up automatically.

However, if a VM dies unexpectedly (host crash, OOM kill, admin
`virsh destroy`), the `Node` object lingers in `NotReady` state in
OpenShift indefinitely. A CAPI `MachineHealthCheck` detects this and
triggers remediation — deleting the orphaned Machine and its stale Node.

```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: MachineHealthCheck
metadata:
  name: caplv-worker-health
  namespace: default
spec:
  clusterName: my-ocp-cluster
  selector:
    matchLabels:
      node-role.kubernetes.io/worker: ""
  unhealthyConditions:
    - type: Ready
      status: "False"
      timeout: 5m
    - type: Ready
      status: "Unknown"
      timeout: 5m
  nodeStartupTimeout: 10m
```

This is the safety net for the known limitation that CAPLV does not
monitor VM health after provisioning. Without it, dead VMs leave orphaned
Node objects in the cluster.

## Key Design Decisions

- **virsh-over-SSH** — pure Go binary (`CGO_ENABLED=0`), distroless container.
  No `libvirt-dev`, no CGo. The `Client` interface allows swapping to native
  libvirt bindings later.
- **Static IPs required** — every VM must declare its network identity upfront.
  No DHCP. This matches 5-Spot's model of predetermined machine configurations.
- **Control plane health gate** — LibvirtCluster verifies the OpenShift API
  endpoint is reachable (TCP dial, 60s recheck) before allowing machine
  provisioning. Prevents spinning up hundreds of workers against a dead
  control plane.
- **Parallel at scale** — 50 concurrent reconcilers by default. Each host runs
  one VM, so there are no contention issues across parallel provisions.
- **Ephemeral storage** — `ephemeralPool: true` creates a per-machine tmpfs
  mount and libvirt pool on demand, destroys both on cleanup. No host setup
  required, no persistent storage touched, RAM reclaimed when the VM goes away.
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

## Host Storage Layout (with ephemeralPool: true)

```
/var/lib/libvirt/images/                    (persistent pool "default")
  └── rhcos-4.14.qcow2                     ← pre-staged by operator (read-only)

/run/caplv/ns-cluster-worker01/             (tmpfs, CAPLV creates/destroys)
  ├── ns-cluster-worker01-root.qcow2       ← CoW overlay (in RAM)
  └── ns-cluster-worker01-bootstrap.iso    ← ignition ISO (in RAM)

/var/lib/libvirt/qemu/nvram/
  └── ns-cluster-worker01_VARS.fd           ← UEFI NVRAM (libvirt manages)
```

The tmpfs mount and libvirt pool are created when the VM is provisioned
and destroyed when the VM is deleted. No permanent host storage impact.

## Failure Modes

### Host failures

| Scenario | What happens | Recovery |
|----------|-------------|----------|
| **Host dies or reboots** | VM vanishes. LibvirtHost controller marks host not-ready on the next health check (per-host `spec.healthCheckIntervalSeconds`, default 5 min). LibvirtMachine finalizer stalls cleanup (`CleanupStalled` condition). | Operator fixes host. Once reachable again, CAPLV retries cleanup automatically. If the VM no longer exists, cleanup completes (not-found errors are ignored). |
| **Host network becomes unreachable** | Same as host death from CAPLV's perspective. SSH connections fail, host goes not-ready on next health check. | Network restored, host re-verified on next check cycle. |
| **libvirtd crashes or restarts** | SSH works but virsh commands fail. Host Ping check (`virsh version`) catches this on the next cycle, marks host not-ready. Running VMs may or may not survive depending on libvirt config. | libvirtd restarts, next health check restores host to ready. |
| **Incumbent workload reclaims resources** | OOM killer terminates the VM process, or host admin runs `virsh destroy`. Domain goes to `shutoff` state. | CAPLV does not currently detect post-ready VM state changes (see Known Limitations). CAPI MachineHealthCheck can detect the node going `NotReady`. |

### VM failures

| Scenario | What happens | Recovery |
|----------|-------------|----------|
| **VM killed externally** (`virsh destroy`) | Domain state changes to `shutoff`. CAPLV does not detect this after initial `ready=true`. | CAPI MachineHealthCheck detects the node disappearing and can trigger remediation. |
| **VM crashes** | Same as killed — domain state changes but CAPLV's status still shows `ready=true`. | Same as above. |
| **VM boots but never joins cluster** | Not CAPLV's responsibility. The node will sit in `NotReady` state. | CAPI MachineHealthCheck handles this. The bootstrap provider and machine approver must be functional. |

### Controller failures

| Scenario | What happens | Recovery |
|----------|-------------|----------|
| **Controller crashes mid-provisioning** | Partially created artifacts may exist on the host. | Automatic. Deterministic artifact naming allows the controller to discover existing artifacts and resume from where it left off. |
| **Controller crashes mid-deletion** | Finalizer remains on the resource, blocking garbage collection. | Automatic. On restart, the controller retries cleanup. Idempotent delete operations (not-found errors ignored) ensure safe retry. |
| **Leader election lost** | New leader picks up all pending reconciles. | Automatic. All operations are idempotent. |

### Control plane failures

| Scenario | What happens | Recovery |
|----------|-------------|----------|
| **OpenShift API goes down** | LibvirtCluster TCP dial fails, status goes `ready=false` with condition `ControlPlaneUnreachable`. CAPI will not create new machines while the InfrastructureCluster is not ready. Already-running VMs are unaffected. | Automatic. LibvirtCluster rechecks every 60s and restores ready when the API is reachable again. |
| **Machine approver is down** | VMs boot and request CSRs, but certificates are never approved. Nodes stay `NotReady`. | Operator restores the machine approver. Pending CSRs are approved and nodes join. |

### Storage failures (with ephemeralPool)

| Scenario | What happens | Recovery |
|----------|-------------|----------|
| **Host runs out of RAM** | tmpfs can't allocate pages for CoW writes. VM disk I/O fails, VM likely crashes. | Operator intervention. Reduce `reservedResources` or increase host RAM. The crashed VM can be deleted and recreated by 5-Spot. |
| **Base image pool goes offline** | New VMs can't be provisioned (backing image unreachable). Existing CoW VMs may also fail if the backing chain is broken. | Operator restores the persistent storage pool. |

### Health check behavior

Host health checks only run while machines actively reference the host.
When 5-Spot deletes all machines on a host, health checks stop — no SSH
traffic to idle hosts. When a new machine is created, the host controller
wakes up and resumes checking. The interval is per-host via
`spec.healthCheckIntervalSeconds` (default 300, minimum 30).

### Known Limitations

**No post-ready VM health monitoring.** Once a LibvirtMachine reaches
`ready=true`, CAPLV does not currently recheck the domain state. If the
VM dies after provisioning, `status.ready` remains `true` until the
resource is deleted. Deploy a `MachineHealthCheck` (see example above) to
detect node-level failures and trigger remediation. Without it, dead VMs
leave orphaned `Node` objects and `Terminating` pods in OpenShift.

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
make podman-build IMG=ghcr.io/atgreen/caplv:latest

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
