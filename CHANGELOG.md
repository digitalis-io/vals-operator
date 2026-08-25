# Changelog

All notable changes to vals-operator are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Vals-operator uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- New `-disable-namespace-sync` flag to block all cross-namespace `ref+k8s://` references. When enabled, any `ref+k8s://` reference targeting a namespace other than the `ValsSecret`'s own namespace is rejected. Same-namespace references are unaffected. ([#91](https://github.com/digitalis-io/vals-operator/issues/91))
- New `-allowed-namespaces-for-sync` flag to allowlist specific namespaces for cross-namespace `ref+k8s://` access. References targeting namespaces outside the list are rejected. An empty value (the default) permits all namespaces. `-disable-namespace-sync` takes precedence over this flag when both are set. ([#91](https://github.com/digitalis-io/vals-operator/issues/91))
- Helm chart is now published as an OCI artifact to `oci://ghcr.io/digitalis-io/helm-charts/vals-operator` on every release, enabling installation without `helm repo add` on Helm 3.8+. ([#95](https://github.com/digitalis-io/vals-operator/issues/95))
- New `-allowed-backend-paths` flag (Helm value `allowedBackendPaths`) restricts which backend paths each namespace may read. It governs both `ValsSecret` references — every backend, not only `ref+k8s://` — and the mount and role named by a `DbSecret`, so that restricting one resource cannot be sidestepped through the other. Semicolon-separated `namespace=prefix[,prefix...]` entries, where a namespace of `*` applies to all; prefixes match on path-segment boundaries. Denied references are rejected before any backend call and recorded as an event naming the namespace and the rejected path. An empty value (the default) permits all, preserving existing behaviour. Recommended on any cluster where namespaces are not all equally trusted. ([#102](https://github.com/digitalis-io/vals-operator/issues/102))
- `DbSecret` resources now validate `spec.vault.mount`, `spec.vault.role`, `spec.secretName` and `spec.rollout[].kind` at the API level.

### Security

- Container images and the Helm OCI chart are now signed on every release using cosign keyless signing via GitHub Actions OIDC. Consumers can verify signatures without trusting any long-lived key. See README for `cosign verify` commands. ([#98](https://github.com/digitalis-io/vals-operator/issues/98))
- Secret templates in `ValsSecret` and `DbSecret` are no longer rendered with the full sprig function set. The functions that read the operator's process environment or resolve hostnames (`env`, `expandenv`, `getHostByName`) are no longer available to template authors. **Breaking:** a template using one of them now fails with a `function "env" not defined` error; move the value into your secrets backend and reference it from `data` instead.
- Vault and OpenBao leases issued for a `DbSecret` are now revoked on rotation and on deletion. They previously were not, leaving credentials valid until their natural TTL. Leases orphaned before this release are not tracked by the operator and must be revoked in the backend directly — see the upgrade notes in the README.
- A `DbSecret` will no longer write to a Secret it does not own. Because `spec.secretName` is free-form, naming an existing unrelated Secret previously replaced its contents and placed it under the `DbSecret`'s ownership.
- Hardened parsing of lease IDs returned by the secrets backend, and of StatefulSet rollout targets with no pod template annotations. Both could previously crash the operator.
- SPDX 2.3 JSON and CycloneDX 1.5 JSON SBOMs are now generated for every released container image and attached as GitHub Release assets. The SPDX SBOM is additionally recorded as a cosign attestation on the image digest, verifiable with `cosign verify-attestation --type spdxjson`. See README for download and verification commands. ([#99](https://github.com/digitalis-io/vals-operator/issues/99))

### Changed

- Updated all Go module dependencies to latest stable versions; fixed `ENVTEST_K8S_VERSION` and bumped `CONTROLLER_TOOLS_VERSION`. ([#94](https://github.com/digitalis-io/vals-operator/issues/94))
- Pinned all GitHub Actions workflow steps to SHA references. ([#94](https://github.com/digitalis-io/vals-operator/issues/94))
- A `DbSecret` whose Secret could not be written is now retried with backoff instead of being left until its spec changed.
- **Breaking:** `spec.rollout[].kind` on a `DbSecret` is now restricted to `Deployment` and `StatefulSet` at the API level. `Pod` was named in the field documentation but was never implemented — a `DbSecret` using it was accepted and then failed during reconciliation. Such a resource is now rejected by `kubectl apply` instead.

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
