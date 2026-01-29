#!/bin/bash

# KubeBao KMS Encryption Setup for Minikube
# This script configures Kubernetes API server to use KubeBao KMS plugin
# for encrypting Secrets at rest in etcd

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info() { echo -e "${GREEN}[INFO]${NC} $1"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }

# Check if minikube is running
if ! minikube status &>/dev/null; then
    error "Minikube не запущен. Запустите: minikube start"
fi

info "=== Настройка KMS шифрования для Kubernetes ==="

# Get KMS socket path on minikube node
KMS_SOCKET_PATH="/var/run/kubebao/kms.sock"

# Check if KMS plugin is running
if ! kubectl get pods -n kubebao-system -l app.kubernetes.io/component=kms --no-headers 2>/dev/null | grep -q "Running"; then
    error "KubeBao KMS plugin не запущен. Установите KubeBao сначала."
fi

info "KubeBao KMS plugin запущен"

# Create EncryptionConfiguration
cat > /tmp/encryption-config.yaml << 'EOF'
apiVersion: apiserver.config.k8s.io/v1
kind: EncryptionConfiguration
resources:
  - resources:
      - secrets
    providers:
      # KubeBao KMS provider (primary - encrypts new secrets)
      - kms:
          apiVersion: v2
          name: kubebao-kms
          endpoint: unix:///var/run/kubebao/kms.sock
          timeout: 10s
      # Fallback to identity for reading old unencrypted secrets
      - identity: {}
EOF

info "Создана конфигурация шифрования"

# Copy encryption config to minikube
info "Копирование конфигурации в Minikube..."
minikube cp /tmp/encryption-config.yaml /var/lib/minikube/certs/encryption-config.yaml

# Create directory for KMS socket on minikube node if not exists
minikube ssh "sudo mkdir -p /var/run/kubebao && sudo chmod 755 /var/run/kubebao" || true

info "Для активации KMS шифрования необходимо перезапустить Minikube с дополнительными параметрами."
echo ""
echo "Выполните следующие команды:"
echo ""
echo -e "${YELLOW}# 1. Остановите Minikube${NC}"
echo "minikube stop"
echo ""
echo -e "${YELLOW}# 2. Запустите с параметрами KMS${NC}"
echo 'minikube start --extra-config=apiserver.encryption-provider-config=/var/lib/minikube/certs/encryption-config.yaml'
echo ""
echo -e "${YELLOW}# 3. После запуска переустановите KubeBao${NC}"
echo "helm upgrade kubebao ./charts/kubebao -n kubebao-system"
echo ""
echo -e "${YELLOW}# 4. Проверьте что шифрование работает${NC}"
echo "kubectl create secret generic test-encrypted --from-literal=key=value"
echo "kubectl get secret test-encrypted -o yaml"
echo ""

info "Альтернативный способ - использовать существующий кластер без перезапуска:"
echo ""
echo "KubeBao Operator уже синхронизирует секреты из OpenBao в Kubernetes."
echo "Секреты хранятся в OpenBao (зашифрованы), а в Kubernetes создаются по требованию."
echo ""
echo "Для максимальной безопасности рекомендуется:"
echo "1. Использовать CSI driver для прямой инъекции секретов в поды"
echo "2. Не хранить секреты в Kubernetes вообще (только в OpenBao)"
echo ""

info "=== Настройка завершена ==="
