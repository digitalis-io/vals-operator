#!/bin/bash
set -euo pipefail

echo "===================================="
echo "vals-operator DevContainer Setup"
echo "===================================="

# Use sudo for operations that require root (node user has NOPASSWD sudo)
SUDO=""
if [ "$(id -u)" -ne 0 ]; then
  SUDO="sudo"
fi

echo ""
echo "Detecting system architecture..."
# Detect architecture using uname
MACHINE=$(uname -m)
case "${MACHINE}" in
  x86_64)
    ARCH="amd64"
    ;;
  aarch64|arm64)
    ARCH="arm64"
    ;;
  *)
    echo "WARNING: Unsupported architecture ${MACHINE}, defaulting to amd64"
    ARCH="amd64"
    ;;
esac
echo "Architecture: ${ARCH}"

echo ""
echo "------------------------------------"
echo "Setting up bash completion..."
echo "------------------------------------"

BASH_COMPLETIONS_DIR="/usr/share/bash-completion/completions"

# Enable bash-completion in user's .bashrc
if ! grep -q "source /usr/share/bash-completion/bash_completion" ~/.bashrc 2>/dev/null; then
  echo 'source /usr/share/bash-completion/bash_completion' >> ~/.bashrc
  echo "Added bash-completion to .bashrc"
fi

echo ""
echo "------------------------------------"
echo "Installing development tools..."
echo "------------------------------------"

# Install kind (fallback if not baked into image)
if ! command -v kind &> /dev/null; then
  echo "Installing kind..."
  curl -Lo /tmp/kind "https://kind.sigs.k8s.io/dl/latest/kind-linux-${ARCH}"
  $SUDO install -o root -g root -m 0755 /tmp/kind /usr/local/bin/kind
  rm /tmp/kind
  echo "kind installed successfully"
fi

# Generate kind bash completion
if command -v kind &> /dev/null; then
  if kind completion bash | $SUDO tee "${BASH_COMPLETIONS_DIR}/kind" > /dev/null 2>&1; then
    echo "kind completion installed"
  else
    echo "WARNING: Failed to generate kind completion"
  fi
fi

# Install kubebuilder (fallback if not baked into image)
if ! command -v kubebuilder &> /dev/null; then
  echo "Installing kubebuilder..."
  curl -Lo /tmp/kubebuilder "https://github.com/kubernetes-sigs/kubebuilder/releases/latest/download/kubebuilder_linux_${ARCH}"
  $SUDO install -o root -g root -m 0755 /tmp/kubebuilder /usr/local/bin/kubebuilder
  rm /tmp/kubebuilder
  echo "kubebuilder installed successfully"
fi

# Generate kubebuilder bash completion
if command -v kubebuilder &> /dev/null; then
  if kubebuilder completion bash | $SUDO tee "${BASH_COMPLETIONS_DIR}/kubebuilder" > /dev/null 2>&1; then
    echo "kubebuilder completion installed"
  else
    echo "WARNING: Failed to generate kubebuilder completion"
  fi
fi

# Install kubectl (fallback if not provided by devcontainer feature)
if ! command -v kubectl &> /dev/null; then
  echo "Installing kubectl..."
  KUBECTL_VERSION=$(curl -Ls https://dl.k8s.io/release/stable.txt)
  curl -Lo /tmp/kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${ARCH}/kubectl"
  $SUDO install -o root -g root -m 0755 /tmp/kubectl /usr/local/bin/kubectl
  rm /tmp/kubectl
  echo "kubectl installed successfully"
fi

# Generate kubectl bash completion
if command -v kubectl &> /dev/null; then
  if kubectl completion bash | $SUDO tee "${BASH_COMPLETIONS_DIR}/kubectl" > /dev/null 2>&1; then
    echo "kubectl completion installed"
  else
    echo "WARNING: Failed to generate kubectl completion"
  fi
fi

# Generate Docker bash completion
if command -v docker &> /dev/null; then
  if docker completion bash | $SUDO tee "${BASH_COMPLETIONS_DIR}/docker" > /dev/null 2>&1; then
    echo "docker completion installed"
  else
    echo "WARNING: Failed to generate docker completion"
  fi
fi

echo ""
echo "------------------------------------"
echo "Configuring Docker environment..."
echo "------------------------------------"

# Wait for Docker to be ready
echo "Waiting for Docker to be ready..."
for i in {1..30}; do
  if docker info >/dev/null 2>&1; then
    echo "Docker is ready"
    break
  fi
  if [ "$i" -eq 30 ]; then
    echo "WARNING: Docker not ready after 30s"
  fi
  sleep 1
done

# Create kind network (ignore if already exists)
if ! docker network inspect kind >/dev/null 2>&1; then
  if docker network create kind >/dev/null 2>&1; then
    echo "Created kind network"
  else
    echo "WARNING: Failed to create kind network (may already exist)"
  fi
fi

echo ""
echo "------------------------------------"
echo "Verifying installations..."
echo "------------------------------------"
kind version
kubebuilder version
kubectl version --client
docker --version
go version

echo ""
echo "===================================="
echo "vals-operator DevContainer ready!"
echo "===================================="
echo "All development tools installed successfully."
echo "Run 'make help' to see available targets."
