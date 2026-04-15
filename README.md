# CAPLV — *EXPERIMENTAL* Cluster API Provider for LibVirt

CAPLV is an *EXPERIMENTAL* [Cluster API](https://cluster-api.sigs.k8s.io/)
infrastructure provider that provisions KVM virtual machines on libvirt
hosts. It is built exclusively for
[5-Spot](https://github.com/RBC/5-spot), which schedules OpenShift worker
nodes on and off physical RHEL/KVM infrastructure based on time-of-day
rules.

The target hosts are machines with incumbent workloads — DR standby
systems or servers with predictable load schedules that have idle
capacity during off-peak periods. CAPLV is designed to be minimally
disruptive to these hosts: worker-node VMs are fully ephemeral and,
when backed by a tmpfs storage pool, won't touch persistent storage
on the device at all.

## How It Works

```
5-Spot: schedule becomes active
  → Creates CAPI Machine + CAPLV LibvirtMachine
    → CAPLV connects to libvirt host over SSH
      → Clones RHCOS base image, writes ignition config
        → Defines and starts KVM domain
          → VM boots, joins this OpenShift cluster as worker

5-Spot: schedule becomes inactive
  → Deletes CAPI Machine
    → CAPI drains pods, deletes the Node object
      → CAPLV destroys domain, cleans up all artifacts
```

CAPLV runs on the same OpenShift cluster that the worker VMs join.

Each libvirt host runs exactly one CAPLV-managed VM — enforced by an
admission webhook that rejects a second machine targeting the same host.
Hundreds or thousands of hosts can come online simultaneously — the
controller runs up to 50 concurrent reconcilers by default
(`--max-concurrent-reconciles`), each operating against a different host
over its own SSH connection.

## Cluster Setup

Before creating any ScheduledMachine resources, the OpenShift cluster
needs these one-time prerequisites:

**1. Install CAPI, 5-Spot, and CAPLV** on the OpenShift cluster.

**2. Create a CAPI Cluster resource** pointing to itself:

```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: Cluster
metadata:
  name: my-ocp-cluster
  namespace: default
spec:
  controlPlaneEndpoint:
    host: "api.my-ocp-cluster.example.com"
    port: 6443
  infrastructureRef:
    apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
    kind: LibvirtCluster
    name: my-ocp-cluster
    namespace: default
```

**3. Create a LibvirtCluster resource** (CAPLV verifies the endpoint is
reachable before reporting ready):

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtCluster
metadata:
  name: my-ocp-cluster
  namespace: default
spec:
  controlPlaneEndpoint:
    host: "api.my-ocp-cluster.example.com"
    port: 6443
```

**4. Register libvirt hosts** (one resource per physical host):

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtHost
metadata:
  name: rhel-host-01
spec:
  uri: "qemu+ssh://caplv@rhel-host-01.example.com/system"
  secretRef:
    name: rhel-host-01-ssh-key
  hostKeyFingerprint: "SHA256:abc123..."
  reservedResources:             # leave this much for the incumbent workload
    vcpus: 2                     # default: 2
    memoryMB: 4096               # default: 4096
  healthCheckIntervalSeconds: 300  # default: 300 (5 min), only while machines exist
```

The controller discovers total host capacity via `virsh nodeinfo` and
reports available resources (total minus reserved) in
`status.capacity.availableVCPUs` / `status.capacity.availableMemoryMB`.

**5. Create a service account on each host** with minimal privileges:

```bash
# Create the caplv user with libvirt group membership (no login shell).
useradd -r -s /sbin/nologin -G libvirt caplv
mkdir -p /home/caplv/.ssh
# Deploy the SSH public key matching the Kubernetes Secret.
cat > /home/caplv/.ssh/authorized_keys <<< "<public-key>"
chmod 700 /home/caplv/.ssh
chmod 600 /home/caplv/.ssh/authorized_keys
chown -R caplv:caplv /home/caplv/.ssh
```

Grant restricted sudo for tmpfs and file operations only:

```
# /etc/sudoers.d/caplv
caplv ALL=(root) NOPASSWD: /usr/bin/mkdir -p /run/caplv/*
caplv ALL=(root) NOPASSWD: /usr/bin/mount -t tmpfs tmpfs /run/caplv/*
caplv ALL=(root) NOPASSWD: /usr/bin/umount /run/caplv/*
caplv ALL=(root) NOPASSWD: /usr/bin/rmdir /run/caplv/*
caplv ALL=(root) NOPASSWD: /usr/bin/tee /run/caplv/*
caplv ALL=(root) NOPASSWD: /usr/bin/rm -f /run/caplv/*
```

The `libvirt` group grants access to `virsh` commands against
`qemu:///system` without sudo. Only tmpfs mount/unmount and file writes
under `/run/caplv/` require elevated privileges.

**6. Pre-stage RHCOS base images** on each libvirt host in the persistent
storage pool (e.g., `/var/lib/libvirt/images/rhcos-4.14.qcow2`).

**7. Deploy a MachineHealthCheck** (recommended — see below).

## Custom Resources

| CRD | Purpose |
|-----|---------|
| **LibvirtHost** | Reusable connection details for a libvirt hypervisor (URI, SSH key, OVMF paths, reserved resources). Discovers host capacity via `virsh nodeinfo`. Health-checked only while machines are active. |
| **LibvirtCluster** | CAPI contract resource — verifies the control plane endpoint is reachable (TCP dial) before reporting ready. No infrastructure provisioned. |
| **LibvirtMachine** | Core resource — a single KVM VM with static IP, UEFI firmware, and ignition or cloud-init bootstrap. VM size can be auto-derived from host capacity. One per host (webhook-enforced). |

## Example

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
      domain: {}                   # auto-sized from host capacity minus reserved
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
`virsh destroy`), the `Node` object lingers in `NotReady` state
indefinitely. A CAPI `MachineHealthCheck` detects this and triggers
remediation — deleting the orphaned Machine and its stale Node.

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
      timeout: 5m           # node was Ready, now isn't — remediate after 5 min
    - type: Ready
      status: "Unknown"
      timeout: 5m           # node stopped reporting — remediate after 5 min
  nodeStartupTimeout: 10m   # time for a new VM to boot and join the cluster
```

**Tuning `nodeStartupTimeout`:** This is how long CAPI waits for a newly
provisioned VM to boot, run ignition, start kubelet, get its CSR approved,
and report `Ready`. Typical CAPLV startup is 2-4 minutes (VM provision
~30s, RHCOS boot ~60s, ignition + kubelet ~60s, CSR approval ~30s). The
10-minute default gives comfortable headroom. Increase to 15m+ if your
hosts are slow, base images are large, or the network path to the API
server has high latency.

This is the safety net for the known limitation that CAPLV does not
monitor VM health after provisioning. Without it, dead VMs leave orphaned
Node objects in the cluster.

## Key Design Decisions

- **virsh-over-SSH** — pure Go binary (`CGO_ENABLED=0`), distroless container.
  No `libvirt-dev`, no CGo. The `Client` interface allows swapping to native
  libvirt bindings later.
- **One VM per host** — enforced by admission webhook. Each LibvirtHost runs
  at most one CAPLV-managed VM. No contention, no resource accounting complexity.
- **Auto-sizing** — omit `vcpus` and `memoryMB` and CAPLV sizes the VM from
  the host's available capacity (total minus `reservedResources`). Hosts with
  varying specs just work.
- **Static IPs required** — every VM must declare its network identity upfront.
  No DHCP. This matches 5-Spot's model of predetermined machine configurations.
- **Control plane health gate** — LibvirtCluster verifies the worker-facing
  API endpoint is reachable (TCP dial, 60s recheck) before allowing machine
  provisioning. Since CAPLV runs on the same cluster, this catches the case
  where the internal API works but the external endpoint (that workers use
  to join) is down.
- **Parallel at scale** — 50 concurrent reconcilers by default. Each host runs
  one VM, so there are no contention issues across parallel provisions.
- **Ephemeral storage** — `ephemeralPool: true` creates a per-machine tmpfs
  mount and libvirt pool on demand, destroys both on cleanup. No host setup
  required, no persistent storage touched, RAM reclaimed when the VM goes away.
- **Bootstrap pass-through** — CAPLV does not modify ignition or cloud-init data.
  Network configuration is the bootstrap provider's responsibility. For ignition
  (OpenShift/RHCOS), the config is delivered via QEMU `fw_cfg` — the standard
  libvirt method, no ISO needed. For cloud-init, a NoCloud ISO is created.
- **Deterministic artifact naming** — domain names, disk volumes, and ISOs are
  named `<namespace>-<cluster>-<machine>`. Artifacts can be rediscovered after a
  controller crash without relying on status writes.
- **Immutable spec** — all spec fields are frozen after creation. To change VM
  configuration, delete and recreate. Enforced by admission webhook.
- **Finalizer safety** — the finalizer is never removed until cleanup is confirmed
  on the libvirt host. If the host is unreachable, the resource stays and a
  `CleanupStalled` condition is surfaced for operator intervention.

## Host Security Model

CAPLV connects to each libvirt host over SSH using a dedicated `caplv`
service account. The account is designed with minimal privileges:

| Operation | Privilege | Mechanism |
|-----------|-----------|-----------|
| `virsh define/start/destroy/undefine` | libvirt group | Group membership grants `qemu:///system` access — no sudo |
| `virsh vol-create-as/vol-upload/vol-delete` | libvirt group | Same — storage pool operations via libvirt API |
| `virsh nodeinfo/dominfo/version` | libvirt group | Read-only host and domain queries |
| `mkdir -p /run/caplv/*` | root | `sudo` — create tmpfs mount point |
| `mount -t tmpfs tmpfs /run/caplv/*` | root | `sudo` — mount ephemeral storage |
| `umount /run/caplv/*` | root | `sudo` — unmount on cleanup |
| `rmdir /run/caplv/*` | root | `sudo` — remove mount point |
| `tee /run/caplv/*` | root | `sudo` — write ignition config |
| `rm -f /run/caplv/*` | root | `sudo` — delete ignition config |
| `cat > /tmp/caplv-*` | caplv | No sudo — `/tmp/` is world-writable (temp files for virsh define/vol-upload) |

All sudo rules are restricted to paths under `/run/caplv/`. The service
account cannot escalate beyond these specific commands.

## Host Storage Layout (with ephemeralPool: true)

```
/var/lib/libvirt/images/                    (persistent pool "default")
  └── rhcos-4.14.qcow2                     ← pre-staged by operator (read-only)

/run/caplv/ns-cluster-worker01/             (tmpfs, CAPLV creates/destroys)
  ├── ns-cluster-worker01-root.qcow2       ← CoW overlay (in RAM)
  └── ignition.json                        ← ignition config (delivered via fw_cfg)

/var/lib/libvirt/qemu/nvram/
  └── ns-cluster-worker01_VARS.fd           ← UEFI NVRAM (libvirt manages)
```

The tmpfs mount, libvirt pool, and ignition file are created when the VM
is provisioned and destroyed when the VM is deleted. No permanent host
storage impact. For cloud-init guests, a NoCloud ISO replaces the
ignition file.

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
| **VM boots but never joins cluster** | Not CAPLV's responsibility. The node will sit in `NotReady` state. | CAPI MachineHealthCheck handles this (`nodeStartupTimeout`). The bootstrap provider and machine approver must be functional. |

### Controller failures

| Scenario | What happens | Recovery |
|----------|-------------|----------|
| **Controller crashes mid-provisioning** | Partially created artifacts may exist on the host. | Automatic. Deterministic artifact naming allows the controller to discover existing artifacts and resume from where it left off. |
| **Controller crashes mid-deletion** | Finalizer remains on the resource, blocking garbage collection. | Automatic. On restart, the controller retries cleanup. Idempotent delete operations (not-found errors ignored) ensure safe retry. |
| **Leader election lost** | New leader picks up all pending reconciles. | Automatic. All operations are idempotent. |

### Control plane failures

Since CAPLV runs on the same cluster, control plane issues directly
affect CAPLV's ability to operate.

| Scenario | What happens | Recovery |
|----------|-------------|----------|
| **OpenShift API fully down** | CAPLV cannot reconcile at all — it runs on this cluster. No new machines are created. Already-running VMs continue to serve workloads but cannot be managed (no drain, no deletion). | API recovers, CAPLV resumes reconciliation automatically. |
| **API reachable internally but not from worker endpoint** | CAPLV can reconcile, but the LibvirtCluster TCP dial against the external endpoint fails. Status goes `ready=false` with condition `ControlPlaneUnreachable`. CAPI blocks new machine creation. Already-running VMs are unaffected. | External endpoint restored, LibvirtCluster rechecks every 60s. |
| **Cluster under resource pressure** | CAPLV pods may be evicted or throttled. Reconciliation slows. If 5-Spot activates a large schedule during a stressed period, hundreds of concurrent reconciles add API server and etcd load. | CAPLV pods restart via Deployment. Consider PodDisruptionBudget and resource requests to keep CAPLV running during pressure. |
| **etcd degradation** | Status patches and Machine updates slow down. All concurrent reconciles compete for etcd bandwidth. | etcd recovers, backlogged reconciles drain. |
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

- An OpenShift cluster (CAPLV runs on the same cluster that workers join)
- CAPI installed on the cluster
- libvirt hosts reachable over SSH from the cluster network
- RHCOS qcow2 base images pre-staged in libvirt storage pools on each host
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
  iso/                 Cloud-init NoCloud ISO creation (pure Go, go-diskfs)
  ssh/                 SSH client with host key verification
  scope/               MachineScope — gathers reconciliation context, artifact naming
  webhook/             Admission webhook (immutable spec, required static IP, one VM per host)
config/                Kubernetes manifests (CRDs, RBAC, deployment, webhook)
```

## License

Copyright 2026 Anthony Green.

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.
