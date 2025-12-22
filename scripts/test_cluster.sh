#!/usr/bin/env bash
export KUBERLR_ALLOWDOWNLOAD=false
KUBECTL=${KUBECTL:-kubectl}
echo "Using kubectl: $(which $KUBECTL)"
set -euo pipefail

CLUSTER_NAME="dev-secrets-cluster"

echo "=== Checking prerequisites (docker, kind, kubectl, helm) ==="
command -v docker >/dev/null 2>&1 || { echo "docker not found in PATH"; exit 1; }
command -v kind   >/dev/null 2>&1 || { echo "kind not found in PATH"; exit 1; }
command -v kubectl >/dev/null 2>&1 || { echo "kubectl not found in PATH"; exit 1; }
command -v helm   >/dev/null 2>&1 || { echo "helm not found in PATH"; exit 1; }

echo "=== Creating single-node kind cluster '$CLUSTER_NAME' ==="
if ! kind get clusters | grep -q "^${CLUSTER_NAME}\$"; then
  kind create cluster --name "${CLUSTER_NAME}"
else
  echo "Cluster ${CLUSTER_NAME} already exists, skipping creation."
fi

# Ensure kubectl context
$KUBECTL cluster-info --context "kind-${CLUSTER_NAME}"

echo "=== Creating namespaces ==="
$KUBECTL get ns openbao >/dev/null 2>&1 || kubectl create namespace openbao
$KUBECTL get ns hashicorp >/dev/null 2>&1 || kubectl create namespace hashicorp

echo "=== Adding Helm repositories ==="
helm repo add openbao https://openbao.github.io/openbao-helm >/dev/null 2>&1 || true
helm repo add hashicorp https://helm.releases.hashicorp.com >/dev/null 2>&1 || true
helm repo update 2> /dev/null  || true

echo "=== Installing OpenBao (single dev pod) in 'openbao' namespace ==="
# OpenBao Helm chart supports dev mode via server.dev.enabled=true for a single in-memory server.[web:3][web:7][web:11]
helm upgrade --install openbao openbao/openbao \
  --namespace openbao \
  --set "server.dev.enabled=true" \
  --set "server.ha.enabled=false" \
  --wait

echo "=== Installing HashiCorp Vault (single dev pod) in 'hashicorp' namespace ==="
# Vault Helm chart can run a single dev server via server.dev.enabled=true for demo use.[web:16][web:20]
helm upgrade --install vault hashicorp/vault \
  --namespace hashicorp \
  --set "server.dev.enabled=true" \
  --set "server.ha.enabled=false" \
  --wait

echo "=== Waiting for pods to be Ready ==="
$KUBECTL wait --for=condition=Ready pod -l app.kubernetes.io/name=openbao -n openbao --timeout=120s
$KUBECTL wait --for=condition=Ready pod -l app.kubernetes.io/name=vault -n hashicorp --timeout=120s

echo "=== Done ==="
echo "OpenBao namespace:   openbao"
echo "Vault namespace:     hashicorp"
echo
echo "OpenBao pods:"
$KUBECTL get pods -n openbao
echo
echo "Vault pods:"
$KUBECTL get pods -n hashicorp

echo "=== Configuring Kubernetes auth for OpenBao ==="
OPENBAO_POD="$(kubectl get pod -n openbao -l app.kubernetes.io/name=openbao -o jsonpath='{.items[0].metadata.name}')"

# Enable Kubernetes auth method in OpenBao and configure it to talk to the K8s API.[web:21][web:22][web:25]
# In dev mode, OpenBao uses "root" as the default token
$KUBECTL exec -n openbao "$OPENBAO_POD" -- sh -c '
  export BAO_TOKEN="root"
  export BAO_ADDR="http://127.0.0.1:8200"

  bao auth enable kubernetes || true

  bao write auth/kubernetes/config \
    kubernetes_host="https://$KUBERNETES_SERVICE_HOST:$KUBERNETES_SERVICE_PORT" \
    disable_iss_validation=true
'

echo "=== Configuring Kubernetes auth for HashiCorp Vault ==="
VAULT_POD="$(kubectl get pod -n hashicorp -l app.kubernetes.io/name=vault -o jsonpath='{.items[0].metadata.name}')"

# Enable Kubernetes auth method in Vault and configure it to talk to the K8s API.[web:26][web:28][web:29][web:32]
# In dev mode, HashiCorp Vault uses "root" as the default token
$KUBECTL exec -n hashicorp "$VAULT_POD" -- sh -c '
  export VAULT_TOKEN="root"
  export VAULT_ADDR="http://127.0.0.1:8200"

  vault auth enable kubernetes || true

  vault write auth/kubernetes/config \
    kubernetes_host="https://$KUBERNETES_SERVICE_HOST:$KUBERNETES_SERVICE_PORT" \
    disable_iss_validation=true
'

echo "=== Kubernetes auth enabled on both OpenBao and Vault (basic config) ==="

echo ""
echo "=== Connection Information ==="
echo ""
echo "OpenBao (dev mode):"
echo "  Namespace: openbao"
echo "  Root Token: root"
echo "  Port-forward: kubectl port-forward -n openbao svc/openbao 8200:8200"
echo "  Connect: export BAO_ADDR=http://localhost:8200 BAO_TOKEN=root"
echo ""
echo "HashiCorp Vault (dev mode):"
echo "  Namespace: hashicorp"
echo "  Root Token: root"
echo "  Port-forward: kubectl port-forward -n hashicorp svc/vault 8201:8200"
echo "  Connect: export VAULT_ADDR=http://localhost:8201 VAULT_TOKEN=root"

echo "=== Adding Vals Opetrator CRD ==="
for crd in config/crd/bases/*.yaml; do
  echo "Applying CRD: $crd"
  $KUBECTL apply -f "$crd"
done