# Changelog

All notable changes to vals-operator are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Vals-operator uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- New `-disable-namespace-sync` flag to block all cross-namespace `ref+k8s://` references. When enabled, any `ref+k8s://` reference targeting a namespace other than the `ValsSecret`'s own namespace is rejected. Same-namespace references are unaffected. ([#91](https://github.com/digitalis-io/vals-operator/issues/91))
- New `-allowed-namespaces-for-sync` flag to allowlist specific namespaces for cross-namespace `ref+k8s://` access. References targeting namespaces outside the list are rejected. An empty value (the default) permits all namespaces. `-disable-namespace-sync` takes precedence over this flag when both are set. ([#91](https://github.com/digitalis-io/vals-operator/issues/91))
- Helm chart is now published as an OCI artifact to `oci://ghcr.io/digitalis-io/helm-charts/vals-operator` on every release, enabling installation without `helm repo add` on Helm 3.8+. ([#95](https://github.com/digitalis-io/vals-operator/issues/95))

### Security

- Container images and the Helm OCI chart are now signed on every release using cosign keyless signing via GitHub Actions OIDC. Consumers can verify signatures without trusting any long-lived key. See README for `cosign verify` commands. ([#98](https://github.com/digitalis-io/vals-operator/issues/98))
- SPDX 2.3 JSON and CycloneDX 1.5 JSON SBOMs are now generated for every released container image and attached as GitHub Release assets. The SPDX SBOM is additionally recorded as a cosign attestation on the image digest, verifiable with `cosign verify-attestation --type spdxjson`. See README for download and verification commands. ([#99](https://github.com/digitalis-io/vals-operator/issues/99))

### Changed

- Updated all Go module dependencies to latest stable versions; fixed `ENVTEST_K8S_VERSION` and bumped `CONTROLLER_TOOLS_VERSION`. ([#94](https://github.com/digitalis-io/vals-operator/issues/94))
- Pinned all GitHub Actions workflow steps to SHA references. ([#94](https://github.com/digitalis-io/vals-operator/issues/94))

## [0.8.1] - 2026-02-10

### Added

- Bump vals and Go dependency versions. ([#93](https://github.com/digitalis-io/vals-operator/pull/93))

## [0.8.0] - 2026-01-19

### Added

- OpenBao backend support alongside HashiCorp Vault. The operator automatically selects the backend based on `BAO_ADDR` or `VAULT_ADDR` environment variables. `BAO_*` variables fall back to `VAULT_*` equivalents for backwards compatibility. ([#92](https://github.com/digitalis-io/vals-operator/pull/92))

### Security

- Upgraded `golang.org/x/oauth2` to address advisory ([#89](https://github.com/digitalis-io/vals-operator/pull/89), [#90](https://github.com/digitalis-io/vals-operator/pull/90))
- Upgraded `golang.org/x/crypto` to address CVE-2024-45337 ([#87](https://github.com/digitalis-io/vals-operator/pull/87))
- Upgraded `golang.org/x/net` to address CVE-2024-45338 ([#88](https://github.com/digitalis-io/vals-operator/pull/88))

## [0.7.12] - 2024-11-22

### Fixed

- Do not trigger a rollout when a secret value was checked but not updated. ([#86](https://github.com/digitalis-io/vals-operator/pull/86))

[Unreleased]: https://github.com/digitalis-io/vals-operator/compare/v0.8.1...HEAD
[0.8.1]: https://github.com/digitalis-io/vals-operator/compare/v0.8.0...v0.8.1
[0.8.0]: https://github.com/digitalis-io/vals-operator/compare/v0.7.12...v0.8.0
[0.7.12]: https://github.com/digitalis-io/vals-operator/compare/v0.7.11...v0.7.12
