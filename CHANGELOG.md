# Changelog

All notable changes to this project are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Helm chart is now published as an OCI artifact to `oci://ghcr.io/digitalis-io/helm-charts/vals-operator` on every release, enabling installation without `helm repo add` on Helm 3.8+. ([#95](https://github.com/digitalis-io/vals-operator/issues/95))

### Changed

- Updated all Go module dependencies to latest stable versions; fixed `ENVTEST_K8S_VERSION` and bumped `CONTROLLER_TOOLS_VERSION`. ([#94](https://github.com/digitalis-io/vals-operator/issues/94))
- Pinned all GitHub Actions workflow steps to SHA references. ([#94](https://github.com/digitalis-io/vals-operator/issues/94))

---

[Unreleased]: https://github.com/digitalis-io/vals-operator/compare/v0.8.0...HEAD
