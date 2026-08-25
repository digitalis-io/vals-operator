# Vals-operator documentation

| Page | What it covers |
|---|---|
| [Installation](installation.md) | Installing with Helm from the OCI registry or the Helm repository, CRD management, and verifying release signatures and SBOMs. |
| [Secrets backends](backends.md) | Pointing the operator at Vault, OpenBao, AWS, GCP or any other vals backend, and the environment variables each needs. |
| [Usage](usage.md) | The `ValsSecret` and `DbSecret` resources: fields, templates, rollouts and database password rotation. |
| [Security](security.md) | Threat model, restricting backend paths per namespace, and controlling cross-namespace `ref+k8s://` references. |
| [Configuration reference](configuration.md) | Every operator flag, every Helm value, and the annotations the operator understands. |
| [Upgrade notes](upgrading.md) | Behaviour changes that need action before upgrading an existing deployment. |
| [EKS integration](eks/index.md) | IAM trust policy and service account setup for reading AWS secrets from EKS with IRSA. |

Also in the repository:

- [README](../README.md) — what vals-operator is, and a quick start.
- [CHANGELOG](../CHANGELOG.md) — release history.
- [CONTRIBUTING](../CONTRIBUTING.md) — how to build, test and submit changes.
