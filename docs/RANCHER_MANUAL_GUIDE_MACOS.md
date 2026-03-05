# Ручная настройка KubeBao с Rancher Desktop (macOS)

Подробная инструкция по развёртыванию и тестированию KubeBao вручную на macOS, шаг за шагом.

> **Важно для macOS:** при применении YAML из heredoc используйте pipe: `cat << 'EOF' | kubectl apply -f -`  
> Heredoc передаёт содержимое в stdin, `kubectl apply -f -` читает его.

---

## Часть 1: Подготовка окружения

### 1.1 Установка Rancher Desktop

1. Скачайте установщик для macOS: https://rancherdesktop.io/
2. Откройте `.dmg`, перетащите Rancher Desktop в Applications
3. Запустите Rancher Desktop (при первом запуске может потребоваться разрешение в System Preferences → Security & Privacy)
4. Откройте **Settings** (иконка шестерёнки):
   - **Kubernetes** → Kubernetes: **ON**
   - **Container runtime** → выберите **dockerd (moby)** — образы из `docker build` будут доступны кластеру
5. Нажмите **Apply & Restart**
6. Дождитесь в левом нижнем углу: **Kubernetes: Running** (обычно 2–5 минут)

### 1.2 Проверка доступа к кластеру

Откройте **Terminal** или **iTerm2**:

```bash
kubectl version --short
kubectl get nodes
```

Должен отображаться один узел в статусе `Ready`.

---

## Часть 2: Развёртывание OpenBao

### 2.1 Создание namespace

```bash
kubectl create namespace openbao
```

### 2.2 ServiceAccount и RBAC для Kubernetes auth

```bash
cat << 'EOF' | kubectl apply -f -
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
EOF
```

### 2.3 Deployment OpenBao (dev-режим)

```bash
cat << 'EOF' | kubectl apply -f -
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
          readinessProbe:
            httpGet:
              path: /v1/sys/health
              port: 8200
            initialDelaySeconds: 5
            periodSeconds: 5
          resources:
            requests:
              memory: "256Mi"
              cpu: "250m"
EOF
```

### 2.4 Service для OpenBao

```bash
cat << 'EOF' | kubectl apply -f -
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
```

### 2.5 Ожидание готовности

```bash
kubectl wait --for=condition=ready pod -l app=openbao -n openbao --timeout=180s
kubectl get pods -n openbao
```

---

## Часть 3: Настройка OpenBao

### 3.1 Port-forward для доступа с хоста

Запустите в **отдельном окне Terminal** (оставьте работать):

```bash
kubectl port-forward svc/openbao 8200:8200 -n openbao
```

### 3.2 Установка OpenBao CLI (опционально)

Через Homebrew (если установлен):
```bash
brew install openbao
```

Или скачайте бинарник: https://github.com/openbao/openbao/releases (выберите darwin/arm64 или darwin/amd64)

Переменные для CLI:
```bash
export BAO_ADDR="http://127.0.0.1:8200"
export BAO_TOKEN="root"
```

Если CLI установлен:
```bash
bao status
```

### 3.3 Включение Transit (для KMS)

Через браузер или curl:
- URL: http://127.0.0.1:8200
- Token: `root`

Или через API (curl):
```bash
curl -s -X POST "http://127.0.0.1:8200/v1/sys/mounts/transit" \
  -H "X-Vault-Token: root" -H "Content-Type: application/json" \
  -d '{"type":"transit"}'

curl -s -X POST "http://127.0.0.1:8200/v1/transit/keys/kubebao-kms" \
  -H "X-Vault-Token: root" -H "Content-Type: application/json" \
  -d '{"type":"aes256-gcm96"}'
```

### 3.4 Включение KV v2

```bash
curl -s -X POST "http://127.0.0.1:8200/v1/sys/mounts/secret" \
  -H "X-Vault-Token: root" -H "Content-Type: application/json" \
  -d '{"type":"kv","options":{"version":"2"}}'
```

### 3.5 Включение Kubernetes auth

Получите данные кластера:
```bash
KUBE_HOST="https://kubernetes.default.svc"
KUBE_CA_CERT=$(kubectl config view --raw --minify --flatten -o jsonpath='{.clusters[].cluster.certificate-authority-data}')
TOKEN_JWT=$(kubectl create token openbao -n openbao --duration=87600h)
```

Включите auth и настройте:
```bash
curl -s -X POST "http://127.0.0.1:8200/v1/sys/auth/kubernetes" \
  -H "X-Vault-Token: root" -H "Content-Type: application/json" \
  -d '{"type":"kubernetes"}'

CA_DECODED=$(echo "$KUBE_CA_CERT" | base64 -d)
AUTH_CONFIG=$(jq -n \
  --arg host "$KUBE_HOST" \
  --arg ca "$CA_DECODED" \
  --arg jwt "$TOKEN_JWT" \
  '{kubernetes_host: $host, kubernetes_ca_cert: $ca, token_reviewer_jwt: $jwt, disable_iss_validation: true}')

curl -s -X POST "http://127.0.0.1:8200/v1/auth/kubernetes/config" \
  -H "X-Vault-Token: root" -H "Content-Type: application/json" \
  -d "$AUTH_CONFIG"
```

> **Если `jq` не установлен:** `brew install jq`  
> **Если `$KUBE_CA_CERT` пустой** (Rancher/k3d): используйте body без `kubernetes_ca_cert` и добавьте `disable_local_ca_jwt: true`.

### 3.6 Создание политики

```bash
POLICY='path "secret/*" { capabilities = ["create","read","update","delete","list"] }
path "secret/data/*" { capabilities = ["create","read","update","delete","list"] }
path "secret/metadata/*" { capabilities = ["read","list","delete"] }
path "transit/*" { capabilities = ["create","read","update","list"] }
path "transit/encrypt/*" { capabilities = ["create","update"] }
path "transit/decrypt/*" { capabilities = ["create","update"] }
path "transit/keys/*" { capabilities = ["read","create","update"] }'

BODY=$(jq -n --arg policy "$POLICY" '{policy: $policy}')
curl -s -X PUT "http://127.0.0.1:8200/v1/sys/policies/acl/kubebao-policy" \
  -H "X-Vault-Token: root" -H "Content-Type: application/json" \
  -d "$BODY"
```

### 3.7 Роли для Kubernetes auth

```bash
curl -s -X POST "http://127.0.0.1:8200/v1/auth/kubernetes/role/kubebao" \
  -H "X-Vault-Token: root" -H "Content-Type: application/json" \
  -d '{
    "bound_service_account_names": "kubebao",
    "bound_service_account_namespaces": "kubebao-system",
    "policies": "kubebao-policy",
    "ttl": "1h"
  }'

curl -s -X POST "http://127.0.0.1:8200/v1/auth/kubernetes/role/my-app" \
  -H "X-Vault-Token: root" -H "Content-Type: application/json" \
  -d '{
    "bound_service_account_names": "demo-app,default",
    "bound_service_account_namespaces": "default,kubebao-system",
    "policies": "kubebao-policy",
    "ttl": "1h"
  }'
```

### 3.8 Тестовые секреты

```bash
curl -s -X POST "http://127.0.0.1:8200/v1/secret/data/myapp/database" \
  -H "X-Vault-Token: root" -H "Content-Type: application/json" \
  -d '{"data":{"username":"dbuser","password":"dbpass123","host":"db.example.com","port":"5432"}}'

curl -s -X POST "http://127.0.0.1:8200/v1/secret/data/myapp/config" \
  -H "X-Vault-Token: root" -H "Content-Type: application/json" \
  -d '{"data":{"api_key":"test_key_123","environment":"development","debug":"false"}}'

curl -s -X POST "http://127.0.0.1:8200/v1/secret/data/myapp/api" \
  -H "X-Vault-Token: root" -H "Content-Type: application/json" \
  -d '{"data":{"key":"secret_api_key_xyz"}}'
```

**Проверка:** откройте http://127.0.0.1:8200, войдите с токеном `root`, убедитесь что есть секреты в `secret/myapp/` (database, config, api).

---

## Часть 4: Сборка и установка KubeBao

### 4.1 Переход в каталог проекта

```bash
cd ~/kubebao-main  # или путь к вашему клонированному репозиторию
```

### 4.2 Сборка Docker-образов

```bash
docker build -t kubebao/kubebao-kms:dev --build-arg COMPONENT=kubebao-kms .
docker build -t kubebao/kubebao-csi:dev --build-arg COMPONENT=kubebao-csi .
docker build -t kubebao/kubebao-operator:dev --build-arg COMPONENT=kubebao-operator .
```

Проверка:
```bash
docker images | grep kubebao
```

### 4.3 Установка Secrets Store CSI Driver

```bash
kubectl create namespace kubebao-system

helm repo add secrets-store-csi-driver https://kubernetes-sigs.github.io/secrets-store-csi-driver/charts
helm repo update

helm install csi-secrets-store secrets-store-csi-driver/secrets-store-csi-driver \
  -n kubebao-system \
  --set syncSecret.enabled=true \
  --set enableSecretRotation=true \
  --set rotationPollInterval=30s \
  --wait --timeout=120s
```

### 4.4 Установка KubeBao через Helm

```bash
helm upgrade --install kubebao ./charts/kubebao \
  --namespace kubebao-system \
  --set global.openbao.address="http://openbao.openbao.svc.cluster.local:8200" \
  --set global.openbao.role=kubebao \
  --set global.image.tag=dev \
  --set global.image.pullPolicy=Never \
  --set global.image.registry="" \
  --set kms.image.repository=kubebao/kubebao-kms \
  --set csi.image.repository=kubebao/kubebao-csi \
  --set operator.image.repository=kubebao/kubebao-operator \
  --set csi.driver.install=false \
  --wait --timeout=300s
```

### 4.5 Проверка установки

```bash
kubectl get pods -n kubebao-system
kubectl get daemonsets -n kubebao-system
kubectl get deployment -n kubebao-system
```

Все поды должны быть в статусе `Running`. Подождите 1–2 минуты, если какие-то ещё создаются.

---

## Часть 5: Тестирование

### 5.1 Тест 1: BaoSecret (Operator)

Operator синхронизирует секреты из OpenBao в Kubernetes Secrets.

```bash
kubectl apply -f config/samples/baosecret_sample.yaml
```

Подождите 10–20 секунд, затем:
```bash
kubectl get baosecrets
kubectl get baosecrets -o wide
kubectl get secret my-app-secret -o yaml
```

Ожидаемый результат: BaoSecret в статусе `Ready`, создан Kubernetes Secret `my-app-secret` с данными из OpenBao.

### 5.2 Тест 2: CSI — инъекция секретов в под

Создайте SecretProviderClass и тестовый под:

```bash
kubectl apply -f config/samples/secretproviderclass_sample.yaml
```

Создайте pod (используйте `kubebao-secrets` — первый SecretProviderClass из файла):

```bash
cat << 'EOF' | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: test-csi-secrets
  namespace: default
spec:
  serviceAccountName: default
  containers:
  - name: busybox
    image: busybox:1.36
    command: ['sleep', '3600']
    volumeMounts:
    - name: secrets
      mountPath: /mnt/secrets
      readOnly: true
  volumes:
  - name: secrets
    csi:
      driver: secrets-store.csi.k8s.io
      readOnly: true
      volumeAttributes:
        secretProviderClass: "kubebao-secrets"
EOF
```

Дождитесь `Running`:
```bash
kubectl wait --for=condition=ready pod test-csi-secrets -n default --timeout=60s
```

Проверьте доступ к секретам:
```bash
kubectl exec test-csi-secrets -- cat /mnt/secrets/db-password
kubectl exec test-csi-secrets -- ls -la /mnt/secrets
```

### 5.3 Тест 3: Полный демо (BaoSecret + CSI + синхронизация)

```bash
kubectl apply -f examples/dynamic-secrets-demo.yaml
```

Подождите готовности:
```bash
kubectl get pods -l app=demo-app
kubectl wait --for=condition=ready pod -l app=demo-app -n default --timeout=120s
```

Просмотр логов (под выводит секреты каждые 30 сек):
```bash
kubectl logs -l app=demo-app -f
```

Проверка синхронизированного Secret:
```bash
kubectl get secret demo-synced-secret -o jsonpath='{.data.username}' | base64 -d
echo
kubectl get secret demo-synced-secret -o jsonpath='{.data.password}' | base64 -d
echo
```

### 5.4 Тест 4: Ротация секретов (опционально)

Измените секрет в OpenBao (через UI http://127.0.0.1:8200 или CLI).  
Через 30–60 секунд проверьте, что под `demo-app` видит новые значения:

```bash
kubectl logs -l app=demo-app --tail=20
```

---

## Часть 6: Проверка компонентов

| Компонент | Команда проверки |
|-----------|------------------|
| Operator | `kubectl logs -n kubebao-system -l app.kubernetes.io/name=kubebao-operator -f` |
| KMS | `kubectl logs -n kubebao-system -l app=kubebao-kms --tail=50` |
| CSI | `kubectl logs -n kubebao-system -l app=kubebao-csi --tail=50` |
| OpenBao | `kubectl logs -n openbao -l app=openbao --tail=20` |

---

## Часть 7: Очистка

```bash
# Удаление тестовых ресурсов
kubectl delete pod test-csi-secrets -n default --ignore-not-found
kubectl delete -f examples/dynamic-secrets-demo.yaml --ignore-not-found
kubectl delete -f config/samples/baosecret_sample.yaml --ignore-not-found
kubectl delete -f config/samples/secretproviderclass_sample.yaml --ignore-not-found

# Удаление KubeBao и CSI Driver
helm uninstall kubebao -n kubebao-system
helm uninstall csi-secrets-store -n kubebao-system

# Удаление OpenBao
kubectl delete namespace openbao kubebao-system

# Остановить port-forward (Ctrl+C в окне, где он запущен)
```

---

## Краткая шпаргалка

```bash
# Статус всего
kubectl get pods -n openbao
kubectl get pods -n kubebao-system
kubectl get baosecrets
kubectl get baopolicies

# OpenBao UI
# http://127.0.0.1:8200  (Token: root)
# Port-forward: kubectl port-forward svc/openbao 8200:8200 -n openbao
```

---

## Требования для macOS

- **Homebrew** (рекомендуется): `brew install kubectl helm jq`
- **Docker Desktop** не требуется — Rancher Desktop включает свой Docker
- **M1/M2 (ARM64)**: Rancher Desktop и Kubernetes поддерживают Apple Silicon
