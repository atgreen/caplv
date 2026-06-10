# Changelog

All notable changes to the CAPLV project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Added
- `LibvirtCluster.spec.bootArtifacts` — opt-in switch from QEMU `fw_cfg` ignition delivery to libvirt direct-kernel-boot plus a virtio-blk ignition disk. Sidesteps the kernel `qemu_fw_cfg` O(n²) read regression (tens of seconds of wall-clock time on multi-MB ignition payloads) and shaves first-boot time accordingly. Kernel/initramfs can be pulled from **HTTPS**, **OCI** (single `oras`-style artifact, layers identified by `org.opencontainers.image.title`), or **S3** (any S3-compatible store: AWS, MinIO, Ceph RGW). Optional `kernelSHA256` / `initramfsSHA256` fields enforce integrity, and OCI/S3 sources accept a `credentialsSecretRef` for private endpoints (`kubernetes.io/dockerconfigjson` or basic-auth secrets for OCI; static-credential secrets for S3). Resolved bytes are cached in-process and content-addressed on each libvirt host so machines in the same cluster reuse the same staged files.
- `LibvirtMachine.spec.rootDisk.ephemeralPoolSize` — optional cap on the per-machine tmpfs storage pool (accepts tmpfs `size=` syntax, e.g. `"80%"`, `"8G"`). Defaults to the kernel's tmpfs default (50% of RAM).
- `LibvirtMachine.spec.nodeLabels` and `LibvirtMachine.spec.nodeAnnotations` — controller-applied Node labels and annotations that bypass the NodeRestriction admission allow-list, so arbitrary keys (e.g. `dynatrace`, `k8s.ovn.org/egress-assignable`) can be set on workers. Owned keys are tracked on the Node via `infrastructure.cluster.x-k8s.io/libvirt-managed-labels` / `-managed-annotations` annotations; admin-set labels are left untouched. Surfaced as the `NodeLabelled` status condition.
- Unified CI/CD pipeline with build, test, lint, Docker build+push, Cosign signing, SBOM generation, Trivy scanning, SLSA provenance, and release asset upload
- Container image signing via Cosign (keyless, Sigstore)
- CycloneDX SBOM generation for container images
- Trivy container security scanning with GitHub Security integration
- Go dependency vulnerability checking via govulncheck
- SLSA Level 3 provenance generation for release artifacts
- GitHub Artifact Attestation for container images
- Release artifacts follow the [CAPI clusterctl provider contract](https://cluster-api.sigs.k8s.io/clusterctl/provider-contract.html): `infrastructure-components.yaml`, `metadata.yaml`, and `cluster-template.yaml` are published as individual top-level release assets (replacing the `deploy-manifests.tar.gz` bundle), so the provider can be consumed directly by `clusterctl init`, `clusterctl generate cluster`, ArgoCD, and other tooling that expects per-file URLs
- Multi-architecture container builds (linux/amd64, linux/arm64)
- Event-driven versioning (semver for releases, date-based for main, pr-NUMBER for PRs)

### Changed
- Consolidated separate CI workflows (test, lint, e2e, image) into a single unified build pipeline
- Container images now pushed to GHCR with consistent tagging across all event types
- Release workflow triggers on `v*` tag push and creates a DRAFT release for the maintainer to review before publishing (matches the CAPI ecosystem convention used by cluster-api-provider-vsphere); the previous flow ran on release-published, which left the release public if any earlier job failed
- `make build-installer` stages `config/` in a build dir before running `kustomize edit set image`, so the checked-in `config/manager/kustomization.yaml` is no longer mutated as a side effect of producing a release manifest locally

### Fixed
- E2E CI failure: set CONTAINER_TOOL=docker for GitHub Actions runners
- All golangci-lint issues (gofmt, modernize, unparam, unused)
