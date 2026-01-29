#!/bin/bash
#
# KubeBao - Полный скрипт установки
# ================================
# Этот скрипт выполняет полную установку KubeBao:
# - Запуск Minikube
# - Развёртывание OpenBao
# - Настройка Kubernetes auth, Transit, KV engines
# - Сборка и установка всех компонентов KubeBao
#
# Использование: ./scripts/setup-all.sh [options]
#   --skip-minikube    Пропустить запуск Minikube
#   --skip-build       Пропустить сборку образов
#   --clean            Очистить и переустановить
#

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# Параметры
SKIP_MINIKUBE=false
SKIP_BUILD=false
CLEAN_INSTALL=false

# Парсинг аргументов
for arg in "$@"; do
    case $arg in
        --skip-minikube) SKIP_MINIKUBE=true ;;
        --skip-build) SKIP_BUILD=true ;;
        --clean) CLEAN_INSTALL=true ;;
        --help|-h)
            echo "Использование: $0 [options]"
            echo "  --skip-minikube  Пропустить запуск Minikube"
            echo "  --skip-build     Пропустить сборку образов"
            echo "  --clean          Очистить и переустановить"
            exit 0
            ;;
    esac
done

# Цвета и форматирование
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

info() { echo -e "${GREEN}[INFO]${NC} $1"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }
step() { echo -e "\n${CYAN}${BOLD}━━━ $1 ━━━${NC}\n"; }
success() { echo -e "${GREEN}${BOLD}✓${NC} $1"; }

cd "$PROJECT_DIR"

echo ""
echo -e "${BLUE}${BOLD}"
echo "╔═══════════════════════════════════════════════════════════════════╗"
echo "║                                                                   ║"
echo "║   ██╗  ██╗██╗   ██╗██████╗ ███████╗██████╗  █████╗  ██████╗      ║"
echo "║   ██║ ██╔╝██║   ██║██╔══██╗██╔════╝██╔══██╗██╔══██╗██╔═══██╗     ║"
echo "║   █████╔╝ ██║   ██║██████╔╝█████╗  ██████╔╝███████║██║   ██║     ║"
echo "║   ██╔═██╗ ██║   ██║██╔══██╗██╔══╝  ██╔══██╗██╔══██║██║   ██║     ║"
echo "║   ██║  ██╗╚██████╔╝██████╔╝███████╗██████╔╝██║  ██║╚██████╔╝     ║"
echo "║   ╚═╝  ╚═╝ ╚═════╝ ╚═════╝ ╚══════╝╚═════╝ ╚═╝  ╚═╝ ╚═════╝     ║"
echo "║                                                                   ║"
echo "║              Kubernetes Secrets Management System                 ║"
echo "║                     Full Setup Script                             ║"
echo "╚═══════════════════════════════════════════════════════════════════╝"
echo -e "${NC}"
echo ""

# =====================================================
# Шаг 1: Проверка зависимостей
# =====================================================
step "Шаг 1/10: Проверка зависимостей"

check_command() {
    if command -v $1 &> /dev/null; then
        VERSION=$($1 version 2>/dev/null | head -1 || echo "installed")
        success "$1 установлен"
        return 0
    else
        echo -e "${RED}✗${NC} $1 не найден"
        return 1
    fi
}

DEPS_OK=true
check_command minikube || DEPS_OK=false
check_command kubectl || DEPS_OK=false
check_command helm || DEPS_OK=false
check_command docker || DEPS_OK=false
check_command go || DEPS_OK=false

if [ "$DEPS_OK" = false ]; then
    error "Не все зависимости установлены. Установите недостающие компоненты."
fi

# =====================================================
# Шаг 2: Запуск Minikube
# =====================================================
step "Шаг 2/10: Запуск Minikube"

if [ "$SKIP_MINIKUBE" = true ]; then
    info "Пропуск запуска Minikube (--skip-minikube)"
else
    if minikube status 2>/dev/null | grep -q "Running"; then
        success "Minikube уже запущен"
    else
        info "Запуск Minikube..."
        minikube start \
            --driver=docker \
            --cpus=4 \
            --memory=8192 \
            --disk-size=40g \
            --kubernetes-version=v1.29.0 \
            --addons=default-storageclass,storage-provisioner
        success "Minikube запущен"
    fi
fi

# Настройка Docker для использования Minikube
info "Настройка Docker для Minikube..."
eval $(minikube docker-env)
success "Docker настроен"

# Очистка при необходимости
if [ "$CLEAN_INSTALL" = true ]; then
    step "Очистка предыдущей установки"
    helm uninstall kubebao -n kubebao-system 2>/dev/null || true
    helm uninstall csi-secrets-store -n kubebao-system 2>/dev/null || true
    kubectl delete namespace kubebao-system --ignore-not-found 2>/dev/null || true
    kubectl delete namespace openbao --ignore-not-found 2>/dev/null || true
    sleep 5
    success "Очистка завершена"
fi

# =====================================================
# Шаг 3: Развёртывание OpenBao
# =====================================================
step "Шаг 3/10: Развёртывание OpenBao"

kubectl create namespace openbao 2>/dev/null || true

info "Установка OpenBao в режиме разработки..."
cat <<'EOF' | kubectl apply -f -
apiVersion: v1
kind: ServiceAccount
metadata:
  name: openbao
  namespace: openbao
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: openbao-tokenreview
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:auth-delegator
subjects:
  - kind: ServiceAccount
    name: openbao
    namespace: openbao
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: openbao
  namespace: openbao
spec:
  replicas: 1
  selector:
    matchLabels:
      app: openbao
  template:
    metadata:
      labels:
        app: openbao
    spec:
      serviceAccountName: openbao
      containers:
        - name: openbao
          image: quay.io/openbao/openbao:2.1.0
          args:
            - "server"
            - "-dev"
            - "-dev-root-token-id=root"
            - "-dev-listen-address=0.0.0.0:8200"
          ports:
            - containerPort: 8200
              name: http
          env:
            - name: BAO_DEV_ROOT_TOKEN_ID
              value: "root"
            - name: BAO_ADDR
              value: "http://127.0.0.1:8200"
          readinessProbe:
            httpGet:
              path: /v1/sys/health
              port: 8200
            initialDelaySeconds: 5
            periodSeconds: 5
          livenessProbe:
            httpGet:
              path: /v1/sys/health?standbyok=true
              port: 8200
            initialDelaySeconds: 10
            periodSeconds: 10
          resources:
            requests:
              memory: "256Mi"
              cpu: "250m"
            limits:
              memory: "512Mi"
              cpu: "500m"
---
apiVersion: v1
kind: Service
metadata:
  name: openbao
  namespace: openbao
spec:
  selector:
    app: openbao
  ports:
    - port: 8200
      targetPort: 8200
      name: http
  type: ClusterIP
EOF

info "Ожидание готовности OpenBao..."
kubectl wait --for=condition=ready pod -l app=openbao -n openbao --timeout=180s
success "OpenBao запущен"

# =====================================================
# Шаг 4: Настройка port-forward
# =====================================================
step "Шаг 4/10: Настройка доступа к OpenBao"

# Убиваем старые port-forward
pkill -f "kubectl port-forward.*openbao" 2>/dev/null || true
sleep 2

# Запускаем новый
kubectl port-forward svc/openbao 8200:8200 -n openbao &>/dev/null &
PF_PID=$!
info "Port-forward запущен (PID: $PF_PID)"
sleep 5

# Проверка доступности
if curl -s http://127.0.0.1:8200/v1/sys/health | grep -q "initialized"; then
    success "OpenBao доступен на http://127.0.0.1:8200"
else
    error "OpenBao недоступен"
fi

# =====================================================
# Шаг 5: Настройка OpenBao
# =====================================================
step "Шаг 5/10: Настройка OpenBao (Auth, Secrets Engines)"

export BAO_ADDR="http://127.0.0.1:8200"
export BAO_TOKEN="root"

# Включение Transit для KMS
info "Включение Transit secrets engine..."
bao secrets enable transit 2>/dev/null || warn "Transit уже включен"
bao write -f transit/keys/kubebao-kms type=aes256-gcm96 2>/dev/null || warn "Ключ уже существует"
success "Transit engine настроен"

# KV v2 для секретов
info "Настройка KV secrets engine..."
bao secrets enable -version=2 -path=secret kv 2>/dev/null || warn "KV уже включен"
success "KV engine настроен"

# Kubernetes auth
info "Настройка Kubernetes authentication..."
bao auth enable kubernetes 2>/dev/null || warn "Kubernetes auth уже включен"

# Получение данных для конфигурации
KUBE_HOST="https://kubernetes.default.svc"
KUBE_CA_CERT=$(kubectl config view --raw --minify --flatten -o jsonpath='{.clusters[].cluster.certificate-authority-data}' | base64 -d)
TOKEN_REVIEWER_JWT=$(kubectl create token openbao -n openbao --duration=87600h 2>/dev/null || kubectl get secret -n openbao -o jsonpath="{.items[?(@.metadata.annotations['kubernetes\.io/service-account\.name']=='openbao')].data.token}" | base64 -d)

bao write auth/kubernetes/config \
    kubernetes_host="$KUBE_HOST" \
    kubernetes_ca_cert="$KUBE_CA_CERT" \
    token_reviewer_jwt="$TOKEN_REVIEWER_JWT" \
    disable_iss_validation=true
success "Kubernetes auth настроен"

# Политики
info "Создание политик..."
cat <<'EOF' | bao policy write kubebao-policy -
# KubeBao full access policy
path "secret/*" {
  capabilities = ["create", "read", "update", "delete", "list"]
}
path "secret/data/*" {
  capabilities = ["create", "read", "update", "delete", "list"]
}
path "secret/metadata/*" {
  capabilities = ["read", "list", "delete"]
}
path "transit/*" {
  capabilities = ["create", "read", "update", "list"]
}
path "transit/encrypt/*" {
  capabilities = ["create", "update"]
}
path "transit/decrypt/*" {
  capabilities = ["create", "update"]
}
path "transit/keys/*" {
  capabilities = ["read", "create", "update"]
}
path "sys/policies/acl/*" {
  capabilities = ["create", "read", "update", "delete", "list"]
}
EOF
success "Политики созданы"

# Роли для Kubernetes auth
info "Создание ролей..."
bao write auth/kubernetes/role/kubebao \
    bound_service_account_names=kubebao \
    bound_service_account_namespaces=kubebao-system \
    policies=kubebao-policy \
    ttl=1h

bao write auth/kubernetes/role/my-app \
    bound_service_account_names="demo-app,kubebao,default" \
    bound_service_account_namespaces="default,kubebao-system" \
    policies=kubebao-policy \
    ttl=1h
success "Роли созданы"

# Тестовые секреты
info "Создание тестовых секретов..."
bao kv put secret/myapp/database \
    username=dbuser \
    password=dbpass123 \
    host=db.example.com \
    port=5432

bao kv put secret/myapp/config \
    api_key=test_api_key_123 \
    environment=production \
    debug=false
success "Тестовые секреты созданы"

# =====================================================
# Шаг 6: Сборка KubeBao
# =====================================================
step "Шаг 6/10: Сборка KubeBao"

if [ "$SKIP_BUILD" = true ]; then
    info "Пропуск сборки (--skip-build)"
else
    info "Загрузка Go зависимостей..."
    go mod download
    go mod tidy 2>/dev/null || true

    info "Сборка Docker образов..."
    
    # KMS
    docker build -t kubebao/kubebao-kms:dev --build-arg COMPONENT=kubebao-kms . 2>&1 | tail -5
    success "kubebao-kms:dev собран"
    
    # CSI
    docker build -t kubebao/kubebao-csi:dev --build-arg COMPONENT=kubebao-csi . 2>&1 | tail -5
    success "kubebao-csi:dev собран"
    
    # Operator
    docker build -t kubebao/kubebao-operator:dev --build-arg COMPONENT=kubebao-operator . 2>&1 | tail -5
    success "kubebao-operator:dev собран"
    
    info "Собранные образы:"
    docker images | grep kubebao | head -5
fi

# =====================================================
# Шаг 7: Подготовка CRDs
# =====================================================
step "Шаг 7/10: Подготовка Custom Resource Definitions"

# CRDs управляются через Helm chart (templates/crds.yaml)
# Если CRDs уже существуют без Helm меток - удаляем их
for CRD_NAME in baosecrets.kubebao.io baopolicies.kubebao.io; do
    if kubectl get crd "$CRD_NAME" &>/dev/null; then
        MANAGED_BY=$(kubectl get crd "$CRD_NAME" -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by}' 2>/dev/null)
        if [ "$MANAGED_BY" != "Helm" ]; then
            info "Удаление CRD $CRD_NAME (будет пересоздан Helm)..."
            kubectl delete crd "$CRD_NAME" --ignore-not-found &>/dev/null || true
        fi
    fi
done
success "CRDs подготовлены (будут установлены через Helm)"

# =====================================================
# Шаг 8: Установка Secrets Store CSI Driver
# =====================================================
step "Шаг 8/10: Установка Secrets Store CSI Driver"

kubectl create namespace kubebao-system 2>/dev/null || true

helm repo add secrets-store-csi-driver https://kubernetes-sigs.github.io/secrets-store-csi-driver/charts 2>/dev/null || true
helm repo update 2>/dev/null || true

if helm list -n kubebao-system | grep -q csi-secrets-store; then
    info "Обновление CSI Driver..."
    helm upgrade csi-secrets-store secrets-store-csi-driver/secrets-store-csi-driver \
        --namespace kubebao-system \
        --set syncSecret.enabled=true \
        --set enableSecretRotation=true \
        --set rotationPollInterval=30s \
        --wait --timeout=120s
else
    info "Установка CSI Driver..."
    helm install csi-secrets-store secrets-store-csi-driver/secrets-store-csi-driver \
        --namespace kubebao-system \
        --set syncSecret.enabled=true \
        --set enableSecretRotation=true \
        --set rotationPollInterval=30s \
        --wait --timeout=120s
fi
success "Secrets Store CSI Driver установлен"

# =====================================================
# Шаг 9: Установка KubeBao
# =====================================================
step "Шаг 9/10: Установка KubeBao"

OPENBAO_ADDR="http://openbao.openbao.svc.cluster.local:8200"

helm upgrade --install kubebao ./charts/kubebao \
    --namespace kubebao-system \
    --set global.openbao.address="$OPENBAO_ADDR" \
    --set global.openbao.role="kubebao" \
    --set global.image.tag="dev" \
    --set global.image.pullPolicy="Never" \
    --set global.image.registry="" \
    --set kms.image.repository="kubebao/kubebao-kms" \
    --set csi.image.repository="kubebao/kubebao-csi" \
    --set operator.image.repository="kubebao/kubebao-operator" \
    --set csi.driver.install=false \
    --wait --timeout=300s
success "KubeBao установлен"

# =====================================================
# Шаг 10: Проверка установки
# =====================================================
step "Шаг 10/10: Проверка установки"

info "Статус OpenBao:"
kubectl get pods -n openbao

info "Статус KubeBao:"
kubectl get pods -n kubebao-system

info "Custom Resource Definitions:"
kubectl get crds | grep kubebao

# Проверка компонентов
ALL_OK=true

if kubectl get deployment kubebao-operator -n kubebao-system -o jsonpath='{.status.readyReplicas}' 2>/dev/null | grep -q "1"; then
    success "Operator работает"
else
    warn "Operator не готов"
    ALL_OK=false
fi

if kubectl get daemonset kubebao-kms -n kubebao-system -o jsonpath='{.status.numberReady}' 2>/dev/null | grep -q "1"; then
    success "KMS Plugin работает"
else
    warn "KMS Plugin не готов"
    ALL_OK=false
fi

if kubectl get daemonset kubebao-csi -n kubebao-system -o jsonpath='{.status.numberReady}' 2>/dev/null | grep -q "1"; then
    success "CSI Provider работает"
else
    warn "CSI Provider не готов"
    ALL_OK=false
fi

# Финальное сообщение
echo ""
echo -e "${BLUE}${BOLD}"
echo "╔═══════════════════════════════════════════════════════════════════╗"
if [ "$ALL_OK" = true ]; then
echo "║               ✓ KubeBao успешно установлен!                       ║"
else
echo "║           ⚠ KubeBao установлен с предупреждениями                 ║"
fi
echo "╠═══════════════════════════════════════════════════════════════════╣"
echo "║                                                                   ║"
echo "║  Полезные команды:                                                ║"
echo "║    kubectl get pods -n kubebao-system    # Статус компонентов     ║"
echo "║    kubectl get baosecrets                # Список BaoSecret       ║"
echo "║    kubectl get baopolicies               # Список BaoPolicy       ║"
echo "║                                                                   ║"
echo "║  Запуск тестов:                                                   ║"
echo "║    ./scripts/e2e-test.sh                                          ║"
echo "║                                                                   ║"
echo "║  OpenBao UI: http://127.0.0.1:8200  (Token: root)                 ║"
echo "║                                                                   ║"
echo "╚═══════════════════════════════════════════════════════════════════╝"
echo -e "${NC}"
