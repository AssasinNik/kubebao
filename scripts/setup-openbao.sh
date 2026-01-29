#!/bin/bash
#
# Скрипт настройки OpenBao для KubeBao
# Использование: ./setup-openbao.sh
#

set -e

export BAO_ADDR="${BAO_ADDR:-http://127.0.0.1:8200}"
export BAO_TOKEN="${BAO_TOKEN:-root}"

# Цвета для вывода
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info() { echo -e "${GREEN}[INFO]${NC} $1"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }

# Определяем CLI (vault или bao)
CLI="vault"
if command -v bao &> /dev/null; then
    CLI="bao"
elif ! command -v vault &> /dev/null; then
    error "Требуется vault или bao CLI. Установите один из них."
fi

info "=== Настройка OpenBao для KubeBao ==="
info "Адрес OpenBao: $BAO_ADDR"
info "Используемый CLI: $CLI"

# Проверка доступности OpenBao
info "Проверка доступности OpenBao..."
if ! $CLI status &>/dev/null; then
    error "OpenBao недоступен по адресу $BAO_ADDR"
fi
info "OpenBao доступен"

# 1. Включение Kubernetes auth
info ">>> Включение Kubernetes auth..."
$CLI auth enable kubernetes 2>/dev/null || warn "Kubernetes auth уже включен"

# Получаем данные для настройки Kubernetes auth
info "Получение данных кластера Kubernetes..."
KUBE_HOST="https://kubernetes.default.svc"
KUBE_CA_CERT=$(kubectl get configmap -n kube-system kube-root-ca.crt -o jsonpath='{.data.ca\.crt}' 2>/dev/null || kubectl config view --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' | base64 -d)

# Создаём ServiceAccount для OpenBao auth
info "Создание ServiceAccount для OpenBao auth..."
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ServiceAccount
metadata:
  name: openbao-auth
  namespace: openbao
---
apiVersion: v1
kind: Secret
metadata:
  name: openbao-auth-token
  namespace: openbao
  annotations:
    kubernetes.io/service-account.name: openbao-auth
type: kubernetes.io/service-account-token
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: openbao-auth-delegator
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:auth-delegator
subjects:
  - kind: ServiceAccount
    name: openbao-auth
    namespace: openbao
EOF

# Ждём создания токена
info "Ожидание создания токена..."
sleep 5

TOKEN_REVIEWER_JWT=$(kubectl get secret openbao-auth-token -n openbao -o jsonpath='{.data.token}' | base64 -d)

if [ -z "$TOKEN_REVIEWER_JWT" ]; then
    error "Не удалось получить токен ServiceAccount"
fi

# Настраиваем Kubernetes auth
info "Настройка Kubernetes auth backend..."
$CLI write auth/kubernetes/config \
    kubernetes_host="$KUBE_HOST" \
    kubernetes_ca_cert="$KUBE_CA_CERT" \
    token_reviewer_jwt="$TOKEN_REVIEWER_JWT"

# 2. Включение Transit для KMS
info ">>> Включение Transit secrets engine..."
$CLI secrets enable transit 2>/dev/null || warn "Transit уже включен"

# Создаём ключ для KMS
info "Создание Transit ключа kubebao-kms..."
$CLI write -f transit/keys/kubebao-kms type=aes256-gcm96 2>/dev/null || warn "Ключ kubebao-kms уже существует"

# 3. Включение KV v2 для секретов
info ">>> Включение KV v2 secrets engine..."
$CLI secrets enable -path=secret kv-v2 2>/dev/null || warn "KV v2 уже включен"

# 4. Создание тестовых секретов
info ">>> Создание тестовых секретов..."
$CLI kv put secret/myapp/config \
    database_url="postgresql://user:pass@db:5432/myapp" \
    api_key="sk-test-1234567890abcdef" \
    debug="true"

$CLI kv put secret/myapp/database \
    username="dbuser" \
    password="supersecret123"

$CLI kv put secret/myapp/api \
    key="api-key-12345" \
    secret="api-secret-67890"

# 5. Создание политики для KubeBao
info ">>> Создание политики kubebao-policy..."
$CLI policy write kubebao-policy - <<EOF
# Политика для KubeBao

# Transit для KMS шифрования
path "transit/encrypt/kubebao-kms" {
  capabilities = ["update"]
}

path "transit/decrypt/kubebao-kms" {
  capabilities = ["update"]
}

path "transit/keys/kubebao-kms" {
  capabilities = ["read", "create", "update"]
}

# KV секреты - чтение
path "secret/data/*" {
  capabilities = ["read", "list"]
}

path "secret/metadata/*" {
  capabilities = ["read", "list"]
}

# Системные endpoints
path "sys/health" {
  capabilities = ["read"]
}

path "sys/policies/acl/*" {
  capabilities = ["create", "read", "update", "delete", "list"]
}

# Auth endpoints
path "auth/token/lookup-self" {
  capabilities = ["read"]
}

path "auth/token/renew-self" {
  capabilities = ["update"]
}
EOF

# 6. Создание роли для KubeBao компонентов
info ">>> Создание роли kubebao..."
$CLI write auth/kubernetes/role/kubebao \
    bound_service_account_names="kubebao,kubebao-kms,kubebao-csi,kubebao-operator,default" \
    bound_service_account_namespaces="kubebao-system,default,kube-system" \
    policies="kubebao-policy" \
    ttl="1h" \
    max_ttl="24h"

# 7. Создание роли для тестового приложения
info ">>> Создание роли my-app..."
$CLI write auth/kubernetes/role/my-app \
    bound_service_account_names="my-app,default" \
    bound_service_account_namespaces="default" \
    policies="kubebao-policy" \
    ttl="1h"

# 8. Проверка настройки
echo ""
info "=== Проверка настройки ==="

info "Тест Transit шифрования:"
ENCRYPTED=$($CLI write -format=json transit/encrypt/kubebao-kms plaintext=$(echo "test message" | base64) | grep ciphertext)
if [ -n "$ENCRYPTED" ]; then
    echo -e "${GREEN}✓${NC} Transit шифрование работает"
else
    echo -e "${RED}✗${NC} Ошибка Transit шифрования"
fi

info "Тест KV секретов:"
SECRET=$($CLI kv get -format=json secret/myapp/config 2>/dev/null | grep api_key)
if [ -n "$SECRET" ]; then
    echo -e "${GREEN}✓${NC} KV секреты доступны"
else
    echo -e "${RED}✗${NC} Ошибка доступа к KV секретам"
fi

info "Проверка ролей:"
ROLE=$($CLI read auth/kubernetes/role/kubebao 2>/dev/null)
if [ -n "$ROLE" ]; then
    echo -e "${GREEN}✓${NC} Роль kubebao создана"
else
    echo -e "${RED}✗${NC} Роль kubebao не найдена"
fi

echo ""
info "=== Настройка OpenBao завершена успешно! ==="
echo ""
echo "Полезные команды:"
echo "  $CLI kv get secret/myapp/config     # Прочитать секрет"
echo "  $CLI policy read kubebao-policy     # Просмотреть политику"
echo "  $CLI auth list                      # Список auth методов"
echo ""
