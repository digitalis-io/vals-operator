# Upgrade notes

Behaviour changes that need action when upgrading an existing deployment. See
[CHANGELOG.md](../CHANGELOG.md) for the full history.

## Current release

Three changes need action.

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
