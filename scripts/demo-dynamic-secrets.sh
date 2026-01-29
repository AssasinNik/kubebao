#!/bin/bash

# KubeBao Dynamic Secrets Demo Script
# Demonstrates automatic secret rotation without pod restart

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

info() { echo -e "${GREEN}[INFO]${NC} $1"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }
step() { echo -e "\n${BLUE}=== $1 ===${NC}"; }

# Check prerequisites
check_prerequisites() {
    step "Проверка предварительных условий"
    
    if ! kubectl get pods -n kubebao-system 2>/dev/null | grep -q "Running"; then
        error "KubeBao не установлен. Запустите ./scripts/setup-all.sh"
    fi
    
    if ! kubectl get crd secretproviderclasses.secrets-store.csi.x-k8s.io &>/dev/null; then
        error "Secrets Store CSI Driver не установлен"
    fi
    
    info "✓ Все предварительные условия выполнены"
}

# Create test secrets in OpenBao
setup_openbao_secrets() {
    step "Создание тестовых секретов в OpenBao"
    
    export BAO_ADDR="http://127.0.0.1:8200"
    export BAO_TOKEN="root"
    
    # Check if port-forward is running
    if ! curl -s "$BAO_ADDR/v1/sys/health" &>/dev/null; then
        info "Запуск port-forward к OpenBao..."
        kubectl port-forward -n openbao svc/openbao 8200:8200 &
        sleep 3
    fi
    
    # Create initial secrets
    info "Создание начальных секретов..."
    bao kv put secret/myapp/database \
        username=initial_user \
        password=initial_password \
        host=db.example.com \
        port=5432
    
    bao kv put secret/myapp/config \
        api_key=initial_api_key \
        environment=development
    
    info "✓ Секреты созданы"
}

# Deploy demo application
deploy_demo() {
    step "Развёртывание демо-приложения"
    
    kubectl apply -f "$PROJECT_DIR/examples/dynamic-secrets-demo.yaml"
    
    info "Ожидание запуска приложения..."
    kubectl wait --for=condition=ready pod -l app=demo-app --timeout=120s 2>/dev/null || {
        warn "Pod не запустился, проверьте логи:"
        kubectl describe pod -l app=demo-app
        kubectl logs -l app=demo-app --tail=20 2>/dev/null || true
    }
    
    info "✓ Демо-приложение развёрнуто"
}

# Show current secrets
show_secrets() {
    step "Текущие секреты в поде"
    
    POD_NAME=$(kubectl get pod -l app=demo-app -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
    
    if [ -n "$POD_NAME" ]; then
        echo ""
        kubectl exec "$POD_NAME" -- cat /mnt/secrets/db-password 2>/dev/null && echo " <- db-password"
        kubectl exec "$POD_NAME" -- cat /mnt/secrets/db-username 2>/dev/null && echo " <- db-username"
        kubectl exec "$POD_NAME" -- cat /mnt/secrets/db-host 2>/dev/null && echo " <- db-host"
        echo ""
    else
        warn "Pod не найден"
    fi
}

# Update secrets in OpenBao
update_secrets() {
    step "Обновление секретов в OpenBao"
    
    export BAO_ADDR="http://127.0.0.1:8200"
    export BAO_TOKEN="root"
    
    TIMESTAMP=$(date +%H%M%S)
    
    info "Устанавливаем новые значения секретов..."
    bao kv put secret/myapp/database \
        username="updated_user_$TIMESTAMP" \
        password="updated_password_$TIMESTAMP" \
        host="new-db-$TIMESTAMP.example.com" \
        port=5432
    
    info "✓ Секреты обновлены в OpenBao"
    echo ""
    echo "Новые значения:"
    echo "  username: updated_user_$TIMESTAMP"
    echo "  password: updated_password_$TIMESTAMP"
    echo "  host: new-db-$TIMESTAMP.example.com"
}

# Watch for secret updates in pod
watch_secrets() {
    step "Наблюдение за обновлением секретов"
    
    POD_NAME=$(kubectl get pod -l app=demo-app -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
    
    if [ -z "$POD_NAME" ]; then
        error "Pod не найден"
    fi
    
    echo ""
    echo "Ожидаем обновления секретов в поде..."
    echo "CSI driver проверяет обновления каждые 30 секунд"
    echo "Нажмите Ctrl+C для выхода"
    echo ""
    
    for i in {1..20}; do
        echo "--- Проверка #$i ($(date +%H:%M:%S)) ---"
        echo -n "password: "
        kubectl exec "$POD_NAME" -- cat /mnt/secrets/db-password 2>/dev/null || echo "N/A"
        echo -n "username: "
        kubectl exec "$POD_NAME" -- cat /mnt/secrets/db-username 2>/dev/null || echo "N/A"
        echo ""
        sleep 15
    done
}

# Check BaoSecret operator sync
check_operator_sync() {
    step "Проверка синхронизации через Operator"
    
    echo "BaoSecret статус:"
    kubectl get baosecrets -A 2>/dev/null || echo "Нет BaoSecrets"
    
    echo ""
    echo "Синхронизированный Kubernetes Secret:"
    kubectl get secret demo-operator-synced -o yaml 2>/dev/null | grep -A 20 "data:" || echo "Secret не найден"
}

# Cleanup
cleanup() {
    step "Очистка"
    
    kubectl delete -f "$PROJECT_DIR/examples/dynamic-secrets-demo.yaml" 2>/dev/null || true
    
    info "✓ Очистка завершена"
}

# Main menu
main() {
    echo ""
    echo "╔══════════════════════════════════════════════════════════════╗"
    echo "║          KubeBao Dynamic Secrets Demo                        ║"
    echo "╚══════════════════════════════════════════════════════════════╝"
    echo ""
    echo "Команды:"
    echo "  $0 setup     - Настройка демо (секреты + приложение)"
    echo "  $0 show      - Показать текущие секреты в поде"
    echo "  $0 update    - Обновить секреты в OpenBao"
    echo "  $0 watch     - Наблюдать за обновлением секретов"
    echo "  $0 operator  - Проверить синхронизацию через Operator"
    echo "  $0 cleanup   - Удалить демо-ресурсы"
    echo "  $0 full      - Полная демонстрация"
    echo ""
    
    case "${1:-help}" in
        setup)
            check_prerequisites
            setup_openbao_secrets
            deploy_demo
            sleep 5
            show_secrets
            ;;
        show)
            show_secrets
            ;;
        update)
            update_secrets
            ;;
        watch)
            watch_secrets
            ;;
        operator)
            check_operator_sync
            ;;
        cleanup)
            cleanup
            ;;
        full)
            check_prerequisites
            setup_openbao_secrets
            deploy_demo
            
            echo ""
            info "Подождите 30 секунд для инициализации..."
            sleep 30
            
            show_secrets
            
            echo ""
            info "Теперь обновим секреты в OpenBao..."
            sleep 3
            
            update_secrets
            
            echo ""
            info "Наблюдаем за обновлением в поде (автоматическое обновление)..."
            watch_secrets
            ;;
        *)
            # Show menu
            ;;
    esac
}

main "$@"
