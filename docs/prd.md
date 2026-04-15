# CAPLV — Cluster API Provider for LibVirt

## Product Requirements Document

**Version:** 0.3.1
**Date:** 2026-04-15
**Author:** Anthony Green
**Status:** Draft
**License:** Apache-2.0

---

## 1. Problem Statement

Organizations running physical RHEL hosts with KVM/libvirt need a way to
dynamically provision and deprovision virtual machines as Kubernetes worker
nodes — managed through the Cluster API (CAPI) contract. Today, no maintained
CAPI provider exists for libvirt. The archived `cluster-api-provider-libvirt`
(Go, Kubernetes SIGs) was never production-ready and has been abandoned.

Without CAPLV, operators must either:

- Use Metal3 (complex Ironic dependency, additional infrastructure overhead)
- Manually manage VM lifecycle outside of CAPI

All of these add unnecessary complexity or operational burden for environments
where libvirt is the native hypervisor.

## 2. Scope and Constraints

CAPLV is a **worker-only infrastructure provider built exclusively for
5-Spot**. It does not provision clusters or control planes. It attaches
worker VMs to existing clusters on a time-based schedule.

**Supported CAPI topology:** Existing cluster with external control plane.
CAPLV satisfies the CAPI `InfrastructureCluster` contract with a stub that
immediately reports ready. CAPI requires this resource to exist — CAPLV does
not use it to provision infrastructure.

**Static IPs required:** Every `LibvirtMachine` must specify at least one
static IP address in `spec.network.addresses`. CAPLV does not support DHCP.
This aligns with 5-Spot's model where each scheduled machine has a
predetermined network identity.

**Why this constraint exists:** 5-Spot schedules individual machines with
known network identities on/off existing clusters. There is no need for
dynamic IP allocation, MachineDeployment scaling, or template-based
replication.

**Scale model:** Each libvirt host runs exactly one CAPLV-managed worker
VM. However, hundreds or thousands of hosts may come online simultaneously
(e.g., 9am Monday when 5-Spot activates a schedule). CAPLV must process
these in parallel — sequential reconciliation is not acceptable at this
scale. The LibvirtMachine controller runs with configurable concurrent
reconcilers (default: 50) so that machines targeting different hosts are
provisioned simultaneously. Since each host runs one VM, there are no
contention issues — each reconcile operates against a different libvirt
host over its own SSH connection.

**Ephemeral storage model:** VMs managed by CAPLV are ephemeral — created
in the morning, destroyed in the evening. To avoid unnecessary disk I/O,
operators can configure a tmpfs-backed libvirt storage pool for VM disks
and ISOs. The `spec.rootDisk.baseImagePool` field allows the base image
(read-only, persistent) to live in a separate pool from the ephemeral
CoW overlay and bootstrap ISO. This keeps base images on persistent
storage while all per-VM artifacts live entirely in RAM.

## 3. Target Users

| User | Need |
|------|------|
| **5-Spot operator** (time-based machine scheduler) | Create and destroy worker VMs on a schedule via standard CAPI resources |
| **Platform engineers** operating physical RHEL/KVM infrastructure | Provision VMs as CAPI machines without additional infrastructure overhead |
| **OpenShift administrators** | Add/remove KVM-based worker nodes to OCP clusters dynamically via 5-Spot |

## 4. Goals

1. Implement a CAPI-compliant infrastructure provider for libvirt/KVM
2. Enable 5-Spot to schedule KVM virtual machines via standard CAPI resources
3. Support OpenShift bootstrap via the current RHCOS installer flow and
   generic Kubernetes bootstrap via cloud-init
4. Provide secure, production-grade connectivity to remote libvirt hosts
5. Keep the provider minimal and focused — no scheduler, no image registry

## 5. Non-Goals

- **VM placement scheduling** — the user specifies which host runs the VM
- **Image management / registry** — images are pre-staged on hosts
- **Storage orchestration** — uses libvirt storage pools as-is
- **Network orchestration** — uses existing libvirt networks or host bridges
- **Control plane provisioning** — CAPLV manages worker nodes only
- **Live migration** — VMs are ephemeral, destroyed and recreated by 5-Spot
- **Multi-tenancy** — single trust domain assumed
- **MachineDeployment / MachineSet** — CAPLV targets individual machines
  managed by 5-Spot, not replica-based scaling
- **DHCP** — all VMs require static IP configuration
- **Bootstrap payload mutation** — CAPLV does not modify bootstrap data (see
  section 10)

## 6. Custom Resource Definitions

### 6.1 LibvirtHost

Reusable host connection details, referenced by `LibvirtMachine` resources.
Extracting host configuration avoids duplicating URI and credentials across
every machine spec and simplifies credential rotation.

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtHost
metadata:
  name: rhel-host-01
  namespace: default
spec:
  uri: "qemu+ssh://root@rhel-host-01.example.com/system"
  secretRef:
    name: rhel-host-01-ssh-key
    namespace: default
  # SSH host key fingerprint for verification (required for SSH connections)
  hostKeyFingerprint: "SHA256:abc123..."
status:
  ready: false
  lastChecked: "2026-04-14T10:00:00Z"
  conditions:
    - type: Reachable
      status: "True"
      reason: "ConnectionSucceeded"
      message: "libvirtd accessible and authorized"
```

**Behavior:** The controller periodically validates connectivity to the
libvirt host and updates `status.ready`. Machines referencing an unreachable
host will not be provisioned.

### 6.2 LibvirtCluster

Satisfies the CAPI `InfrastructureCluster` contract requirement. CAPLV does
not provision cluster infrastructure — this is a pass-through.

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtCluster
metadata:
  name: my-cluster
spec:
  controlPlaneEndpoint:
    host: "api.my-cluster.example.com"
    port: 6443
status:
  ready: true
```

**Behavior:** The controller sets `status.ready: true` immediately. CAPI
requires this resource to exist for the `Cluster` object to proceed. No
infrastructure is created or managed.

### 6.3 LibvirtMachine

The core resource. Represents a single KVM virtual machine on a specific
libvirt host.

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: LibvirtMachine
metadata:
  name: worker-01
  namespace: default
spec:
  # Reference to a LibvirtHost (connection details)
  hostRef:
    name: rhel-host-01

  # VM specifications
  domain:
    vcpus: 4
    memoryMB: 8192
    machine: "q35"       # optional, default: q35
    firmware: "uefi"     # optional: bios | uefi, default: uefi

  # Storage
  rootDisk:
    size: "100Gi"
    storagePool: "default"
    baseImage: "rhcos-4.14.qcow2"   # must exist in the storage pool
    bus: "virtio"                    # optional, default: virtio
    cloneStrategy: "copy-on-write"  # copy-on-write | full-clone, default: copy-on-write

  additionalDisks: []               # optional, same schema as rootDisk minus baseImage

  # Networking
  network:
    type: "bridge"                  # bridge | network
    name: "br0"                     # bridge name or libvirt network name
    model: "virtio"                 # optional, default: virtio
    addresses:                        # REQUIRED — at least one static IP in CIDR notation
      - "192.168.1.50/24"
    gateway: "192.168.1.1"          # default gateway
    dns:
      nameservers:
        - "192.168.1.1"
        - "8.8.8.8"
      searchDomains:                # optional
        - "example.com"
    macAddress: "52:54:00:ab:cd:01" # optional, auto-generated if omitted

  # Bootstrap
  bootstrapFormat: "ignition"       # ignition | cloud-init

status:
  ready: false
  infrastructureReady: false        # VM running with correct hypervisor-level config
  addresses:
    - type: InternalIP
      address: "192.168.1.50"
  providerID: "libvirt:///rhel-host-01/default-my-cluster-worker-01"
  domainUUID: "a1b2c3d4-..."
  domainState: "running"
  # Artifacts created by CAPLV — only these are cleaned up on deletion
  # Names are deterministic: <namespace>-<cluster>-<machine> for collision safety
  managedArtifacts:
    domainName: "default-my-cluster-worker-01"
    rootDiskVolume: "default-my-cluster-worker-01-root.qcow2"
    bootstrapISO: "default-my-cluster-worker-01-bootstrap.iso"
    additionalDiskVolumes: []
  failureReason: ""                 # machine-level terminal failure
  failureMessage: ""
  conditions:
    - type: InfrastructureReady     # VM defined, started, network attached
      status: "True"
    - type: HostReachable           # libvirt host is accessible
      status: "True"
    - type: ArtifactsCreated        # all disk/ISO artifacts exist
      status: "True"
```

**All `spec` fields are immutable after creation.** The admission webhook
rejects any update that changes `spec`. To change VM configuration, delete
the `LibvirtMachine` and create a new one. This is consistent with how CAPI
infrastructure providers work — the `Machine` is the unit of replacement,
not in-place update.

**Why fully immutable:** CAPLV never modifies a running domain's XML (section
8.2). Allowing some fields to appear mutable while silently ignoring changes
would mislead users. Making the entire spec immutable is honest and simple.

## 7. CAPI Contract Compliance

CAPLV must satisfy the CAPI infrastructure provider contract:

| Requirement | Implementation |
|-------------|---------------|
| `InfrastructureCluster` with `status.ready` | `LibvirtCluster` — sets ready immediately (pass-through) |
| `InfrastructureMachine` with `status.ready` | `LibvirtMachine` — set when VM domain is running with correct hypervisor-level config (does not verify guest OS networking — see note below) |
| `status.addresses` | Populated from `spec.network.addresses` (user-provided static IP) |
| `providerID` | Format: `libvirt:///<host-name>/<domain-name>` — immutable after first set |
| Bootstrap data consumption | Read from `Machine.spec.bootstrap.dataSecretName`, passed through unmodified |
| Machine deletion | Explicit artifact cleanup: delete only `status.managedArtifacts` resources |
| Finalizer for cleanup | `infrastructure.cluster.x-k8s.io/libvirt-machine` |
| Owner references | Set by CAPI core, respected by CAPLV |
| `failureReason` / `failureMessage` | Set on terminal failures to signal CAPI to stop retrying |

**ProviderID contract:** The `providerID` is set once when the VM is first
created and never changes. Format is `libvirt:///<libvirthost-name>/<domain-name>`.
It is consumed by the kubelet `--provider-id` flag and must remain stable for
the lifetime of the machine.

**InfrastructureReady semantics:** `status.ready` means the VM domain is
running and the hypervisor-level configuration (CPU, memory, disks, NIC
attachment) is correct. It does **not** mean the guest OS has booted, network
is reachable, or the node has joined the cluster. This matches how other CAPI
providers work — CAPA does not SSH into EC2 instances to verify guest
networking. Guest-level readiness is handled by CAPI's machine health checks
and the bootstrap provider's status reporting.

## 8. Reconciliation State Machine

CAPLV reconciliation is not a simple create/delete. Partial failures during
provisioning produce intermediate states that must be handled idempotently.

### 8.1 Artifact Naming Convention

All libvirt artifacts (domains, volumes, ISOs) use deterministic names
derived from the Kubernetes resource identity:

```
<namespace>-<cluster-name>-<machine-name>
```

Example: machine `worker-01` in namespace `default` for cluster `my-cluster`
produces domain name `default-my-cluster-worker-01`.

**Why deterministic naming matters:** If the controller crashes after creating
a libvirt artifact but before writing `status.managedArtifacts`, the artifact
would be orphaned. With deterministic naming, the controller can rediscover
artifacts by computing the expected name from the resource's metadata — no
status write required. This makes `status.managedArtifacts` a cache for
performance, not the source of truth for cleanup.

### 8.2 Provisioning States

```
Pending
  → HostValidation      (verify LibvirtHost is reachable and authorized)
  → DiskCreation         (clone base image to root disk volume)
  → ISOCreation          (generate bootstrap ISO in-memory, upload to storage pool)
  → DomainDefinition     (define VM domain XML with disks, network, firmware)
  → DomainStart          (start the VM)
  → AddressReporting     (set status.addresses from spec, set providerID)
  → InfrastructureReady  (set status.ready = true)
```

### 8.3 Recovery Invariants

On re-reconcile after a crash or error, the controller computes expected
artifact names from the resource identity and checks libvirt for their
existence. `status.managedArtifacts` is updated to reflect discovered state.

| State | Recovery |
|-------|----------|
| Disk exists, no domain | Skip disk creation, continue from ISOCreation |
| Domain exists, not started | Skip define, start the domain |
| Domain running, no status | Skip lifecycle, update status |
| Domain exists with wrong spec | **Do not update** — set `failureReason: SpecMismatch` |
| Artifact partially created | Clean up partial artifacts, retry from last checkpoint |

**Invariant:** CAPLV never modifies a running domain's XML. Spec is immutable
after creation (enforced by admission webhook).

### 8.4 Deletion States

```
DeletionRequested
  → DomainDestroy        (force power off — skip if already off)
  → DomainUndefine       (remove domain definition only, no --remove-all-storage)
  → ArtifactCleanup      (delete each volume in status.managedArtifacts explicitly)
  → FinalizerRemoval     (remove finalizer, allow k8s garbage collection)
```

**Safety:** Deletion targets artifacts by deterministic name (computed from
resource identity), cross-checked against `status.managedArtifacts`. No
blanket `--remove-all-storage`. If an artifact is missing (already cleaned
up or never created), skip it without error.

**Host unreachable during deletion:** Retry with exponential backoff. The
finalizer is **never removed** until cleanup is confirmed on the host. If
the host remains unreachable, the finalizer stays, the resource stays, and
the condition `CleanupStalled` is set with a message indicating the host
and duration. The operator must manually resolve (fix the host, or manually
clean up and remove the finalizer). CAPLV does not silently leak artifacts.

### 8.6 Error Categories

Errors fall into three categories with different retry behavior:

**Terminal (no retry):** Operator must fix the underlying issue. CAPLV sets
`failureReason` and `failureMessage`, which signals CAPI to stop retrying.
Machine must be deleted and recreated after the issue is resolved.

**Transient (retry with backoff):** Temporary conditions that may resolve.
CAPLV requeues with exponential backoff. A condition is set so the operator
can observe the issue.

**Waiting (watch, no polling):** The resource depends on another resource
that doesn't exist yet. CAPLV sets a condition and relies on controller-runtime
watches to re-trigger when the dependency appears.

### 8.7 Error Cases

| Scenario | Category | Behavior |
|----------|----------|----------|
| Bootstrap secret missing | Waiting | Set condition `BootstrapDataNotReady`; watch for Secret creation |
| Bootstrap secret malformed | Terminal | Set `failureReason: InvalidBootstrapData` |
| VM name collision on host | Terminal | Set `failureReason: DomainAlreadyExists` — deterministic naming makes this a config error |
| Storage pool insufficient capacity | Transient | Set condition `StorageInsufficient`; requeue with backoff |
| Base image missing or corrupted | Terminal | Set `failureReason: BaseImageNotFound` |
| Host reachable but libvirtd unauthorized | Terminal | Set `failureReason: HostUnauthorized` — operator must fix permissions |
| VM boots but never joins cluster | Not CAPLV's responsibility — CAPI machine health checks handle this |
| Guest changes MAC/IP | Not CAPLV's responsibility — CAPLV reports spec IP, not observed IP |
| Immutable field changed on update | Admission webhook rejects the update |
| Machine deleted during creation | Finalizer triggers; cleanup whatever artifacts exist in `managedArtifacts` |
| Controller crashes mid-provisioning | Re-reconcile uses `managedArtifacts` to determine resume point |
| Deletion requested while creation in progress | Finalizer blocks deletion; creation abandons, cleanup begins |
| ISO upload succeeds but domain define fails | Next reconcile sees ISO in `managedArtifacts`, no domain — retries define |
| Host deleted or reprovisioned | `LibvirtHost` status goes not-ready; machines on that host get condition `HostUnreachable` |
| Multiple controllers without leader election | Cannot happen — leader election is Phase 1 (see section 15) |

## 9. Libvirt Connectivity

### 9.1 Supported Connection Methods

Phase 1 supports SSH only. TLS support is deferred.

| Method | URI Format | Use Case | Phase |
|--------|-----------|----------|-------|
| SSH tunnel | `qemu+ssh://user@host/system` | Production — encrypted, key-based auth | 1 |
| TLS | `qemu+tls://host/system` | Production — certificate-based auth | 3 |
| Unix socket | `qemu:///system` | Local development, testing | 1 |

### 9.2 Authentication

- **SSH:** Private key stored in a Kubernetes Secret, referenced by
  `LibvirtHost.spec.secretRef`. The Secret must contain a `ssh-privatekey`
  key. Host key verification is mandatory — the expected fingerprint is
  stored in `LibvirtHost.spec.hostKeyFingerprint`.
- **Connection lifecycle:** Connections are created per-reconcile and closed
  after each operation. No long-lived connection pool. Credentials are read
  from the Secret on each connection attempt — no caching in memory.

**Why no connection pooling:** Pooling requires holding libvirt connections
(and their underlying SSH sessions) open, which means holding credentials
in memory. For a security-sensitive controller managing privileged access to
hypervisors, the per-reconcile connection model is simpler and safer. If
performance becomes an issue at scale, connection pooling with bounded TTL
can be introduced later with explicit credential lifecycle management.

### 9.3 Assumptions

- The management cluster has network reachability to every libvirt host.
- The SSH user in the URI has privileges for domain, storage pool, and
  network operations on libvirtd.
- libvirtd is running and accepting connections on every registered host.
- Guest OS images are pre-staged in the named storage pools by the operator.

## 10. Bootstrap Data Handling

**CAPLV does not modify bootstrap data.** It reads the bootstrap payload from
the CAPI `dataSecretName` Secret and passes it through to the VM unmodified.

### 10.0 OpenShift Bootstrap Positioning

For **modern OpenShift worker nodes**, the preferred bootstrap model is:

- Boot **RHCOS live media** via ISO or PXE
- Pass the worker Ignition to the installer using `coreos-installer`
  semantics such as `coreos.inst.ignition_url`
- Install RHCOS to the target disk, then boot the installed system

This is the current recommended RHCOS/OpenShift pattern. CAPLV's existing
"attach a small Ignition ISO to a pre-cloned root disk" flow remains a
workable compatibility path, but it should be treated as an implementation
shortcut, not the target state-of-the-art design for OpenShift.

For **generic Kubernetes** images that expect cloud-init, an attached NoCloud
ISO remains a normal and acceptable pattern.

### 10.1 Why No Mutation

Mutating opaque bootstrap payloads from different bootstrap providers
(OpenShift MachineConfig, etc.) is fragile and
version-sensitive. Each provider has its own schema, and injecting network
config into someone else's payload creates coupling that breaks silently on
provider upgrades.

### 10.2 Network Configuration Responsibility

Static IP configuration is the **bootstrap provider's responsibility**, not
CAPLV's. The user must ensure their bootstrap config includes the correct
network settings for the VM.

For cloud-init, this means including `network-config` in the bootstrap
provider output. For ignition, this means including NetworkManager keyfiles
in the ignition config or otherwise ensuring the installed RHCOS system has
the required network configuration on first boot.

CAPLV's `spec.network.addresses` field serves two purposes:
1. **Populating `status.addresses`** for CAPI contract compliance
2. **Setting the MAC address** on the VM NIC (if `macAddress` is specified),
   which allows the bootstrap-configured networking to bind to the correct
   interface

### 10.3 Bootstrap Artifact Strategy

CAPLV supports two bootstrap delivery patterns:

- **Cloud-init:** Create a NoCloud ISO with `user-data` and `meta-data`
  files. The `meta-data` contains instance-id and local-hostname derived
  from the machine name.
- **Ignition compatibility path:** Create a small ISO containing the
  Ignition config at `/ignition/config.ign`. This works for FCOS/RHCOS-style
  environments that consume Ignition from attached media, but it should not
  be considered the preferred long-term OpenShift path.

### 10.4 Preferred OpenShift Worker Flow

For OpenShift workers, CAPLV should evolve toward:

1. Attaching or booting an **RHCOS live ISO** (or PXE-equivalent)
2. Supplying installer kernel arguments or equivalent configuration for
   `coreos-installer`
3. Pointing the installer at the cluster-generated worker Ignition
   (`worker.ign` / `worker-user-data-managed`)
4. Installing RHCOS to the cloned VM disk
5. Rebooting into the installed system

This better matches current OpenShift operational guidance than directly
booting a preinstalled disk plus an attached Ignition ISO.

### 10.5 Implementation Note

Phase 1 may continue to use pure-Go ISO creation for simplicity. ISO creation
uses Go's `diskfs` library (pure Go, no CGo) — no external tools like
`genisoimage` required. However, this should be framed as an initial
implementation tradeoff rather than the desired steady-state architecture for
OpenShift worker provisioning.

## 11. Integration with 5-Spot

5-Spot creates CAPI resources with inline specs via `EmbeddedResource`. For
CAPLV integration:

1. Add `infrastructure.cluster.x-k8s.io` to 5-Spot's
   `ALLOWED_INFRASTRUCTURE_API_GROUPS` (already present)
2. The `LibvirtMachine` spec is embedded in the `ScheduledMachine`
   `infrastructure_spec` field
3. 5-Spot creates/deletes `LibvirtMachine` resources on schedule
4. CAPLV handles the actual VM lifecycle
5. The `LibvirtHost` resource is created once per host by the operator —
   5-Spot does not manage it

**Note:** Because 5-Spot targets individual machines (not MachineDeployments),
the static IP + explicit host model works well. Each `ScheduledMachine`
specifies exactly which host and which IP to use.

**Example ScheduledMachine using CAPLV:**

```yaml
apiVersion: 5spot.finos.org/v1alpha1
kind: ScheduledMachine
metadata:
  name: weekday-worker-01
  namespace: default
spec:
  clusterName: my-ocp-cluster
  schedule:
    daysOfWeek: ["mon-fri"]
    hoursOfDay: ["9-17"]
    timezone: "America/Toronto"
    enabled: true
  bootstrapSpec:
    apiVersion: machine.openshift.io/v1beta1
    kind: MachineConfig
    spec:
      config:
        ignition:
          version: "3.2.0"
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
  gracefulShutdownTimeout: "5m"
  nodeDrainTimeout: "5m"
```

## 12. Security Requirements

- **No credentials in CRDs** — SSH keys stored in Kubernetes Secrets only,
  referenced by `LibvirtHost.spec.secretRef`
- **SSH host key verification** — mandatory for SSH connections; fingerprint
  stored in `LibvirtHost.spec.hostKeyFingerprint`
- **RBAC** — controller needs read/write on CAPLV CRDs, read on Secrets
  (namespaced), read on CAPI Machine resources
- **Network security** — SSH connections to libvirt hosts only; no
  unauthenticated `qemu+tcp://` supported
- **Non-root container** — controller runs as non-root with read-only
  filesystem
- **Audit logging** — all VM create/delete operations logged with
  correlation IDs
- **Per-reconcile credentials** — SSH keys read from Secret on each
  connection, not cached in controller memory
- **Blast radius** — each `LibvirtHost` Secret grants access to one host.
  Compromising the controller exposes all hosts it manages. Operators should
  scope controller RBAC to limit which Secret namespaces it can read.

## 13. Observability

### Metrics (Prometheus)

| Metric | Type | Description |
|--------|------|-------------|
| `caplv_machines_total` | GaugeVec | Machines by host and state |
| `caplv_reconciliations_total` | CounterVec | By phase and result (success/error) |
| `caplv_reconciliation_duration_seconds` | Histogram | Reconciliation latency by phase |
| `caplv_vm_provisioning_duration_seconds` | Histogram | Time from create to infrastructure-ready |
| `caplv_vm_operations_total` | CounterVec | By operation (create/delete/start/stop) and result |
| `caplv_libvirt_connection_errors_total` | CounterVec | Connection failures by host |
| `caplv_bootstrap_iso_creation_seconds` | Histogram | ISO generation time |
| `caplv_host_status` | GaugeVec | Host reachability by host name (1=ready, 0=not) |

### Health Endpoints

- `/healthz` — liveness (controller process is running)
- `/readyz` — readiness (controller has valid kubeconfig and leader lease)
- `/metrics` — Prometheus scrape endpoint

### Structured Logging

- JSON format with correlation IDs
- Log VM lifecycle events: create, start, ready, destroy, undefine
- Log libvirt connection events: connect, disconnect, error, per host
- Log artifact lifecycle: disk created, ISO uploaded, artifact deleted

## 14. Implementation Language

**Go**, using:

- `sigs.k8s.io/controller-runtime` — Kubernetes controller framework
- `sigs.k8s.io/cluster-api` — CAPI types, contracts, and test framework
- `kubebuilder` — project scaffolding and code generation
- `libvirt.org/go/libvirt` — official libvirt Go bindings (libvirt-go-module)
- `k8s.io/client-go` — Kubernetes client
- `github.com/diskfs/go-diskfs` — pure Go ISO9660 generation (no CGo for ISO creation)
- `github.com/onsi/ginkgo/v2` + `gomega` — BDD testing (CAPI convention)

### Build Considerations

**CGo dependency:** `libvirt-go-module` requires CGo and links against
`libvirt-dev` C libraries. This means:

- **Build image** must include `libvirt-dev` headers and `gcc`
- **Runtime image** must include `libvirt-libs` (shared libraries)
- **Base image:** Use `registry.access.redhat.com/ubi9-minimal` with
  `libvirt-libs` installed, not scratch/distroless
- **Cross-compilation** is not possible with CGo — build natively for target
  arch or use multi-arch CI runners
- **CVE patching:** `libvirt-libs` in the runtime image must be kept current

**Alternative under evaluation:** If CGo proves too burdensome, CAPLV could
shell out to `virsh` over SSH instead of using the Go bindings. This
eliminates the CGo dependency entirely at the cost of parsing CLI output.
Decision deferred to implementation.

## 15. Development Phases

### Phase 1: Minimal Viable Provider

- `LibvirtHost` CRD with connectivity validation
- `LibvirtCluster` stub (sets ready immediately)
- `LibvirtMachine` controller with:
  - SSH connectivity to libvirt hosts (per-reconcile, no pooling)
  - VM define/start/destroy/undefine
  - Root disk cloning from base image (copy-on-write)
  - Bootstrap artifact creation
    - Cloud-init NoCloud ISO
    - Ignition ISO compatibility path for early OpenShift support
  - UEFI firmware with OVMF support
    - Configurable firmware path override on `LibvirtHost`
    - NVRAM template handling for per-VM NVRAM copies
  - Static IP address reporting from spec
  - Reconciliation state machine with artifact tracking
  - Finalizer cleanup with explicit artifact deletion
  - Status conditions: `InfrastructureReady`, `HostReachable`, `ArtifactsCreated`
- Validating admission webhook (immutable spec, required static IP)
- OpenShift worker join validation
  - Machine approver, CSR handling, kubelet certs
  - Document OpenShift-specific prerequisites
- Concurrent reconciliation (configurable, default 50 workers) for
  parallel provisioning across hundreds/thousands of hosts
- Leader election via controller-runtime (kubebuilder default)
- CRD generation and deployment manifests
- Unit and integration tests (envtest)

**Phase 1 note:** OpenShift support may initially use an attached Ignition
ISO for expedience, but the preferred follow-on design is RHCOS live
installer boot with `coreos-installer` semantics.

### Phase 1.5: OpenShift Bootstrap Alignment

- Boot OpenShift workers via RHCOS live ISO or PXE semantics
- Pass worker Ignition using `coreos-installer` / `coreos.inst.ignition_url`
- Install to the target disk, then reboot into the installed system
- Keep cloud-init ISO support unchanged for non-OpenShift guests

### Phase 2: Production Hardening

- TLS libvirt connectivity (`qemu+tls://`)
  - Client cert rotation via Secret watch
- Prometheus metrics and ServiceMonitor
- Connection pooling with bounded TTL (opt-in, explicit credential lifecycle)
- Documentation site

### Phase 3: Advanced Features

- Additional disks support
- Multiple network interfaces
- GPU/PCI passthrough configuration
- Resource capacity reporting per host

## 16. Testing Strategy

| Level | Scope | Tools |
|-------|-------|-------|
| **Unit** | CRD validation, domain XML generation, ISO creation, status mapping, artifact tracking | `go test`, `testify` or `gomega` |
| **Integration** | Controller reconciliation with mock libvirt | `envtest` (controller-runtime), CAPI test framework |
| **E2E** | Full VM lifecycle on real libvirt host | Dedicated CI host with libvirt, `ginkgo` |

**E2E CI strategy:** E2E tests require a real libvirt host (nested
virtualization is unreliable for CI). Use a dedicated bare-metal CI runner
with libvirt installed. Tests create/destroy VMs in an isolated storage pool
and network. E2E runs on merge to main, not on every PR.

## 17. Success Criteria

1. A `LibvirtMachine` resource, when created, provisions a running KVM VM
   within 120 seconds (excluding image clone time)
2. The VM's IP address is correctly reported in `status.addresses`
3. CAPI core successfully links the VM to a `Machine` resource
4. 5-Spot can create and delete `LibvirtMachine` resources on schedule
5. Machine deletion cleanly removes only CAPLV-managed artifacts
6. The controller recovers from crashes at any point in the provisioning
   state machine without leaving orphaned resources
7. The controller handles libvirt host unavailability gracefully (conditions,
   backoff, no data loss)

## 18. Open Questions

1. ~~**Guest agent vs DHCP leases for IP detection?**~~ — **Resolved:** IP is
   user-provided in `spec.network.addresses`. The controller sets
   `status.addresses` from the spec. Network configuration is the bootstrap
   provider's responsibility.
2. **Should CAPLV support multiple network interfaces per VM?** — Deferred to
   Phase 4, but the CRD should be designed to allow it (change `network` to
   `networks` array in a future API version).
3. ~~**Copy-on-write vs full clone for root disk?**~~ — **Resolved:** Default
   to copy-on-write. Full clone available as `cloneStrategy: full-clone`.
   CoW creates a dependency on the base image — operators must not delete
   base images while VMs using them exist.
4. ~~**Should the controller run on libvirt hosts or remotely?**~~ —
   **Resolved:** Remote, in the management cluster. This is the standard
   CAPI model. SSH key management is handled via `LibvirtHost` resources
   and Kubernetes Secrets.
5. **CGo vs virsh-over-SSH?** — The `libvirt-go-module` bindings require CGo
   and complicate the container build. Shelling out to `virsh` over SSH
   eliminates CGo but requires parsing CLI output. Evaluate during Phase 1
   implementation.
6. ~~**IPAM for MachineDeployment?**~~ — **Resolved:** CAPLV is exclusively
   for 5-Spot, which uses individual machines with static IPs. No
   MachineDeployment, MachineSet, or MachineTemplate support needed.

---

*This document incorporates two rounds of independent code review (Codex,
2026-04-14). Version 0.2.0 addressed: artifact tracking for safe deletion,
reconciliation state machine, LibvirtHost extraction, bootstrap
pass-through design, and CGo build considerations. Version 0.3.0 addresses:
deterministic artifact naming for crash-safe recovery, fully immutable spec,
coherent error categories (terminal/transient/waiting), finalizer never
removed without confirmed cleanup, InfrastructureReady semantics documented,
full-clone contradiction resolved, MachineTemplate scope clarified, and
OpenShift+UEFI+ignition promoted to Phase 1.*
