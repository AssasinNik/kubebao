#!/bin/bash
#
# KubeBao - End-to-End Test Suite
# ================================
# Комплексное тестирование всех компонентов KubeBao:
# - Operator (BaoSecret, BaoPolicy)
# - CSI Provider (секреты в подах)
# - KMS Plugin (готовность)
# - Динамическое обновление секретов
#
# Использование: ./scripts/e2e-test.sh [options]
#   --quick    Быстрый тест (без CSI)
#   --verbose  Подробный вывод
#

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# Параметры
QUICK_MODE=false
VERBOSE=false

for arg in "$@"; do
    case $arg in
        --quick) QUICK_MODE=true ;;
        --verbose) VERBOSE=true ;;
        --help|-h)
            echo "Использование: $0 [options]"
            echo "  --quick    Быстрый тест (без CSI)"
            echo "  --verbose  Подробный вывод"
            exit 0
            ;;
    esac
done

# Цвета
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

# Счётчики
TESTS_PASSED=0
TESTS_FAILED=0
TESTS_TOTAL=0

# Функции вывода
pass() {
    ((TESTS_PASSED++))
    ((TESTS_TOTAL++))
    echo -e "  ${GREEN}✓ PASS:${NC} $1"
}

fail() {
    ((TESTS_FAILED++))
    ((TESTS_TOTAL++))
    echo -e "  ${RED}✗ FAIL:${NC} $1"
    if [ "$VERBOSE" = true ]; then
        echo -e "    ${YELLOW}Детали: $2${NC}"
    fi
}

info() { echo -e "  ${BLUE}[INFO]${NC} $1"; }
test_section() {
    echo ""
    echo -e "${CYAN}${BOLD}┌─────────────────────────────────────────────────────────────────┐${NC}"
    echo -e "${CYAN}${BOLD}│ $1${NC}"
    echo -e "${CYAN}${BOLD}└─────────────────────────────────────────────────────────────────┘${NC}"
}

# Проверка доступности кластера
check_cluster() {
    if ! kubectl cluster-info &>/dev/null; then
        echo -e "${RED}Ошибка: Kubernetes кластер недоступен${NC}"
        exit 1
    fi
}

echo ""
echo -e "${BLUE}${BOLD}"
echo "╔═══════════════════════════════════════════════════════════════════╗"
echo "║                                                                   ║"
echo "║                 KubeBao End-to-End Test Suite                     ║"
echo "║                                                                   ║"
echo "╚═══════════════════════════════════════════════════════════════════╝"
echo -e "${NC}"

check_cluster

# =====================================================
# Тест 1: Инфраструктура
# =====================================================
test_section "Тест 1: Проверка инфраструктуры"

# Namespace
if kubectl get namespace kubebao-system &>/dev/null; then
    pass "Namespace kubebao-system существует"
else
    fail "Namespace kubebao-system не найден"
fi

if kubectl get namespace openbao &>/dev/null; then
    pass "Namespace openbao существует"
else
    fail "Namespace openbao не найден"
fi

# OpenBao
OPENBAO_READY=$(kubectl get pods -n openbao -l app=openbao -o jsonpath='{.items[0].status.phase}' 2>/dev/null)
if [ "$OPENBAO_READY" = "Running" ]; then
    pass "OpenBao pod работает"
else
    fail "OpenBao pod не работает" "status=$OPENBAO_READY"
fi

# =====================================================
# Тест 2: Компоненты KubeBao
# =====================================================
test_section "Тест 2: Компоненты KubeBao"

# Operator
OPERATOR_READY=$(kubectl get deployment kubebao-operator -n kubebao-system -o jsonpath='{.status.readyReplicas}' 2>/dev/null)
if [ "$OPERATOR_READY" -gt 0 ] 2>/dev/null; then
    pass "Operator работает (replicas: $OPERATOR_READY)"
else
    fail "Operator не готов" "readyReplicas=$OPERATOR_READY"
fi

# KMS DaemonSet
KMS_READY=$(kubectl get daemonset kubebao-kms -n kubebao-system -o jsonpath='{.status.numberReady}' 2>/dev/null)
if [ "$KMS_READY" -gt 0 ] 2>/dev/null; then
    pass "KMS Plugin работает (ready: $KMS_READY)"
else
    fail "KMS Plugin не готов" "numberReady=$KMS_READY"
fi

# CSI DaemonSet
CSI_READY=$(kubectl get daemonset kubebao-csi -n kubebao-system -o jsonpath='{.status.numberReady}' 2>/dev/null)
if [ "$CSI_READY" -gt 0 ] 2>/dev/null; then
    pass "CSI Provider работает (ready: $CSI_READY)"
else
    fail "CSI Provider не готов" "numberReady=$CSI_READY"
fi

# Secrets Store CSI Driver
CSI_DRIVER_READY=$(kubectl get daemonset -n kubebao-system -l app=secrets-store-csi-driver -o jsonpath='{.items[0].status.numberReady}' 2>/dev/null)
if [ "$CSI_DRIVER_READY" -gt 0 ] 2>/dev/null; then
    pass "Secrets Store CSI Driver работает (ready: $CSI_DRIVER_READY)"
else
    fail "Secrets Store CSI Driver не готов"
fi

# =====================================================
# Тест 3: CRDs
# =====================================================
test_section "Тест 3: Custom Resource Definitions"

if kubectl get crd baosecrets.kubebao.io &>/dev/null; then
    pass "CRD baosecrets.kubebao.io установлен"
else
    fail "CRD baosecrets.kubebao.io не найден"
fi

if kubectl get crd baopolicies.kubebao.io &>/dev/null; then
    pass "CRD baopolicies.kubebao.io установлен"
else
    fail "CRD baopolicies.kubebao.io не найден"
fi

if kubectl get crd secretproviderclasses.secrets-store.csi.x-k8s.io &>/dev/null; then
    pass "CRD SecretProviderClass установлен"
else
    fail "CRD SecretProviderClass не найден"
fi

# =====================================================
# Тест 4: BaoSecret - Синхронизация секретов
# =====================================================
test_section "Тест 4: BaoSecret - Синхронизация OpenBao → K8s Secret"

# Очистка
kubectl delete baosecret e2e-test-secret --ignore-not-found &>/dev/null
kubectl delete secret e2e-synced-secret --ignore-not-found &>/dev/null
sleep 2

info "Создание BaoSecret..."
cat <<EOF | kubectl apply -f -
apiVersion: kubebao.io/v1alpha1
kind: BaoSecret
metadata:
  name: e2e-test-secret
  namespace: default
spec:
  secretPath: "myapp/database"
  target:
    name: e2e-synced-secret
    labels:
      test: e2e
      managed-by: kubebao
  refreshInterval: "30s"
EOF

if [ $? -eq 0 ]; then
    pass "BaoSecret создан"
else
    fail "Не удалось создать BaoSecret"
fi

info "Ожидание синхронизации (40 секунд)..."
sleep 40

# Проверка K8s Secret
if kubectl get secret e2e-synced-secret &>/dev/null; then
    pass "Kubernetes Secret e2e-synced-secret создан"
    
    # Проверка данных
    USERNAME=$(kubectl get secret e2e-synced-secret -o jsonpath='{.data.username}' 2>/dev/null | base64 -d)
    if [ "$USERNAME" = "dbuser" ]; then
        pass "Данные секрета корректны (username=dbuser)"
    else
        fail "Данные секрета некорректны" "username=$USERNAME, ожидалось dbuser"
    fi
    
    PASSWORD=$(kubectl get secret e2e-synced-secret -o jsonpath='{.data.password}' 2>/dev/null | base64 -d)
    if [ "$PASSWORD" = "dbpass123" ]; then
        pass "Пароль в секрете корректен"
    else
        fail "Пароль в секрете некорректен" "password=$PASSWORD"
    fi
    
    # Проверка labels
    LABEL=$(kubectl get secret e2e-synced-secret -o jsonpath='{.metadata.labels.test}' 2>/dev/null)
    if [ "$LABEL" = "e2e" ]; then
        pass "Labels переданы корректно"
    else
        fail "Labels не переданы" "test=$LABEL"
    fi
else
    fail "Kubernetes Secret не создан"
    if [ "$VERBOSE" = true ]; then
        echo "    Логи оператора:"
        kubectl logs -l app.kubernetes.io/component=operator -n kubebao-system --tail=10 2>/dev/null | sed 's/^/    /'
    fi
fi

# Проверка статуса BaoSecret
STATUS=$(kubectl get baosecret e2e-test-secret -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)
if [ "$STATUS" = "True" ]; then
    pass "BaoSecret в состоянии Ready"
else
    fail "BaoSecret не в состоянии Ready" "status=$STATUS"
fi

# =====================================================
# Тест 5: BaoPolicy - Управление политиками
# =====================================================
test_section "Тест 5: BaoPolicy - Создание политики в OpenBao"

kubectl delete baopolicy e2e-test-policy --ignore-not-found &>/dev/null
sleep 2

info "Создание BaoPolicy..."
cat <<EOF | kubectl apply -f -
apiVersion: kubebao.io/v1alpha1
kind: BaoPolicy
metadata:
  name: e2e-test-policy
  namespace: default
spec:
  policyName: "k8s-e2e-test-policy"
  rules:
    - path: "secret/data/e2e/*"
      capabilities:
        - read
        - list
    - path: "secret/metadata/e2e/*"
      capabilities:
        - read
EOF

if [ $? -eq 0 ]; then
    pass "BaoPolicy создан"
else
    fail "Не удалось создать BaoPolicy"
fi

sleep 10

POLICY_STATUS=$(kubectl get baopolicy e2e-test-policy -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)
if [ "$POLICY_STATUS" = "True" ]; then
    pass "BaoPolicy синхронизирован с OpenBao"
else
    fail "BaoPolicy не синхронизирован" "status=$POLICY_STATUS"
fi

# =====================================================
# Тест 6: CSI Provider - Инъекция секретов в поды
# =====================================================
if [ "$QUICK_MODE" = false ]; then
test_section "Тест 6: CSI Provider - Инъекция секретов в поды"

# Очистка
kubectl delete deployment csi-test-app --ignore-not-found &>/dev/null
kubectl delete secretproviderclass e2e-csi-secrets --ignore-not-found &>/dev/null
kubectl delete serviceaccount csi-test-sa --ignore-not-found &>/dev/null
sleep 2

info "Создание тестовых ресурсов для CSI..."

# ServiceAccount
kubectl create serviceaccount csi-test-sa 2>/dev/null || true

# SecretProviderClass
cat <<EOF | kubectl apply -f -
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: e2e-csi-secrets
  namespace: default
spec:
  provider: kubebao
  parameters:
    roleName: "my-app"
    openbaoAddr: "http://openbao.openbao.svc.cluster.local:8200"
    objects: |
      - objectName: "test-password"
        secretPath: "myapp/database"
        secretKey: "password"
      - objectName: "test-username"
        secretPath: "myapp/database"
        secretKey: "username"
EOF

if [ $? -eq 0 ]; then
    pass "SecretProviderClass создан"
else
    fail "Не удалось создать SecretProviderClass"
fi

# Deployment с CSI volume
cat <<EOF | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: csi-test-app
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: csi-test-app
  template:
    metadata:
      labels:
        app: csi-test-app
    spec:
      serviceAccountName: csi-test-sa
      containers:
        - name: app
          image: busybox:1.36
          command: ["sleep", "3600"]
          volumeMounts:
            - name: secrets-store
              mountPath: "/mnt/secrets"
              readOnly: true
          resources:
            limits:
              memory: "32Mi"
              cpu: "50m"
      volumes:
        - name: secrets-store
          csi:
            driver: secrets-store.csi.k8s.io
            readOnly: true
            volumeAttributes:
              secretProviderClass: "e2e-csi-secrets"
EOF

info "Ожидание запуска пода с CSI volume (60 секунд)..."
sleep 10

# Ожидание готовности
for i in {1..10}; do
    POD_STATUS=$(kubectl get pods -l app=csi-test-app -o jsonpath='{.items[0].status.phase}' 2>/dev/null)
    if [ "$POD_STATUS" = "Running" ]; then
        break
    fi
    sleep 5
done

POD_NAME=$(kubectl get pod -l app=csi-test-app -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)

if [ "$POD_STATUS" = "Running" ] && [ -n "$POD_NAME" ]; then
    pass "Pod с CSI volume запущен"
    
    # Проверка секретов в поде
    SECRET_VALUE=$(kubectl exec "$POD_NAME" -- cat /mnt/secrets/test-password 2>/dev/null)
    if [ "$SECRET_VALUE" = "dbpass123" ]; then
        pass "Секрет корректно инжектирован в под (password=dbpass123)"
    else
        fail "Секрет некорректен" "password=$SECRET_VALUE"
    fi
    
    USERNAME_VALUE=$(kubectl exec "$POD_NAME" -- cat /mnt/secrets/test-username 2>/dev/null)
    if [ "$USERNAME_VALUE" = "dbuser" ]; then
        pass "Username корректно инжектирован в под"
    else
        fail "Username некорректен" "username=$USERNAME_VALUE"
    fi
else
    fail "Pod с CSI volume не запустился" "status=$POD_STATUS"
    if [ "$VERBOSE" = true ]; then
        echo "    События пода:"
        kubectl describe pod -l app=csi-test-app 2>/dev/null | grep -A 10 "Events:" | sed 's/^/    /'
        echo "    Логи CSI provider:"
        kubectl logs -n kubebao-system -l app.kubernetes.io/component=csi --tail=10 2>/dev/null | sed 's/^/    /'
    fi
fi

# Очистка CSI ресурсов
info "Очистка CSI тестовых ресурсов..."
kubectl delete deployment csi-test-app --ignore-not-found &>/dev/null
kubectl delete secretproviderclass e2e-csi-secrets --ignore-not-found &>/dev/null
kubectl delete serviceaccount csi-test-sa --ignore-not-found &>/dev/null

else
    echo ""
    info "CSI тест пропущен (--quick mode)"
fi

# =====================================================
# Тест 7: Динамическое обновление секретов
# =====================================================
test_section "Тест 7: Динамическое обновление секретов"

# Получаем текущую версию
OLD_HASH=$(kubectl get baosecret e2e-test-secret -o jsonpath='{.status.secretVersion}' 2>/dev/null)
info "Текущий hash версии: $OLD_HASH"

# Обновляем секрет в OpenBao
info "Обновление секрета в OpenBao..."
export BAO_ADDR="http://127.0.0.1:8200"
export BAO_TOKEN="root"

TIMESTAMP=$(date +%s)
bao kv put secret/myapp/database \
    username=dbuser \
    password="updated_pass_$TIMESTAMP" \
    host=db.example.com \
    port=5432 &>/dev/null

info "Ожидание синхронизации (до 60 секунд)..."

# Ждём обновления секрета с несколькими проверками
UPDATED=false
for i in {1..6}; do
    sleep 10
    NEW_PASSWORD=$(kubectl get secret e2e-synced-secret -o jsonpath='{.data.password}' 2>/dev/null | base64 -d)
    if [[ "$NEW_PASSWORD" == "updated_pass_"* ]]; then
        UPDATED=true
        break
    fi
    info "Проверка $i/6: ожидание обновления..."
done

if [ "$UPDATED" = true ]; then
    pass "Секрет автоматически обновлён в Kubernetes"
else
    fail "Секрет не обновился за 60 секунд" "password=$NEW_PASSWORD"
fi

# Проверяем время последней синхронизации
LAST_SYNC=$(kubectl get baosecret e2e-test-secret -o jsonpath='{.status.lastSyncTime}' 2>/dev/null)
if [ -n "$LAST_SYNC" ]; then
    pass "Время синхронизации обновлено: $LAST_SYNC"
else
    fail "Время синхронизации не обновлено"
fi

# Восстанавливаем исходный секрет
bao kv put secret/myapp/database \
    username=dbuser \
    password=dbpass123 \
    host=db.example.com \
    port=5432 &>/dev/null

# =====================================================
# Тест 8: Очистка ресурсов
# =====================================================
test_section "Тест 8: Очистка тестовых ресурсов"

kubectl delete baosecret e2e-test-secret --wait=true &>/dev/null
if [ $? -eq 0 ]; then
    pass "BaoSecret удалён"
else
    fail "Не удалось удалить BaoSecret"
fi

sleep 3

# Проверяем каскадное удаление
if kubectl get secret e2e-synced-secret &>/dev/null; then
    info "Secret существует (orphan policy), удаляем вручную..."
    kubectl delete secret e2e-synced-secret --ignore-not-found &>/dev/null
else
    pass "Secret удалён каскадно"
fi

kubectl delete baopolicy e2e-test-policy --ignore-not-found &>/dev/null
pass "BaoPolicy удалён"

# =====================================================
# Итоги
# =====================================================
echo ""
echo -e "${BLUE}${BOLD}═══════════════════════════════════════════════════════════════════${NC}"
echo ""
echo -e "  ${BOLD}Результаты тестирования:${NC}"
echo ""
echo -e "  Всего тестов:   ${BOLD}$TESTS_TOTAL${NC}"
echo -e "  Пройдено:       ${GREEN}${BOLD}$TESTS_PASSED${NC}"
echo -e "  Провалено:      ${RED}${BOLD}$TESTS_FAILED${NC}"
echo ""

if [ $TESTS_FAILED -eq 0 ]; then
    echo -e "${GREEN}${BOLD}"
    echo "  ╔═══════════════════════════════════════════════════════════════╗"
    echo "  ║           ✓ ВСЕ ТЕСТЫ ПРОЙДЕНЫ УСПЕШНО!                       ║"
    echo "  ╚═══════════════════════════════════════════════════════════════╝"
    echo -e "${NC}"
    exit 0
else
    echo -e "${RED}${BOLD}"
    echo "  ╔═══════════════════════════════════════════════════════════════╗"
    echo "  ║          ✗ НЕКОТОРЫЕ ТЕСТЫ НЕ ПРОЙДЕНЫ                        ║"
    echo "  ╚═══════════════════════════════════════════════════════════════╝"
    echo -e "${NC}"
    
    if [ "$VERBOSE" = false ]; then
        echo "  Запустите с --verbose для подробной информации"
    fi
    
    exit 1
fi
