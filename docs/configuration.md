# Configuration reference

- [Operator flags](#operator-flags) — the binary's command-line arguments.
- [Helm values](#helm-values) — every key in `charts/vals-operator/values.yaml`.
- [Annotations](#annotations) — per-resource overrides.

## Operator flags

The operator binary accepts the following flags. All flags are optional unless noted.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-metrics-bind-address` | string | `:8080` | Address the metrics endpoint binds to. |
| `-health-probe-bind-address` | string | `:8081` | Address the health probe endpoint binds to. |
| `-reconcile-period` | duration | `5s` | How often the controller re-queues reconciliation events. |
| `-ttl` | duration | `5m0s` | How often each secret is checked against the backend store for updates. |
| `-watch-namespaces` | string | `""` | Comma-separated list of namespaces the operator watches. Empty means all namespaces. |
| `-exclude-namespaces` | string | `""` | Comma-separated list of namespaces the operator ignores entirely. |
| `-record-changes` | bool | `true` | Records each secret update as a Kubernetes Event, visible via `kubectl describe`. Can be overridden per resource with the annotation `vals-operator.digitalis.io/record: "true"`. |
| `-leader-elect` | bool | `false` | Enables leader election, ensuring only one active controller instance when running multiple replicas. |
| `-disable-namespace-sync` | bool | `false` | Blocks all cross-namespace `ref+k8s://` references. See [Cross-namespace references](security.md#cross-namespace-references). |
| `-allowed-namespaces-for-sync` | string | `""` | Comma-separated allowlist of namespaces that may be referenced via `ref+k8s://`. See [Cross-namespace references](security.md#cross-namespace-references). |
| `-allowed-backend-paths` | string | `""` | Restricts which backend paths each namespace may read, covering both `ValsSecret` references (all backends) and `DbSecret` mounts/roles. Semicolon-separated `namespace=prefix[,prefix...]` entries; `*` applies to all namespaces. See [Restricting backend paths](security.md#restricting-backend-paths). |
| `-enable-custom-targets` | bool | `false` | Allow `ValsSecret.spec.target` to write into resources other than Secrets. See [Custom targets](security.md#custom-targets). |
| `-allowed-target-resources` | string | `""` | Comma-separated `resource.group` list that `spec.target` may write, e.g. `configmaps,flinkdeployments.flink.apache.org`. Empty allows nothing. |
| `-target-dry-run` | bool | `true` | Server-side dry-run before applying a custom target. |
| `-kubeconfig` | string | `""` | Path to a kubeconfig. Only required when running out of cluster. |
| `-zap-devel` | bool | `true` | Development log defaults: console encoder, debug level, stack traces at warn. |
| `-zap-encoder` | string | — | Log encoding, `json` or `console`. |
| `-zap-log-level` | string | — | `debug`, `info`, `error`, or an integer for custom verbosity levels. |
| `-zap-stacktrace-level` | string | — | Level at and above which stack traces are captured: `info`, `error` or `panic`. |

Pass flags through the chart with `args`:

```yaml
args:
  - -exclude-namespaces=kube-system,kube-public
  - -ttl=15m
```

The three security flags also have dedicated values — `disableNamespaceSync`,
`allowedNamespacesForSync` and `allowedBackendPaths` — which are rendered on top of
whatever `args` contains.

## Helm values

### Deployment

| Key | Type | Default | Description |
|---|---|---|---|
| `replicaCount` | int | `1` | Declared for compatibility. The Deployment template always renders one replica; use `-leader-elect` if you change this in a fork. |
| `image.repository` | string | `ghcr.io/digitalis-io/vals-operator` | Container image. |
| `image.tag` | string | `""` | Image tag. Defaults to the chart `appVersion`. |
| `image.pullPolicy` | string | `IfNotPresent` | Image pull policy. |
| `imagePullSecrets` | list | `[]` | Pull secrets for a private registry. Example: `[{name: my-registry}]`. |
| `nameOverride` | string | `""` | Overrides the chart name in resource names. |
| `fullnameOverride` | string | `""` | Overrides the full resource name prefix. |
| `resources` | object | `{}` | Container resource requests and limits. Example: `{requests: {cpu: 100m, memory: 128Mi}}`. |
| `nodeSelector` | object | `{}` | Node selector for the operator pod. |
| `tolerations` | list | `[]` | Tolerations for the operator pod. |
| `affinity` | object | `{}` | Affinity rules for the operator pod. |
| `podSecurityContext` | object | `{}` | Pod-level security context. Example: `{fsGroup: 2000}`. |
| `securityContext` | object | `{}` | Container-level security context. Example: `{runAsNonRoot: true, readOnlyRootFilesystem: true}`. |
| `podAnnotations` | object | unset | Annotations on the operator pod. Not present in `values.yaml`; set it to add them. |
| `metricsPort` | int | `8080` | Container port named `metrics`. Not present in `values.yaml`; set it if you also change `-metrics-bind-address`. |

### RBAC and CRDs

| Key | Type | Default | Description |
|---|---|---|---|
| `serviceAccount.create` | bool | `true` | Create a ServiceAccount for the operator. |
| `serviceAccount.name` | string | `""` | Name of the ServiceAccount. Generated from the chart fullname when empty. |
| `serviceAccount.annotations` | object | `{}` | Annotations on the ServiceAccount. Use this for IRSA on EKS: `{eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/vals}`. |
| `manageCrds` | bool | `true` | Render the `ValsSecret` and `DbSecret` CRDs as part of the chart. Set `false` when CRDs are managed elsewhere. |
| `enableDbSecrets` | bool | `true` | Install the `DbSecret` CRD and the wider RBAC that controller needs. Set `false` if you only sync static secrets. |

### Operator behaviour

| Key | Type | Default | Description |
|---|---|---|---|
| `args` | list | `[]` | Extra command-line flags. Example: `["-exclude-namespaces=kube-system", "-ttl=15m"]`. |
| `disableNamespaceSync` | bool | `false` | Renders `-disable-namespace-sync`. Blocks all cross-namespace `ref+k8s://` references. |
| `allowedNamespacesForSync` | string | `""` | Renders `-allowed-namespaces-for-sync`. Example: `"shared-secrets,platform"`. |
| `allowedBackendPaths` | string | `""` | Renders `-allowed-backend-paths`. Example: `"team-a=ref+vault://database/creds/team-a;*=ref+awssecrets://shared"`. See [Security](security.md#restricting-backend-paths). |
| `customTargets.enabled` | bool | `false` | Renders `-enable-custom-targets`. See [Custom targets](security.md#custom-targets). |
| `customTargets.dryRun` | bool | `true` | Renders `-target-dry-run`. |
| `customTargets.allowedResources` | list | `[]` | Resources `spec.target` may write; each entry is `{resource, group}` (`group: ""` for core). Renders both `-allowed-target-resources` and the matching ClusterRole rules. Example: `[{resource: configmaps, group: ""}, {resource: flinkdeployments, group: flink.apache.org}]`. |

### Backend configuration

| Key | Type | Default | Description |
|---|---|---|---|
| `openbao.enabled` | bool | `false` | Render the `BAO_*` environment variables. |
| `openbao.address` | string | `http://openbao:8200` | OpenBao server URL (`BAO_ADDR`). |
| `openbao.skipVerify` | bool | `false` | Skip TLS verification (`BAO_SKIP_VERIFY`). |
| `openbao.auth.kubernetes.roleId` | string | `""` | Kubernetes auth role (`BAO_ROLE_ID`). Example: `vals-operator`. |
| `openbao.auth.kubernetes.mountPoint` | string | `kubernetes` | Kubernetes auth mount (`BAO_KUBERNETES_MOUNT_POINT`). |
| `openbao.auth.approle.roleId` | string | `""` | AppRole role ID (`BAO_APP_ROLE`). |
| `openbao.auth.approle.secretId` | string | `""` | AppRole secret ID (`BAO_SECRET_ID`). Prefer `environmentSecret` — this value ends up in the release manifest. |
| `openbao.auth.approle.mountPath` | string | `approle` | AppRole mount (`BAO_APPROLE_MOUNT_PATH`). |
| `openbao.auth.userpass.username` | string | `""` | Userpass username (`BAO_LOGIN_USER`). Not recommended. |
| `openbao.auth.userpass.password` | string | `""` | Userpass password (`BAO_LOGIN_PASSWORD`). Not recommended. |
| `openbao.auth.userpass.mountPath` | string | `userpass` | Userpass mount (`BAO_USERPASS_MOUNT_PATH`). |
| `vault.enabled` | bool | `false` | Render the `VAULT_*` environment variables. |
| `vault.address` | string | `http://vault:8200` | Vault server URL (`VAULT_ADDR`). |
| `vault.skipVerify` | bool | `false` | Skip TLS verification (`VAULT_SKIP_VERIFY`). |
| `vault.auth.kubernetes.roleId` | string | `""` | Kubernetes auth role (`VAULT_ROLE_ID`). Example: `vals-operator`. |
| `vault.auth.kubernetes.mountPoint` | string | `kubernetes` | Kubernetes auth mount (`VAULT_KUBERNETES_MOUNT_POINT`). |
| `vault.auth.approle.roleId` | string | `""` | AppRole role ID (`VAULT_APP_ROLE`). |
| `vault.auth.approle.secretId` | string | `""` | AppRole secret ID (`VAULT_SECRET_ID`). Prefer `environmentSecret`. |
| `vault.auth.approle.mountPath` | string | `approle` | AppRole mount (`VAULT_APPROLE_MOUNT_PATH`). |
| `vault.auth.userpass.username` | string | `""` | Userpass username (`VAULT_LOGIN_USER`). Not recommended. |
| `vault.auth.userpass.password` | string | `""` | Userpass password (`VAULT_LOGIN_PASSWORD`). Not recommended. |
| `vault.auth.userpass.mountPath` | string | `userpass` | Userpass mount (`VAULT_USERPASS_MOUNT_PATH`). |

Both blocks may be enabled at once; OpenBao then takes precedence and a warning is
logged. See [Secrets backends](backends.md#vault-and-openbao).

### Environment and volumes

| Key | Type | Default | Description |
|---|---|---|---|
| `env` | list | `[]` | Extra environment variables for any vals backend. Example: `[{name: AWS_DEFAULT_REGION, value: us-west-2}]`. |
| `secretEnv` | list | `[]` | `envFrom` sources. Example: `[{secretRef: {name: aws-creds}}]`. Ignored when `environmentSecret` is set. |
| `environmentSecret` | string | `""` | Name of a single Secret loaded with `envFrom`. Takes precedence over `secretEnv`. Example: `vals-operator-env`. |
| `volumes` | list | `[]` | Extra pod volumes. Example: `[{name: creds, secret: {secretName: gcs-credentials}}]`. |
| `volumeMounts` | list | `[]` | Extra container volume mounts. Example: `[{name: creds, mountPath: /secret, readOnly: true}]`. |

### Monitoring

| Key | Type | Default | Description |
|---|---|---|---|
| `podMonitor.enabled` | bool | `false` | Create a Prometheus Operator `PodMonitor` for the operator pod, scraping `/metrics` every 30s in the release namespace. |
| `podMonitor.labels` | object | `{}` | Reserved. Present in `values.yaml` but not currently rendered by the `PodMonitor` template — the monitor carries the standard chart labels only. |
| `prometheusRules.enabled` | bool | `false` | Create a `PrometheusRule` with the operator's alerts. |
| `prometheusRules.additionalRuleLabels` | object | `{}` | Extra labels on every alert. Example: `{severity_team: platform}`. |
| `prometheusRules.additionalRuleAnnotations` | object | `{}` | Extra annotations on every alert. Example: `{runbook_url: https://example.com/runbook}`. |

## Annotations

Set on a `ValsSecret` or `DbSecret`:

| Annotation | Values | Description |
|---|---|---|
| `vals-operator.digitalis.io/record` | `"true"` | Record Kubernetes Events for this resource even when the operator runs with `-record-changes=false`. |

The operator also writes its own annotations onto the Secrets it manages
(`last-updated`, `lease-id`, `lease-duration`, `expires-on`, `hash`) and onto the pod
templates it restarts (`restartedAt`). These are internal bookkeeping — read them if
useful, do not set them.
