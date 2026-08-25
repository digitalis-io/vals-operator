# vals-operator

![Version: 0.8.2](https://img.shields.io/badge/Version-0.8.2-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: v0.8.2](https://img.shields.io/badge/AppVersion-v0.8.2-informational?style=flat-square)

## OCI Registry

This chart is available as an OCI artifact. Helm 3.8+ consumers can install directly without adding a Helm repository:

```bash
helm install vals-operator oci://ghcr.io/digitalis-io/helm-charts/vals-operator --version <version>
```

The traditional Helm repository at `https://digitalis-io.github.io/helm-charts` remains available for Helm versions earlier than 3.8.

This helm chart installs the Digitalis Vals Operator to manage and sync secrets from supported backends into Kubernetes.

Full documentation: [docs/index.md](https://github.com/digitalis-io/vals-operator/blob/main/docs/index.md).

## About Vals-Operator

Here at [Digitalis](https://digitalis.io) we love [vals](https://github.com/helmfile/vals), it's a tool we use daily to keep secrets stored securely. Inspired by this tool, we have created an operator to manage Kubernetes secrets.

*vals-operator* syncs secrets from any secrets store supported by [vals](https://github.com/helmfile/vals) into Kubernetes. Also, `vals-operator` supports database secrets as provider by [HashiCorp Vault Secret Engine](https://developer.hashicorp.com/vault/docs/secrets/databases).

## Backend configuration

The operator needs credentials for at least one secrets backend. See
[Secrets backends](https://github.com/digitalis-io/vals-operator/blob/main/docs/backends.md).

On any cluster where namespaces are not all equally trusted, also set `allowedBackendPaths` and
restrict cross-namespace `ref+k8s://` references — see
[Security](https://github.com/digitalis-io/vals-operator/blob/main/docs/security.md).

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| Digitalis.IO | <info@digitalis.io> |  |

## Requirements

Kubernetes: `>= 1.19.0-0`

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` | Affinity rules for the operator pod. |
| allowedBackendPaths | string | `""` | Renders `-allowed-backend-paths`, e.g. `"team-a=ref+vault://database/creds/team-a;*=ref+awssecrets://shared"`. Restrict which backend paths each namespace may read. This covers BOTH the references in a ValsSecret (every backend, not just ref+k8s://) and the mount and role named by a DbSecret, so that restricting one resource cannot be sidestepped through the other.  Semicolon-separated "namespace=prefix[,prefix...]" entries. A namespace of "*" applies to every namespace, in addition to any namespace-specific entry. Prefixes match on path-segment boundaries.  Empty string (the default) allows every path the operator's backend credential can read. Set this on any cluster where namespaces are not all equally trusted: the operator holds one credential for all namespaces, so anybody who can create a ValsSecret or DbSecret otherwise inherits its full reach. |
| allowedNamespacesForSync | string | `""` | Renders `-allowed-namespaces-for-sync`, e.g. `"shared-secrets,platform"`. Comma-separated list of namespaces that may be referenced via ref+k8s://. Empty string means all namespaces are allowed (unless disableNamespaceSync is true). |
| args | list | `[]` | Additional command-line flags for the operator, e.g. `["-exclude-namespaces=kube-system", "-ttl=15m"]`. See https://github.com/digitalis-io/vals-operator/blob/main/docs/configuration.md |
| customTargets.allowedResources | list | `[]` | Resources that `spec.target` may write. Each entry renders both the `-allowed-target-resources` flag and the matching ClusterRole rule, so RBAC and the allow-list cannot drift. `group: ""` is the core group. Example:   - resource: configmaps     group: ""   - resource: flinkdeployments     group: flink.apache.org |
| customTargets.dryRun | bool | `true` | Renders `-target-dry-run`. Server-side dry-run before each apply. |
| customTargets.enabled | bool | `false` | Renders `-enable-custom-targets`. Off by default: every `spec.target` is rejected with `Ready=False/FeatureDisabled` until enabled. |
| disableNamespaceSync | bool | `false` | Renders `-disable-namespace-sync`. Disable cross-namespace ref+k8s:// references. When true, a ValsSecret can only reference k8s secrets in its own namespace. Takes precedence over allowedNamespacesForSync. |
| enableDbSecrets | bool | `true` | Install the DbSecret CRD and the wider RBAC that controller requires. This may not be required by everyone and the pod will require wider permissions which may not be desired on secure environments |
| env | list | `[]` | Additional environment variables for vals backends, e.g. `[{name: AWS_DEFAULT_REGION, value: "us-west-2"}]`. See https://github.com/helmfile/vals for information on setting up your backend environment. |
| environmentSecret | string | `""` | Name of a single Secret loaded into the pod with `envFrom`, e.g. `vals-operator-env`. Takes precedence over `secretEnv` when set. |
| fullnameOverride | string | `""` | Overrides the full resource name prefix. |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy. |
| image.repository | string | `"ghcr.io/digitalis-io/vals-operator"` | Container image repository. |
| image.tag | string | `""` | Overrides the image tag whose default is the chart appVersion. |
| imagePullSecrets | list | `[]` | Pull secrets for a private registry, e.g. `[{name: my-registry}]`. |
| manageCrds | bool | `true` | Render the ValsSecret and DbSecret CRDs as part of the chart. Set to false when the CRDs are managed elsewhere, such as by a cluster-scoped GitOps process. |
| nameOverride | string | `""` | Overrides the chart name used in resource names. |
| nodeSelector | object | `{}` | Node selector for the operator pod. |
| openbao.address | string | `"http://openbao:8200"` | OpenBao server URL (`BAO_ADDR`). |
| openbao.auth.approle.mountPath | string | `"approle"` | AppRole mount (`BAO_APPROLE_MOUNT_PATH`). |
| openbao.auth.approle.roleId | string | `""` | AppRole role ID (`BAO_APP_ROLE`). |
| openbao.auth.approle.secretId | string | `""` | AppRole secret ID (`BAO_SECRET_ID`). Prefer `environmentSecret` — this value ends up in the Helm release manifest. |
| openbao.auth.kubernetes.mountPoint | string | `"kubernetes"` | Kubernetes auth mount (`BAO_KUBERNETES_MOUNT_POINT`). |
| openbao.auth.kubernetes.roleId | string | `""` | Kubernetes auth role (`BAO_ROLE_ID`), e.g. `vals-operator`. |
| openbao.auth.userpass.mountPath | string | `"userpass"` | Userpass mount (`BAO_USERPASS_MOUNT_PATH`). |
| openbao.auth.userpass.password | string | `""` | Userpass password (`BAO_LOGIN_PASSWORD`). Not recommended. |
| openbao.auth.userpass.username | string | `""` | Userpass username (`BAO_LOGIN_USER`). Not recommended. |
| openbao.enabled | bool | `false` | Render the `BAO_*` environment variables. |
| openbao.skipVerify | bool | `false` | Skip TLS certificate verification (`BAO_SKIP_VERIFY`). |
| podMonitor.enabled | bool | `false` | Create a Prometheus Operator PodMonitor for the operator pod, scraping /metrics every 30s in the release namespace. |
| podMonitor.labels | object | `{}` | Reserved. Not currently applied by the PodMonitor template, which carries the standard chart labels only. |
| podSecurityContext | object | `{}` | Pod-level security context, e.g. `{fsGroup: 2000}`. |
| prometheusRules.additionalRuleAnnotations | object | `{}` | Additional annotations for PrometheusRule alerts, e.g. `{runbook_url: https://example.com/runbook}`. |
| prometheusRules.additionalRuleLabels | object | `{}` | Additional labels for PrometheusRule alerts, e.g. `{severity_team: platform}`. |
| prometheusRules.enabled | bool | `false` | Create a PrometheusRule with the operator's alerts. |
| replicaCount | int | `1` | Declared for compatibility. The Deployment template always renders a single replica; enable `-leader-elect` via `args` if you change this in a fork. |
| resources | object | `{}` | Container resource requests and limits, e.g. `{requests: {cpu: 100m, memory: 128Mi}}`. |
| secretEnv | list | `[]` | `envFrom` sources for the operator container, e.g. `[{secretRef: {name: aws-creds}}]`. Ignored when `environmentSecret` is set. |
| securityContext | object | `{}` | Container-level security context, e.g. `{runAsNonRoot: true, readOnlyRootFilesystem: true}`. |
| serviceAccount.annotations | object | `{}` | Annotations to add to the service account. Use this for IRSA on EKS, e.g. `{eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/vals}`. |
| serviceAccount.create | bool | `true` | Specifies whether a service account should be created |
| serviceAccount.name | string | `""` | The name of the service account to use. If not set and create is true, a name is generated using the fullname template |
| tolerations | list | `[]` | Tolerations for the operator pod. |
| vault.address | string | `"http://vault:8200"` | Vault server URL (`VAULT_ADDR`). |
| vault.auth.approle.mountPath | string | `"approle"` | AppRole mount (`VAULT_APPROLE_MOUNT_PATH`). |
| vault.auth.approle.roleId | string | `""` | AppRole role ID (`VAULT_APP_ROLE`). |
| vault.auth.approle.secretId | string | `""` | AppRole secret ID (`VAULT_SECRET_ID`). Prefer `environmentSecret`. |
| vault.auth.kubernetes.mountPoint | string | `"kubernetes"` | Kubernetes auth mount (`VAULT_KUBERNETES_MOUNT_POINT`). |
| vault.auth.kubernetes.roleId | string | `""` | Kubernetes auth role (`VAULT_ROLE_ID`), e.g. `vals-operator`. |
| vault.auth.userpass.mountPath | string | `"userpass"` | Userpass mount (`VAULT_USERPASS_MOUNT_PATH`). |
| vault.auth.userpass.password | string | `""` | Userpass password (`VAULT_LOGIN_PASSWORD`). Not recommended. |
| vault.auth.userpass.username | string | `""` | Userpass username (`VAULT_LOGIN_USER`). Not recommended. |
| vault.enabled | bool | `false` | Render the `VAULT_*` environment variables. |
| vault.skipVerify | bool | `false` | Skip TLS certificate verification (`VAULT_SKIP_VERIFY`). |
| volumeMounts | list | `[]` | Additional container volume mounts, e.g. `[{name: creds, mountPath: /secret, readOnly: true}]`. |
| volumes | list | `[]` | Additional pod volumes, e.g. `[{name: creds, secret: {secretName: gcs-credentials}}]`. |

## Support

Maintained by [Digitalis.io](https://digitalis.io). For commercial support, consulting or managed
services, get in touch at [digitalis.io/contact](https://digitalis.io/contact).

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2)
