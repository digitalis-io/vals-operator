# Secrets backends

Vals-operator resolves every `ref+...://` reference through
[vals](https://github.com/helmfile/vals), so any store vals supports works here:
HashiCorp Vault, OpenBao, AWS Secrets Manager, AWS SSM Parameter Store, GCP Secret
Manager, Azure Key Vault, SOPS, Kubernetes secrets, and more. See the
[vals README](https://github.com/helmfile/vals#supported-backends) for the full list
and the reference syntax of each.

The operator authenticates to its backends **once at startup**, using a single
credential shared by every namespace. That credential's own policy is the real
security boundary — see [Security](security.md) before running in a multi-tenant
cluster.

## Vault and OpenBao

Both are supported, and the operator picks one at runtime from its environment:

| `BAO_ADDR` | `VAULT_ADDR` | Backend used |
|---|---|---|
| set | unset | OpenBao |
| unset | set | HashiCorp Vault |
| set | set | OpenBao, with a warning logged |
| unset | unset | Error — no backend configured |

`BAO_*` variables fall back to their `VAULT_*` equivalents when unset, so an existing
Vault deployment can move to OpenBao by setting `BAO_ADDR` alone and leaving the rest
of its configuration in place.

```sh
# Equivalent to setting BAO_ADDR and BAO_ROLE_ID:
BAO_ADDR=http://openbao:8200
VAULT_ROLE_ID=my-role
```

### OpenBao (recommended for new deployments)

```sh
# Kubernetes auth
helm upgrade --install vals-operator --create-namespace -n vals-operator \
  --set "openbao.enabled=true" \
  --set "openbao.address=http://openbao:8200" \
  --set "openbao.auth.kubernetes.roleId=vals-operator" \
  digitalis/vals-operator

# AppRole auth
helm upgrade --install vals-operator --create-namespace -n vals-operator \
  --set "openbao.enabled=true" \
  --set "openbao.address=http://openbao:8200" \
  --set "openbao.auth.approle.roleId=my-role-id" \
  --set "openbao.auth.approle.secretId=my-secret-id" \
  digitalis/vals-operator
```

### HashiCorp Vault

```sh
# Kubernetes auth
helm upgrade --install vals-operator --create-namespace -n vals-operator \
  --set "vault.enabled=true" \
  --set "vault.address=http://vault:8200" \
  --set "vault.auth.kubernetes.roleId=vals-operator" \
  digitalis/vals-operator
```

### Database secrets engine

`DbSecret` requires a database secrets engine mount. Vault and OpenBao ship plugins
for PostgreSQL, MySQL, Cassandra, Elasticsearch and others; ClickHouse needs a
third-party plugin such as
[openbao-plugin-database-clickhouse](https://github.com/digitalis-io/openbao-plugin-database-clickhouse).
See [Usage — DbSecret](usage.md#dbsecret).

### Authentication environment variables

The `openbao.*` and `vault.*` Helm values render into the variables below. Set them
directly with `env`, `secretEnv` or `environmentSecret` if you prefer.

| OpenBao | Vault | Purpose |
|---|---|---|
| `BAO_ADDR` | `VAULT_ADDR` | Server URL, e.g. `http://openbao:8200`. |
| `BAO_SKIP_VERIFY` | `VAULT_SKIP_VERIFY` | Skip TLS certificate verification. |
| `BAO_ROLE_ID` | `VAULT_ROLE_ID` | Role for Kubernetes auth. |
| `BAO_KUBERNETES_MOUNT_POINT` | `VAULT_KUBERNETES_MOUNT_POINT` | Kubernetes auth mount, default `kubernetes`. |
| `BAO_APP_ROLE` + `BAO_SECRET_ID` | `VAULT_APP_ROLE` + `VAULT_SECRET_ID` | AppRole auth. |
| `BAO_APPROLE_MOUNT_PATH` | `VAULT_APPROLE_MOUNT_PATH` | AppRole mount, default `approle`. |
| `BAO_LOGIN_USER` + `BAO_LOGIN_PASSWORD` | `VAULT_LOGIN_USER` + `VAULT_LOGIN_PASSWORD` | Userpass auth. Not recommended. |
| `BAO_USERPASS_MOUNT_PATH` | `VAULT_USERPASS_MOUNT_PATH` | Userpass mount, default `userpass`. |

Backend-side setup for Kubernetes auth is documented upstream:
[OpenBao](https://openbao.org/docs/auth/kubernetes/),
[Vault](https://developer.hashicorp.com/vault/docs/auth/kubernetes).

Storing an AppRole secret ID or a password in Helm values puts it in the release
manifest. Prefer Kubernetes auth, or put the credentials in a Secret and reference it
with `environmentSecret`:

```sh
kubectl create secret generic -n vals-operator vals-operator-env \
  --from-literal=BAO_ADDR=http://openbao:8200 \
  --from-literal=BAO_APP_ROLE=my-role-id \
  --from-literal=BAO_SECRET_ID=my-secret-id

helm upgrade --install vals-operator --create-namespace -n vals-operator \
  --set "environmentSecret=vals-operator-env" \
  digitalis/vals-operator
```

> `environmentSecret` replaces `secretEnv`; when it is set, `secretEnv` is ignored.

## AWS

```sh
kubectl create secret generic -n vals-operator aws-creds \
  --from-literal=AWS_ACCESS_KEY_ID=foo \
  --from-literal=AWS_SECRET_ACCESS_KEY=bar \
  --from-literal=AWS_DEFAULT_REGION=us-west-2

helm upgrade --install vals-operator --create-namespace -n vals-operator \
  --set "secretEnv[0].secretRef.name=aws-creds" \
  digitalis/vals-operator
```

On EKS, use IAM Roles for Service Accounts instead of static keys — see
[EKS integration](eks/index.md).

## Google Cloud

```sh
kubectl create secret generic -n vals-operator google-creds \
  --from-file=credentials.json=/path/to/service_account.json

helm upgrade --install vals-operator --create-namespace -n vals-operator \
  --set "env[0].name=GOOGLE_APPLICATION_CREDENTIALS,env[0].value=/secret/credentials.json" \
  --set "env[1].name=GCP_PROJECT,env[1].value=my_project" \
  --set "volumes[0].name=creds,volumes[0].secret.secretName=google-creds" \
  --set "volumeMounts[0].name=creds,volumeMounts[0].mountPath=/secret" \
  digitalis/vals-operator
```

## Other backends

Every other vals backend is configured the same way: put whatever environment
variables that backend needs into `env`, `secretEnv` or `environmentSecret`. The
operator itself needs no per-backend configuration beyond that.

## Next steps

- [Usage](usage.md) — reference secrets from a `ValsSecret`.
- [Security](security.md) — restrict what each namespace may read.
- [Configuration reference](configuration.md) — every operator flag and Helm value.
