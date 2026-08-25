# Installation

Vals-operator is installed with Helm. The chart creates the operator Deployment, its
ServiceAccount and RBAC, and (by default) the `ValsSecret` and `DbSecret` CRDs.

Requirements: Kubernetes >= 1.19, Helm >= 3.

## Install from the OCI registry (Helm 3.8+)

The chart is published as an OCI artifact on every release. This is the recommended
method — no `helm repo add` step is required.

```sh
helm install vals-operator oci://ghcr.io/digitalis-io/helm-charts/vals-operator \
  --create-namespace -n vals-operator --version <version>
```

To upgrade:

```sh
helm upgrade vals-operator oci://ghcr.io/digitalis-io/helm-charts/vals-operator \
  --version <version>
```

## Install from the Helm repository

The traditional Helm repository remains available. Consumers on Helm 3.7 or earlier
must use this method.

```sh
helm repo add digitalis https://digitalis-io.github.io/helm-charts
helm repo update
helm install vals-operator digitalis/vals-operator --create-namespace -n vals-operator
```

## Configuring a backend

The operator needs credentials for at least one secrets backend before it can do
anything. See [Secrets backends](backends.md).

## CRDs

`manageCrds: true` (the default) renders the CRDs as part of the chart. Set it to
`false` when something else owns them — a cluster-scoped GitOps process, or a cluster
admin applying them out of band — and apply
[`charts/vals-operator/crds/`](../charts/vals-operator/crds) yourself:

```sh
helm upgrade --install vals-operator digitalis/vals-operator --set manageCrds=false
```

`enableDbSecrets: false` omits the `DbSecret` CRD and the wider RBAC permissions that
controller requires. Use it if you only sync static secrets.

## Verifying signatures

Container images and the Helm OCI chart are signed on every release using
[cosign](https://github.com/sigstore/cosign) keyless signing via GitHub Actions OIDC.
No long-lived signing key is used — verification trusts only the Sigstore public
infrastructure and the workflow identity that produced the artifact.

Install cosign with `brew install cosign` or grab a binary from the
[Sigstore releases page](https://github.com/sigstore/cosign/releases).

> **Important:** Always verify by an immutable reference — the version tag (`vX.Y.Z`)
> or, preferably, the image digest (`@sha256:...`). The mutable `:latest` tag is not a
> stable signing target.

Verify the container image (substitute the release tag):

```sh
cosign verify ghcr.io/digitalis-io/vals-operator:<TAG> \
  --certificate-identity-regexp "^https://github\.com/digitalis-io/vals-operator/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  | jq .
```

Verify the Helm OCI chart (substitute the chart version, no leading `v`):

```sh
cosign verify ghcr.io/digitalis-io/helm-charts/vals-operator:<CHART_VERSION> \
  --certificate-identity-regexp "^https://github\.com/digitalis-io/vals-operator/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  | jq .
```

## Software Bill of Materials

Every released container image ships with an SPDX 2.3 JSON and a CycloneDX 1.5 JSON
SBOM. Both are attached as assets to the corresponding GitHub Release, and the SPDX
SBOM is additionally recorded as a cosign attestation on the image digest.

Download from the GitHub Release:

```
https://github.com/digitalis-io/vals-operator/releases/download/<TAG>/vals-operator-<TAG>-sbom.spdx.json
https://github.com/digitalis-io/vals-operator/releases/download/<TAG>/vals-operator-<TAG>-sbom.cdx.json
```

Verify the SBOM attestation against the image digest:

```sh
cosign verify-attestation \
  --type spdxjson \
  --certificate-identity-regexp "^https://github\.com/digitalis-io/vals-operator/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/digitalis-io/vals-operator@<DIGEST> \
  | jq '.payload | @base64d | fromjson'
```

## Next steps

- [Secrets backends](backends.md) — point the operator at Vault, OpenBao, AWS, GCP or another store.
- [Usage](usage.md) — create your first `ValsSecret`.
- [Configuration reference](configuration.md) — every operator flag and Helm value.
- [Upgrade notes](upgrading.md) — read before upgrading an existing deployment.
