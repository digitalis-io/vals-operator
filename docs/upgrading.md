# Upgrade notes

Behaviour changes that need action when upgrading an existing deployment. See
[CHANGELOG.md](../CHANGELOG.md) for the full history.

## Current release

Four changes need action.

### Providers and reference format

`ValsSecret` references using `echo`, `envsubst`, `exec`, `file`, `gitlab`, `httpjson`, `keychain`, `sops`, `tfstate`, `tfstateazurerm`, `tfstategitlab`, `tfstategs`, `tfstateremote`, `tfstates3` are now rejected with a `Denied` event.
They read from the operator's own pod, including its ServiceAccount token, or fetch
from a host named in the reference. To keep using one, list it in
`-allowed-extra-providers` (Helm value `allowedExtraProviders`). A provider not
listed in [Security](security.md#providers) is rejected.

A `ref` must now hold a single reference with no text around it, and may only use the
query parameters listed in [Security](security.md#reference-format). A `ref` that
combines several references, or passes `address`, `token_file` or similar, is
rejected. Per-reference `address`, `namespace`, `profile` or `role_arn` must move to
the operator's environment. A `ref+k8s://` reference must use that exact lower-case
spelling. Literal values still work.

A value read through `ref+k8s://` is no longer expanded a second time. If a source
Secret holds a `ref+...` string, the target Secret now receives that string as it is.

Find affected resources before upgrading:

```sh
kubectl get valssecrets -A -o yaml | grep -nE 'ref\+(echo|envsubst|exec|file|gitlab|httpjson|keychain|sops|tfstate[a-z0-9]*)://|ref\+.*ref\+|\?|K8S|secretref\+k8s'
```

### Orphaned leases

Earlier versions did not revoke a `DbSecret`'s lease when the credentials rotated or
when the resource was deleted, so leases accumulated in the backend and stayed valid
until their natural TTL. The operator has no record of those leases, so they must be
cleared in the backend.

List and inspect what is outstanding for a mount before revoking anything:

```sh
vault list sys/leases/lookup/<mount>/creds/<role>          # bao list ... for OpenBao
vault lease lookup <mount>/creds/<role>/<lease-id>
```

Revoke the ones that no longer belong to a live `DbSecret`. To clear every lease under
a role in one go — this invalidates credentials currently in use, so roll out the
consuming workloads afterwards:

```sh
vault lease revoke -prefix <mount>/creds/<role>
```

### Templates using `env`, `expandenv` or `getHostByName`

These sprig functions are no longer available in `ValsSecret` and `DbSecret`
templates. A template using one now fails with `function "env" not defined`.

Move the value into your secrets backend and reference it from `data` instead. See
[Usage — templates](usage.md#templates) for why.

Find affected resources before upgrading:

```sh
kubectl get valssecrets,dbsecrets -A -o yaml | grep -nE 'env |expandenv|getHostByName'
```

### `rollout[].kind: Pod`

Only `Deployment` and `StatefulSet` are accepted. `Pod` appeared in the field
documentation but was never implemented — it was accepted by the API and then failed
during reconciliation. A `DbSecret` using it is now rejected at apply time, so update
those resources before upgrading the CRDs.

```sh
kubectl get dbsecrets -A -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name}{"\t"}{.spec.rollout[*].kind}{"\n"}{end}'
```

### Also new in this release

Neither needs action, but both are worth adopting:

- `-allowed-backend-paths` restricts which backend paths each namespace may read. It
  defaults to permitting everything, so upgrading changes nothing until you configure
  it. Recommended on any cluster where namespaces are not all equally trusted — see
  [Security](security.md#restricting-backend-paths).
- The Helm chart is now published as an OCI artifact. The existing Helm repository
  still works — see [Installation](installation.md).
