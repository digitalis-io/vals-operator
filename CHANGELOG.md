# Changelog

All notable changes to vals-operator are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Vals-operator uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- New `-disable-namespace-sync` flag to block all cross-namespace `ref+k8s://` references. When enabled, any `ref+k8s://` reference targeting a namespace other than the `ValsSecret`'s own namespace is rejected. Same-namespace references are unaffected. ([#91](https://github.com/digitalis-io/vals-operator/issues/91))
- New `-allowed-namespaces-for-sync` flag to allowlist specific namespaces for cross-namespace `ref+k8s://` access. References targeting namespaces outside the list are rejected. An empty value (the default) permits all namespaces. `-disable-namespace-sync` takes precedence over this flag when both are set. ([#91](https://github.com/digitalis-io/vals-operator/issues/91))

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
