# Руководство по развёртыванию и тестированию KubeBao

Это руководство описывает полный процесс сборки, развёртывания и тестирования KubeBao в локальном Kubernetes кластере (Minikube).

## Содержание

1. [Предварительные требования](#предварительные-требования)
2. [Установка Minikube](#установка-minikube)
3. [Развёртывание OpenBao](#развёртывание-openbao)
4. [Настройка OpenBao](#настройка-openbao)
5. [Сборка KubeBao](#сборка-kubebao)
6. [Развёртывание KubeBao](#развёртывание-kubebao)
7. [Тестирование](#тестирование)
8. [Устранение неполадок](#устранение-неполадок)

---

## Предварительные требования

### Установка необходимых инструментов

```bash
# macOS (через Homebrew)
brew install minikube kubectl helm go docker

# Linux (Ubuntu/Debian)
# Docker
curl -fsSL https://get.docker.com -o get-docker.sh
sudo sh get-docker.sh
sudo usermod -aG docker $USER

# kubectl
curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
sudo install -o root -g root -m 0755 kubectl /usr/local/bin/kubectl

# Helm
curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash

# Minikube
curl -LO https://storage.googleapis.com/minikube/releases/latest/minikube-linux-amd64
sudo install minikube-linux-amd64 /usr/local/bin/minikube

# Go (1.22+)
wget https://go.dev/dl/go1.22.0.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.22.0.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
source ~/.bashrc
```

### Проверка версий

```bash
minikube version   # v1.32.0+
kubectl version    # v1.29+
helm version       # v3.14+
go version         # go1.22+
docker version     # 24.0+
```

---

## Установка Minikube

### Запуск кластера

```bash
# Запуск Minikube с достаточными ресурсами
minikube start \
  --driver=docker \
  --cpus=4 \
  --memory=8192 \
  --disk-size=40g \
  --kubernetes-version=v1.29.0

# Проверка статуса
minikube status

# Включение необходимых аддонов
minikube addons enable ingress
minikube addons enable metrics-server

# Настройка Docker для использования registry Minikube
eval $(minikube docker-env)
```

### Проверка кластера

```bash
kubectl cluster-info
kubectl get nodes
kubectl get pods -A
```

---

## Развёртывание OpenBao

### Вариант 1: Через Helm (рекомендуется)

```bash
# Добавление репозитория OpenBao
helm repo add openbao https://openbao.github.io/openbao-helm
helm repo update

# Создание namespace
kubectl create namespace openbao

# Установка OpenBao в dev режиме (для тестирования)
helm install openbao openbao/openbao \
  --namespace openbao \
  --set "server.dev.enabled=true" \
  --set "server.dev.devRootToken=root" \
  --set "injector.enabled=false"

# Ожидание готовности
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=openbao -n openbao --timeout=120s

# Проверка статуса
kubectl get pods -n openbao
```

### Вариант 2: Минимальное развёртывание (YAML)

Если Helm chart недоступен, используйте манифест:

```bash
# Создание файла манифеста
cat <<EOF | kubectl apply -f -
---
apiVersion: v1
kind: Namespace
metadata:
  name: openbao
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: openbao
  namespace: openbao
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: openbao-config
  namespace: openbao
data:
  config.hcl: |
    ui = true
    
    listener "tcp" {
      address = "0.0.0.0:8200"
      tls_disable = true
    }
    
    storage "inmem" {}
    
    api_addr = "http://127.0.0.1:8200"
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

# Ожидание готовности
kubectl wait --for=condition=ready pod -l app=openbao -n openbao --timeout=120s
```

### Доступ к OpenBao

```bash
# Port-forward для локального доступа
kubectl port-forward svc/openbao 8200:8200 -n openbao &

# Проверка доступа
export BAO_ADDR="http://127.0.0.1:8200"
export BAO_TOKEN="root"

# Если установлен CLI openbao/vault
bao status
# или
vault status
```

---

## Настройка OpenBao

### Установка CLI (опционально)

```bash
# macOS
brew install openbao

# или скачать бинарник
wget https://github.com/openbao/openbao/releases/download/v2.1.0/bao_2.1.0_linux_amd64.zip
unzip bao_2.1.0_linux_amd64.zip
sudo mv bao /usr/local/bin/

# Альтернатива: использовать vault CLI (совместим)
brew install vault
```

### Создание скрипта настройки

```bash
# Создаём скрипт setup-openbao.sh
cat > setup-openbao.sh << 'SCRIPT'
#!/bin/bash
set -e

export BAO_ADDR="${BAO_ADDR:-http://127.0.0.1:8200}"
export BAO_TOKEN="${BAO_TOKEN:-root}"

# Используем vault или bao CLI
CLI="vault"
if command -v bao &> /dev/null; then
    CLI="bao"
fi

echo "=== Настройка OpenBao для KubeBao ==="
echo "Адрес: $BAO_ADDR"
echo "CLI: $CLI"

# 1. Включение Kubernetes auth
echo ">>> Включение Kubernetes auth..."
$CLI auth enable kubernetes || true

# Получаем данные из кластера
KUBE_HOST="https://kubernetes.default.svc"
KUBE_CA_CERT=$(kubectl get configmap -n kube-system kube-root-ca.crt -o jsonpath='{.data.ca\.crt}')

# Создаём ServiceAccount для OpenBao auth
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
sleep 5
TOKEN_REVIEWER_JWT=$(kubectl get secret openbao-auth-token -n openbao -o jsonpath='{.data.token}' | base64 -d)

# Настраиваем Kubernetes auth
$CLI write auth/kubernetes/config \
    kubernetes_host="$KUBE_HOST" \
    kubernetes_ca_cert="$KUBE_CA_CERT" \
    token_reviewer_jwt="$TOKEN_REVIEWER_JWT"

# 2. Включение Transit для KMS
echo ">>> Включение Transit secrets engine..."
$CLI secrets enable transit || true

# Создаём ключ для KMS
echo ">>> Создание Transit ключа kubebao-kms..."
$CLI write -f transit/keys/kubebao-kms type=aes256-gcm96

# 3. Включение KV v2 для секретов
echo ">>> Включение KV v2 secrets engine..."
$CLI secrets enable -path=secret kv-v2 || true

# 4. Создание тестовых секретов
echo ">>> Создание тестовых секретов..."
$CLI kv put secret/myapp/config \
    database_url="postgresql://user:pass@db:5432/myapp" \
    api_key="sk-test-1234567890" \
    debug="true"

$CLI kv put secret/myapp/database \
    username="dbuser" \
    password="supersecret123"

# 5. Создание политики для KubeBao
echo ">>> Создание политики kubebao-policy..."
$CLI policy write kubebao-policy - <<EOF
# Transit для KMS
path "transit/encrypt/kubebao-kms" {
  capabilities = ["update"]
}

path "transit/decrypt/kubebao-kms" {
  capabilities = ["update"]
}

path "transit/keys/kubebao-kms" {
  capabilities = ["read"]
}

# KV секреты
path "secret/data/*" {
  capabilities = ["read", "list"]
}

path "secret/metadata/*" {
  capabilities = ["read", "list"]
}

# Системные endpoint'ы
path "sys/health" {
  capabilities = ["read"]
}

path "sys/policies/acl/*" {
  capabilities = ["create", "read", "update", "delete", "list"]
}
EOF

# 6. Создание роли для KubeBao
echo ">>> Создание роли kubebao..."
$CLI write auth/kubernetes/role/kubebao \
    bound_service_account_names=kubebao,kubebao-kms,kubebao-csi,kubebao-operator \
    bound_service_account_namespaces=kubebao-system,default \
    policies=kubebao-policy \
    ttl=1h

# 7. Создание роли для тестового приложения
echo ">>> Создание роли my-app..."
$CLI write auth/kubernetes/role/my-app \
    bound_service_account_names=my-app,default \
    bound_service_account_namespaces=default \
    policies=kubebao-policy \
    ttl=1h

echo ""
echo "=== Настройка OpenBao завершена ==="
echo ""
echo "Тест Transit:"
$CLI write transit/encrypt/kubebao-kms plaintext=$(echo "hello world" | base64)
echo ""
echo "Тест KV:"
$CLI kv get secret/myapp/config

SCRIPT

chmod +x setup-openbao.sh
```

### Запуск настройки

```bash
# Убедитесь, что port-forward активен
kubectl port-forward svc/openbao 8200:8200 -n openbao &

# Подождите пару секунд
sleep 3

# Запустите скрипт настройки
./setup-openbao.sh
```

---

## Сборка KubeBao

### Клонирование и подготовка

```bash
# Перейти в директорию проекта
cd /Users/nikitacerenkov/Documents/diplom

# Скачать зависимости Go
go mod download
go mod tidy
```

### Сборка бинарных файлов

```bash
# Сборка всех компонентов
make build

# Или сборка по отдельности
make build-kms
make build-csi
make build-operator

# Проверка сборки
ls -la bin/
# Должны быть: kubebao-kms, kubebao-csi, kubebao-operator
```

### Сборка Docker образов

```bash
# Настройка Docker на использование Minikube registry
eval $(minikube docker-env)

# Сборка образов (для локального тестирования)
docker build -t kubebao/kubebao-kms:dev --build-arg COMPONENT=kubebao-kms .
docker build -t kubebao/kubebao-csi:dev --build-arg COMPONENT=kubebao-csi .
docker build -t kubebao/kubebao-operator:dev --build-arg COMPONENT=kubebao-operator .

# Проверка образов
docker images | grep kubebao
```

---

## Развёртывание KubeBao

### Подготовка Namespace

```bash
kubectl create namespace kubebao-system
```

### Установка Secrets Store CSI Driver (зависимость)

```bash
# Добавление репозитория
helm repo add secrets-store-csi-driver https://kubernetes-sigs.github.io/secrets-store-csi-driver/charts
helm repo update

# Установка CSI Driver
helm install csi-secrets-store secrets-store-csi-driver/secrets-store-csi-driver \
  --namespace kubebao-system \
  --set syncSecret.enabled=true \
  --set enableSecretRotation=true

# Ожидание готовности
kubectl wait --for=condition=ready pod -l app=secrets-store-csi-driver -n kubebao-system --timeout=120s
```

### Установка KubeBao через Helm

```bash
# Получаем IP OpenBao внутри кластера
OPENBAO_ADDR="http://openbao.openbao.svc.cluster.local:8200"

# Установка KubeBao
helm install kubebao ./charts/kubebao \
  --namespace kubebao-system \
  --set global.openbao.address="$OPENBAO_ADDR" \
  --set global.openbao.role="kubebao" \
  --set global.image.tag="dev" \
  --set global.image.pullPolicy="Never" \
  --set kms.image.repository="kubebao/kubebao-kms" \
  --set csi.image.repository="kubebao/kubebao-csi" \
  --set operator.image.repository="kubebao/kubebao-operator" \
  --set csi.driver.install=false

# Ожидание готовности
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=kubebao -n kubebao-system --timeout=120s

# Проверка статуса
kubectl get pods -n kubebao-system
kubectl get daemonsets -n kubebao-system
kubectl get deployments -n kubebao-system
```

### Проверка CRDs

```bash
# Проверка установки CRD
kubectl get crds | grep kubebao

# Должны быть:
# baopolicies.kubebao.io
# baosecrets.kubebao.io
```

---

## Тестирование

### Тест 1: Проверка компонентов

```bash
# Проверка логов KMS
kubectl logs -l app.kubernetes.io/component=kms -n kubebao-system

# Проверка логов CSI
kubectl logs -l app.kubernetes.io/component=csi -n kubebao-system

# Проверка логов Operator
kubectl logs -l app.kubernetes.io/component=operator -n kubebao-system
```

### Тест 2: BaoSecret (синхронизация секретов)

```bash
# Создание BaoSecret
cat <<EOF | kubectl apply -f -
apiVersion: kubebao.io/v1alpha1
kind: BaoSecret
metadata:
  name: test-secret
  namespace: default
spec:
  secretPath: "secret/data/myapp/config"
  target:
    name: myapp-secret
  refreshInterval: "1m"
EOF

# Проверка статуса BaoSecret
kubectl get baosecret test-secret -o yaml

# Ожидание синхронизации (30 секунд)
sleep 30

# Проверка созданного Kubernetes Secret
kubectl get secret myapp-secret -o yaml

# Декодирование данных
kubectl get secret myapp-secret -o jsonpath='{.data.api_key}' | base64 -d
echo ""
kubectl get secret myapp-secret -o jsonpath='{.data.database_url}' | base64 -d
echo ""
```

### Тест 3: BaoPolicy (создание политики)

```bash
# Создание BaoPolicy
cat <<EOF | kubectl apply -f -
apiVersion: kubebao.io/v1alpha1
kind: BaoPolicy
metadata:
  name: test-policy
  namespace: default
spec:
  policyName: "k8s-test-policy"
  rules:
    - path: "secret/data/test/*"
      capabilities:
        - read
        - list
    - path: "secret/metadata/test/*"
      capabilities:
        - read
        - list
EOF

# Проверка статуса
kubectl get baopolicy test-policy -o yaml

# Проверка политики в OpenBao
vault policy read k8s-test-policy || bao policy read k8s-test-policy
```

### Тест 4: CSI Provider (монтирование секретов в Pod)

```bash
# Создание ServiceAccount для приложения
kubectl create serviceaccount my-app

# Создание SecretProviderClass
cat <<EOF | kubectl apply -f -
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: kubebao-test
  namespace: default
spec:
  provider: kubebao
  parameters:
    roleName: "my-app"
    objects: |
      - objectName: "api-key"
        secretPath: "secret/data/myapp/config"
        secretKey: "api_key"
      - objectName: "db-password"
        secretPath: "secret/data/myapp/database"
        secretKey: "password"
  secretObjects:
    - secretName: csi-synced-secret
      type: Opaque
      data:
        - objectName: api-key
          key: API_KEY
        - objectName: db-password
          key: DB_PASSWORD
EOF

# Создание тестового Pod
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: test-app
  namespace: default
spec:
  serviceAccountName: my-app
  containers:
    - name: app
      image: busybox
      command: ["sh", "-c", "while true; do echo '=== Mounted secrets ==='; cat /mnt/secrets/*; echo '=== Env vars ==='; env | grep -E '(API_KEY|DB_PASSWORD)'; sleep 30; done"]
      volumeMounts:
        - name: secrets
          mountPath: /mnt/secrets
          readOnly: true
      env:
        - name: API_KEY
          valueFrom:
            secretKeyRef:
              name: csi-synced-secret
              key: API_KEY
        - name: DB_PASSWORD
          valueFrom:
            secretKeyRef:
              name: csi-synced-secret
              key: DB_PASSWORD
  volumes:
    - name: secrets
      csi:
        driver: secrets-store.csi.k8s.io
        readOnly: true
        volumeAttributes:
          secretProviderClass: kubebao-test
EOF

# Ожидание запуска Pod
kubectl wait --for=condition=ready pod/test-app --timeout=120s

# Проверка логов
kubectl logs test-app

# Проверка содержимого секретов
kubectl exec test-app -- cat /mnt/secrets/api-key
kubectl exec test-app -- cat /mnt/secrets/db-password
```

### Тест 5: End-to-End тест

```bash
# Создаём скрипт для полного теста
cat > e2e-test.sh << 'SCRIPT'
#!/bin/bash
set -e

echo "=== KubeBao E2E Test ==="

# Цвета для вывода
RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

pass() { echo -e "${GREEN}✓ $1${NC}"; }
fail() { echo -e "${RED}✗ $1${NC}"; exit 1; }

# Тест 1: Проверка компонентов
echo ""
echo ">>> Тест 1: Проверка компонентов KubeBao"

if kubectl get pods -n kubebao-system | grep -q "Running"; then
    pass "Компоненты KubeBao запущены"
else
    fail "Компоненты KubeBao не запущены"
fi

# Тест 2: Проверка CRDs
echo ""
echo ">>> Тест 2: Проверка CRDs"

if kubectl get crd baosecrets.kubebao.io &>/dev/null; then
    pass "CRD BaoSecret установлен"
else
    fail "CRD BaoSecret не найден"
fi

if kubectl get crd baopolicies.kubebao.io &>/dev/null; then
    pass "CRD BaoPolicy установлен"
else
    fail "CRD BaoPolicy не найден"
fi

# Тест 3: Создание и проверка BaoSecret
echo ""
echo ">>> Тест 3: BaoSecret синхронизация"

kubectl delete baosecret e2e-test-secret --ignore-not-found
kubectl delete secret e2e-secret --ignore-not-found

cat <<EOF | kubectl apply -f -
apiVersion: kubebao.io/v1alpha1
kind: BaoSecret
metadata:
  name: e2e-test-secret
spec:
  secretPath: "secret/data/myapp/database"
  target:
    name: e2e-secret
  refreshInterval: "30s"
EOF

echo "Ожидание синхронизации..."
sleep 35

if kubectl get secret e2e-secret &>/dev/null; then
    pass "Kubernetes Secret создан"
    
    USERNAME=$(kubectl get secret e2e-secret -o jsonpath='{.data.username}' | base64 -d)
    if [ "$USERNAME" == "dbuser" ]; then
        pass "Данные секрета корректны"
    else
        fail "Данные секрета некорректны: $USERNAME"
    fi
else
    fail "Kubernetes Secret не создан"
fi

# Тест 4: Проверка условий BaoSecret
echo ""
echo ">>> Тест 4: Проверка статуса BaoSecret"

STATUS=$(kubectl get baosecret e2e-test-secret -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')
if [ "$STATUS" == "True" ]; then
    pass "BaoSecret в состоянии Ready"
else
    fail "BaoSecret не в состоянии Ready: $STATUS"
fi

# Очистка
echo ""
echo ">>> Очистка тестовых ресурсов"
kubectl delete baosecret e2e-test-secret --ignore-not-found
kubectl delete secret e2e-secret --ignore-not-found

echo ""
echo "=== Все тесты пройдены успешно! ==="

SCRIPT

chmod +x e2e-test.sh
./e2e-test.sh
```

---

## Устранение неполадок

### Проверка логов

```bash
# Логи KMS
kubectl logs -l app.kubernetes.io/component=kms -n kubebao-system --tail=100

# Логи CSI
kubectl logs -l app.kubernetes.io/component=csi -n kubebao-system --tail=100

# Логи Operator
kubectl logs -l app.kubernetes.io/component=operator -n kubebao-system --tail=100

# Логи OpenBao
kubectl logs -l app=openbao -n openbao --tail=100
```

### Частые проблемы

#### 1. Pod не запускается - ImagePullBackOff

```bash
# Убедитесь, что Docker настроен на Minikube
eval $(minikube docker-env)

# Пересоберите образы
docker build -t kubebao/kubebao-operator:dev --build-arg COMPONENT=kubebao-operator .

# Проверьте наличие образа
docker images | grep kubebao
```

#### 2. Ошибка аутентификации в OpenBao

```bash
# Проверьте настройку Kubernetes auth
kubectl port-forward svc/openbao 8200:8200 -n openbao &
export BAO_ADDR="http://127.0.0.1:8200"
export BAO_TOKEN="root"

vault auth list
vault read auth/kubernetes/config
vault read auth/kubernetes/role/kubebao
```

#### 3. BaoSecret не синхронизируется

```bash
# Проверьте логи operator
kubectl logs deployment/kubebao-operator -n kubebao-system

# Проверьте события
kubectl describe baosecret <name>

# Проверьте доступ к секрету в OpenBao
vault kv get secret/myapp/config
```

#### 4. CSI Provider не работает

```bash
# Проверьте CSI Driver
kubectl get csidrivers

# Проверьте DaemonSet
kubectl get daemonset -n kubebao-system

# Проверьте события Pod
kubectl describe pod test-app
```

### Полная переустановка

```bash
# Удаление KubeBao
helm uninstall kubebao -n kubebao-system

# Удаление CSI Driver
helm uninstall csi-secrets-store -n kubebao-system

# Удаление namespace
kubectl delete namespace kubebao-system

# Удаление CRDs
kubectl delete crd baosecrets.kubebao.io baopolicies.kubebao.io

# Повторная установка
kubectl create namespace kubebao-system
# ... повторите шаги установки
```

---

## Полный скрипт автоматизации

Для удобства создан скрипт `scripts/setup-all.sh`:

```bash
cat > scripts/setup-all.sh << 'SCRIPT'
#!/bin/bash
set -e

echo "=== KubeBao Full Setup Script ==="

# 1. Запуск Minikube
echo ">>> Запуск Minikube..."
minikube start --driver=docker --cpus=4 --memory=8192 --kubernetes-version=v1.29.0

# 2. Настройка Docker
echo ">>> Настройка Docker..."
eval $(minikube docker-env)

# 3. Развёртывание OpenBao
echo ">>> Развёртывание OpenBao..."
kubectl create namespace openbao || true
helm repo add openbao https://openbao.github.io/openbao-helm || true
helm repo update
helm upgrade --install openbao openbao/openbao \
  --namespace openbao \
  --set "server.dev.enabled=true" \
  --set "server.dev.devRootToken=root" \
  --set "injector.enabled=false" \
  --wait

# 4. Настройка port-forward
echo ">>> Настройка port-forward..."
kubectl port-forward svc/openbao 8200:8200 -n openbao &
sleep 5

# 5. Настройка OpenBao
echo ">>> Настройка OpenBao..."
./setup-openbao.sh

# 6. Сборка образов
echo ">>> Сборка Docker образов..."
docker build -t kubebao/kubebao-kms:dev --build-arg COMPONENT=kubebao-kms .
docker build -t kubebao/kubebao-csi:dev --build-arg COMPONENT=kubebao-csi .
docker build -t kubebao/kubebao-operator:dev --build-arg COMPONENT=kubebao-operator .

# 7. Установка CSI Driver
echo ">>> Установка Secrets Store CSI Driver..."
kubectl create namespace kubebao-system || true
helm repo add secrets-store-csi-driver https://kubernetes-sigs.github.io/secrets-store-csi-driver/charts || true
helm repo update
helm upgrade --install csi-secrets-store secrets-store-csi-driver/secrets-store-csi-driver \
  --namespace kubebao-system \
  --set syncSecret.enabled=true \
  --wait

# 8. Установка KubeBao
echo ">>> Установка KubeBao..."
OPENBAO_ADDR="http://openbao.openbao.svc.cluster.local:8200"
helm upgrade --install kubebao ./charts/kubebao \
  --namespace kubebao-system \
  --set global.openbao.address="$OPENBAO_ADDR" \
  --set global.openbao.role="kubebao" \
  --set global.image.tag="dev" \
  --set global.image.pullPolicy="Never" \
  --set kms.image.repository="kubebao/kubebao-kms" \
  --set csi.image.repository="kubebao/kubebao-csi" \
  --set operator.image.repository="kubebao/kubebao-operator" \
  --set csi.driver.install=false \
  --wait

echo ""
echo "=== KubeBao успешно установлен! ==="
echo ""
echo "Проверка:"
echo "  kubectl get pods -n kubebao-system"
echo "  kubectl get pods -n openbao"
echo ""
echo "Запуск тестов:"
echo "  ./e2e-test.sh"

SCRIPT

chmod +x scripts/setup-all.sh
```

Запуск полной установки:

```bash
./scripts/setup-all.sh
```
