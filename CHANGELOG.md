# Changelog

All notable changes to the CAPLV project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Added
- Unified CI/CD pipeline with build, test, lint, Docker build+push, Cosign signing, SBOM generation, Trivy scanning, SLSA provenance, and release asset upload
- Container image signing via Cosign (keyless, Sigstore)
- CycloneDX SBOM generation for container images
- Trivy container security scanning with GitHub Security integration
- Go dependency vulnerability checking via govulncheck
- SLSA Level 3 provenance generation for release artifacts
- GitHub Artifact Attestation for container images
- Deploy manifest packaging for releases (CRDs + kustomize output)
- Multi-architecture container builds (linux/amd64, linux/arm64)
- Event-driven versioning (semver for releases, date-based for main, pr-NUMBER for PRs)

### Changed
- Consolidated separate CI workflows (test, lint, e2e, image) into a single unified build pipeline
- Container images now pushed to GHCR with consistent tagging across all event types

### Fixed
- E2E CI failure: set CONTAINER_TOOL=docker for GitHub Actions runners
- All golangci-lint issues (gofmt, modernize, unparam, unused)
