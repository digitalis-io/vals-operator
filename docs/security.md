# Security

Vals-operator holds credentials for your secrets backends and can write Secrets into
every namespace it watches. This page describes what that means, and the controls
available to narrow it.

## Threat model

**What the operator is.** A cluster-scoped controller that authenticates to one or
more secrets backends **once at startup**, with a single set of credentials shared by
every namespace, and writes the values it retrieves into Kubernetes Secrets.

**The core consequence.** The operator's own backend policy is the real security
boundary. It does not — and cannot — impersonate the requesting namespace when it
calls a backend. Anybody who can create a `ValsSecret` or a `DbSecret` in a watched
namespace can, by default, read anything the operator's credential can read, and have
the result written into a Secret in their own namespace.

### Trust boundaries

| Actor | Can do | Constrained by |
|---|---|---|
| Cluster admin | Everything | — |
| Anyone with `create` on `ValsSecret`/`DbSecret` in a watched namespace | Read any backend path the operator's credential can read; read any Kubernetes Secret the operator can read, via `ref+k8s://` | `-allowed-backend-paths`, `-disable-namespace-sync`, `-allowed-namespaces-for-sync`, the operator's own backend policy and RBAC |
| Anyone with `get` on Secrets in a namespace | Read every synced value in that namespace | Kubernetes RBAC |
| The operator | Read its configured backends; create/update Secrets and restart workloads in watched namespaces | Its backend policy, its RBAC, `-watch-namespaces`, `-exclude-namespaces` |

### Threats and mitigations

| Threat | Mitigation |
|---|---|
| A tenant reads another tenant's backend paths through a `ValsSecret` or `DbSecret` | [`-allowed-backend-paths`](#restricting-backend-paths) |
| A tenant reads another namespace's Kubernetes Secrets via `ref+k8s://` | [`-disable-namespace-sync`, `-allowed-namespaces-for-sync`](#cross-namespace-references) |
| A template exfiltrates the operator's own environment, including its backend token | `env`, `expandenv` and `getHostByName` are removed from the template function set. Not configurable. |
| A `DbSecret` overwrites an unrelated Secret by naming it in `secretName` | The operator refuses to write to a Secret it does not own. |
| Credentials outlive the resource that requested them | Vault/OpenBao leases are revoked on rotation and on deletion. Leases orphaned by versions before this behaviour existed must be revoked in the backend — see [Upgrade notes](upgrading.md). |
| The operator reaches namespaces it has no business in | `-watch-namespaces`, `-exclude-namespaces` |
| A tenant uses `spec.target` to write into a resource type it should not, or to a privileged kind | [`-enable-custom-targets`, `-allowed-target-resources`](#custom-targets); RBAC groups, `apiextensions`, ServiceAccounts, Pods, Nodes, Namespaces and PersistentVolumes can never be targeted; targets are same-namespace only |
| A tampered image or chart is deployed | Cosign signatures and SBOM attestations on every release — see [Installation](installation.md#verifying-signatures). |

### Out of scope

- **Secrets at rest in Kubernetes.** Synced values live in ordinary Secrets, which are
  base64-encoded, not encrypted, unless you have enabled encryption at rest. Anybody
  with `get secret` in the namespace can read them.
- **Per-request identity.** The operator has no mechanism to authenticate to a backend
  *as* the requesting namespace or user. `-allowed-backend-paths` is a policy applied
  by the operator, not delegated authentication.
- **Client behaviour on rotation.** Whether a workload picks up a rotated credential is
  a property of that workload; `rollout` helps but guarantees nothing.

## Restricting backend paths

`-allowed-backend-paths` (Helm value `allowedBackendPaths`) narrows the operator's
reach per namespace. It covers **both** resources — every `ref+...://` reference in a
`ValsSecret`, across all backends, and the mount and role named by a `DbSecret`.
Restricting only one of them achieves nothing, because the same dynamic database
credentials are reachable either way:

```yaml
# These two request identical credentials, so both must be governed by the same rule.
apiVersion: digitalis.io/v1beta1
kind: DbSecret
metadata:
  name: db-route
spec:
  vault:
    mount: database
    role: other-tenant-role
---
apiVersion: digitalis.io/v1
kind: ValsSecret
metadata:
  name: vals-route
spec:
  data:
    password:
      ref: "ref+vault://database/creds/other-tenant-role#/password"
```

Configure it as semicolon-separated `namespace=prefix[,prefix...]` entries. A namespace
of `*` applies to every namespace, in addition to any namespace-specific entry:

```yaml
# Helm values
allowedBackendPaths: "team-a=ref+vault://database/creds/team-a,ref+k8s://team-a;team-b=ref+vault://database/creds/team-b;*=ref+awssecrets://shared"
```

Or as an operator flag directly:

```sh
vals-operator -allowed-backend-paths='team-a=ref+vault://database/creds/team-a;*=ref+awssecrets://shared'
```

Prefixes match on path-segment boundaries, so `ref+vault://database/creds/team-a` does
not cover `ref+vault://database/creds/team-abc`.

An empty value (the default) allows every path, so upgrading changes nothing until you
configure it. A namespace with no matching entry is denied once the flag is set.
Denied references are rejected **before any backend call is made**, and the reason is
recorded as an event naming the namespace and the rejected path:

```sh
kubectl describe valssecret my-secret
kubectl describe dbsecret my-db
```

## Cross-namespace references

The `ref+k8s://namespace/secret#key` syntax lets a `ValsSecret` read a Kubernetes
Secret from a different namespace. In multi-tenant clusters this is a privilege
escalation vector: a tenant who can create `ValsSecret` resources can read Secrets from
any namespace the operator has RBAC access to.

These two flags are a narrower control than `-allowed-backend-paths`: they apply only
to `ref+k8s://` references and do not restrict any other backend.

### `-disable-namespace-sync`

When `true`, the operator rejects any `ref+k8s://` reference whose target namespace
differs from the `ValsSecret`'s own. Same-namespace references are never blocked. This
is the most restrictive option; use it where no cross-namespace sharing is required.

A rejected reference produces the event:

```
cross-namespace ref+k8s:// is disabled: namespace "tenant-a" cannot reference "tenant-b"
```

```sh
helm upgrade --install vals-operator digitalis/vals-operator \
  --set "disableNamespaceSync=true"
```

### `-allowed-namespaces-for-sync`

A namespace-level allowlist. Only namespaces named in the list may be the *target* of a
cross-namespace reference. Same-namespace references are always permitted. An empty
value (the default) allows all namespaces, subject to `-disable-namespace-sync`.

A reference targeting a namespace not in the allowlist produces the event:

```
cross-namespace ref+k8s:// denied: namespace "restricted" is not in the allowed list
```

Commas are `--set` separators, so escape them or use a values file:

```sh
helm upgrade --install vals-operator digitalis/vals-operator \
  --set-string 'allowedNamespacesForSync=shared-secrets\,platform'
```

### Precedence

`-disable-namespace-sync` takes precedence. When it is `true` the allowlist is not
consulted at all.

| `-disable-namespace-sync` | `-allowed-namespaces-for-sync` | Result |
|---|---|---|
| `false` | `""` (empty) | All cross-namespace refs allowed |
| `false` | `"ns-a,ns-b"` | Only refs targeting `ns-a` or `ns-b` allowed |
| `true` | any value | All cross-namespace refs rejected |

Same-namespace references are always allowed in every configuration.

## Custom targets

[`spec.target`](usage.md#custom-targets) lets a `ValsSecret` write backend values into
resources other than Secrets. It is disabled unless the operator runs with:

| Flag | Helm value | Description |
|---|---|---|
| `-enable-custom-targets` | `customTargets.enabled` | Feature gate. Off: every `spec.target` is rejected with `Ready=False/FeatureDisabled` and nothing is read from the backend. |
| `-allowed-target-resources` | `customTargets.allowedResources` | Comma-separated `resource.group` list (core group: `configmaps`), e.g. `configmaps,flinkdeployments.flink.apache.org`. Empty means nothing is allowed, even with the gate on. The chart renders the operator's ClusterRole from the same list. |
| `-target-dry-run` | `customTargets.dryRun` | Server-side dry-run before every apply (default `true`). |

Regardless of the allow-list, these can never be targeted: anything in
`rbac.authorization.k8s.io`, `admissionregistration.k8s.io`, `apiextensions.k8s.io`,
`authentication.k8s.io`, `authorization.k8s.io`; core `serviceaccounts`, `pods`,
`nodes`, `namespaces`, `persistentvolumes`; any cluster-scoped kind. The target always
lives in the `ValsSecret`'s own namespace, and `apiVersion`/`kind` are fixed strings,
never rendered from a template.

What to think about before adding a resource to the list:

- **The value becomes plaintext.** A backend secret written into a ConfigMap or a CRD
  `spec` is readable by anybody with `get` on that kind, appears in `kubectl get -o yaml`,
  in audit logs and in etcd unencrypted. Prefer a kind that references a Secret; use a
  custom target only where the consumer offers no such indirection (the Flink
  `flinkConfiguration` case).
- **Workload kinds are code execution.** Allowing `deployments`, `statefulsets`,
  `daemonsets` or `jobs` lets anybody who can create a `ValsSecret` patch a pod spec —
  environment, image, command — as that workload's ServiceAccount. Allow them only in
  namespaces where every `ValsSecret` author already holds that power.
- **`mode: patch` writes into objects the operator does not own.** Ownership is per
  field (Server-Side Apply, field manager `vals-operator`); the operator never touches
  fields it did not render and never deletes the object. Still, it will overwrite the
  listed fields, so pair the allow-list with RBAC on who may create `ValsSecret`s.
- `-allowed-backend-paths` still governs what may be *read*; the two lists compose.

## Recommendations

For any cluster where namespaces are not all equally trusted:

1. Give the operator's backend credential the narrowest policy that covers what your
   workloads actually need. This is the boundary that matters most.
2. Set `allowedBackendPaths` for every tenant namespace.
3. Set `disableNamespaceSync=true` unless cross-namespace mirroring is a requirement;
   otherwise set `allowedNamespacesForSync`.
4. Limit the operator with `-watch-namespaces` or `-exclude-namespaces`.
5. Restrict who can create `ValsSecret` and `DbSecret` resources with RBAC. Nothing
   above helps if any user in a namespace can create them.
6. Enable encryption at rest for Kubernetes Secrets.
7. Leave `customTargets.enabled=false` unless a workload needs it, and then list only
   the exact resources required.

## Reporting a vulnerability

Report security issues privately to [info@digitalis.io](mailto:info@digitalis.io)
rather than opening a public issue.
