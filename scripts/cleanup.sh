#!/bin/bash
#
# KubeBao - Cleanup Script
# ========================
# Полная очистка установки KubeBao и связанных ресурсов
#
# Использование: ./scripts/cleanup.sh [options]
#   --all           Удалить всё включая Minikube
#   --keep-openbao  Оставить OpenBao
#   --force         Не спрашивать подтверждение
#

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# Параметры
DELETE_ALL=false
KEEP_OPENBAO=false
FORCE=false

for arg in "$@"; do
    case $arg in
        --all) DELETE_ALL=true ;;
        --keep-openbao) KEEP_OPENBAO=true ;;
        --force) FORCE=true ;;
        --help|-h)
            echo "Использование: $0 [options]"
            echo "  --all           Удалить всё включая Minikube"
            echo "  --keep-openbao  Оставить OpenBao"
            echo "  --force         Не спрашивать подтверждение"
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

info() { echo -e "${GREEN}[INFO]${NC} $1"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; }
step() { echo -e "\n${CYAN}${BOLD}━━━ $1 ━━━${NC}\n"; }
done_step() { echo -e "  ${GREEN}✓${NC} $1"; }

echo ""
echo -e "${RED}${BOLD}"
echo "╔═══════════════════════════════════════════════════════════════════╗"
echo "║                                                                   ║"
echo "║                    KubeBao Cleanup Script                         ║"
echo "║                                                                   ║"
echo "╚═══════════════════════════════════════════════════════════════════╝"
echo -e "${NC}"

# Подтверждение
if [ "$FORCE" = false ]; then
    echo -e "${YELLOW}Это удалит:${NC}"
    echo "  - KubeBao (Helm release)"
    echo "  - Secrets Store CSI Driver"
    echo "  - Все BaoSecret и BaoPolicy ресурсы"
    echo "  - Namespace kubebao-system"
    if [ "$KEEP_OPENBAO" = false ]; then
        echo "  - OpenBao и namespace openbao"
    fi
    if [ "$DELETE_ALL" = true ]; then
        echo "  - Minikube кластер"
        echo "  - Docker образы KubeBao"
    fi
    echo ""
    read -p "Продолжить? (y/N) " -n 1 -r
    echo ""
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "Отменено"
        exit 0
    fi
fi

# Проверка доступности кластера
if ! kubectl cluster-info &>/dev/null; then
    warn "Kubernetes кластер недоступен, пропускаем Kubernetes очистку"
    SKIP_K8S=true
else
    SKIP_K8S=false
fi

# =====================================================
# Шаг 1: Удаление тестовых ресурсов
# =====================================================
if [ "$SKIP_K8S" = false ]; then

step "Шаг 1: Удаление тестовых ресурсов"

# Демо приложение
kubectl delete -f "$PROJECT_DIR/examples/dynamic-secrets-demo.yaml" --ignore-not-found &>/dev/null || true
done_step "Демо приложение удалено"

# BaoSecrets в default namespace
kubectl delete baosecrets --all -n default --ignore-not-found &>/dev/null || true
done_step "BaoSecrets удалены"

# BaoPolicies в default namespace
kubectl delete baopolicies --all -n default --ignore-not-found &>/dev/null || true
done_step "BaoPolicies удалены"

# SecretProviderClasses
kubectl delete secretproviderclasses --all -n default --ignore-not-found &>/dev/null || true
done_step "SecretProviderClasses удалены"

# Тестовые секреты
kubectl delete secrets -l managed-by=kubebao -n default --ignore-not-found &>/dev/null || true
kubectl delete secrets -l kubebao.io/managed-by=kubebao-operator -n default --ignore-not-found &>/dev/null || true
done_step "Управляемые секреты удалены"

# =====================================================
# Шаг 2: Удаление KubeBao
# =====================================================
step "Шаг 2: Удаление KubeBao"

# Удаляем Helm release
if helm list -n kubebao-system 2>/dev/null | grep -q kubebao; then
    helm uninstall kubebao -n kubebao-system --wait || true
    done_step "Helm release kubebao удалён"
else
    info "Helm release kubebao не найден"
fi

# Удаляем остаточные ресурсы
kubectl delete daemonsets -l app.kubernetes.io/name=kubebao -n kubebao-system --ignore-not-found &>/dev/null || true
kubectl delete deployments -l app.kubernetes.io/name=kubebao -n kubebao-system --ignore-not-found &>/dev/null || true
kubectl delete services -l app.kubernetes.io/name=kubebao -n kubebao-system --ignore-not-found &>/dev/null || true
kubectl delete configmaps -l app.kubernetes.io/name=kubebao -n kubebao-system --ignore-not-found &>/dev/null || true
kubectl delete secrets -l app.kubernetes.io/name=kubebao -n kubebao-system --ignore-not-found &>/dev/null || true
kubectl delete serviceaccounts kubebao -n kubebao-system --ignore-not-found &>/dev/null || true
done_step "Ресурсы KubeBao удалены"

# =====================================================
# Шаг 3: Удаление Secrets Store CSI Driver
# =====================================================
step "Шаг 3: Удаление Secrets Store CSI Driver"

if helm list -n kubebao-system 2>/dev/null | grep -q csi-secrets-store; then
    helm uninstall csi-secrets-store -n kubebao-system --wait || true
    done_step "CSI Driver удалён"
else
    info "CSI Driver не найден"
fi

# =====================================================
# Шаг 4: Удаление CRDs
# =====================================================
step "Шаг 4: Удаление CRDs"

kubectl delete crd baosecrets.kubebao.io --ignore-not-found &>/dev/null || true
kubectl delete crd baopolicies.kubebao.io --ignore-not-found &>/dev/null || true
done_step "KubeBao CRDs удалены"

# CSI Driver CRDs (осторожно, могут использоваться другими)
# kubectl delete crd secretproviderclasses.secrets-store.csi.x-k8s.io --ignore-not-found &>/dev/null || true
# kubectl delete crd secretproviderclasspodstatuses.secrets-store.csi.x-k8s.io --ignore-not-found &>/dev/null || true
info "CSI Driver CRDs оставлены (могут использоваться другими)"

# =====================================================
# Шаг 5: Удаление namespace kubebao-system
# =====================================================
step "Шаг 5: Удаление namespace kubebao-system"

# Удаляем все ресурсы в namespace
kubectl delete all --all -n kubebao-system --ignore-not-found &>/dev/null || true

# Удаляем namespace
kubectl delete namespace kubebao-system --ignore-not-found --timeout=60s &>/dev/null || {
    warn "Не удалось удалить namespace kubebao-system, пробуем принудительно..."
    kubectl patch namespace kubebao-system -p '{"metadata":{"finalizers":null}}' --type=merge &>/dev/null || true
    kubectl delete namespace kubebao-system --force --grace-period=0 &>/dev/null || true
}
done_step "Namespace kubebao-system удалён"

# =====================================================
# Шаг 6: Удаление RBAC
# =====================================================
step "Шаг 6: Удаление RBAC"

kubectl delete clusterrole kubebao-manager-role --ignore-not-found &>/dev/null || true
kubectl delete clusterrolebinding kubebao-manager-rolebinding --ignore-not-found &>/dev/null || true
kubectl delete clusterrole kubebao-proxy-role --ignore-not-found &>/dev/null || true
kubectl delete clusterrolebinding kubebao-proxy-rolebinding --ignore-not-found &>/dev/null || true
done_step "RBAC удалён"

# =====================================================
# Шаг 7: Удаление OpenBao
# =====================================================
if [ "$KEEP_OPENBAO" = false ]; then
    step "Шаг 7: Удаление OpenBao"
    
    # Убиваем port-forward
    pkill -f "kubectl port-forward.*openbao" &>/dev/null || true
    done_step "Port-forward остановлен"
    
    # Удаляем ресурсы
    kubectl delete all --all -n openbao --ignore-not-found &>/dev/null || true
    kubectl delete clusterrolebinding openbao-tokenreview --ignore-not-found &>/dev/null || true
    
    # Удаляем namespace
    kubectl delete namespace openbao --ignore-not-found --timeout=60s &>/dev/null || {
        kubectl patch namespace openbao -p '{"metadata":{"finalizers":null}}' --type=merge &>/dev/null || true
        kubectl delete namespace openbao --force --grace-period=0 &>/dev/null || true
    }
    done_step "OpenBao удалён"
else
    info "OpenBao оставлен (--keep-openbao)"
fi

fi # end SKIP_K8S

# =====================================================
# Шаг 8: Удаление Docker образов
# =====================================================
if [ "$DELETE_ALL" = true ]; then
    step "Шаг 8: Удаление Docker образов"
    
    # Настраиваем Docker на Minikube
    if minikube status &>/dev/null; then
        eval $(minikube docker-env) 2>/dev/null || true
    fi
    
    docker rmi kubebao/kubebao-kms:dev &>/dev/null || true
    docker rmi kubebao/kubebao-csi:dev &>/dev/null || true
    docker rmi kubebao/kubebao-operator:dev &>/dev/null || true
    done_step "Docker образы удалены"
fi

# =====================================================
# Шаг 9: Удаление Minikube
# =====================================================
if [ "$DELETE_ALL" = true ]; then
    step "Шаг 9: Удаление Minikube"
    
    minikube stop &>/dev/null || true
    minikube delete &>/dev/null || true
    done_step "Minikube удалён"
fi

# =====================================================
# Шаг 10: Очистка локальных файлов
# =====================================================
step "Шаг 10: Очистка локальных файлов"

rm -rf "$PROJECT_DIR/bin" &>/dev/null || true
done_step "Директория bin удалена"

# Helm dependencies
rm -rf "$PROJECT_DIR/charts/kubebao/charts" &>/dev/null || true
rm -f "$PROJECT_DIR/charts/kubebao/Chart.lock" &>/dev/null || true
done_step "Helm зависимости удалены"

# =====================================================
# Завершение
# =====================================================
echo ""
echo -e "${GREEN}${BOLD}"
echo "╔═══════════════════════════════════════════════════════════════════╗"
echo "║                                                                   ║"
echo "║                  ✓ Очистка завершена успешно!                     ║"
echo "║                                                                   ║"
echo "╚═══════════════════════════════════════════════════════════════════╝"
echo -e "${NC}"

if [ "$DELETE_ALL" = false ]; then
    echo "Для повторной установки выполните:"
    echo "  ./scripts/setup-all.sh"
    echo ""
fi

if [ "$KEEP_OPENBAO" = true ]; then
    echo "OpenBao оставлен в namespace openbao"
    echo "Для доступа: kubectl port-forward svc/openbao 8200:8200 -n openbao"
    echo ""
fi
