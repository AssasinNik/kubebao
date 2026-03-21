# Руководство по развёртыванию KubeBao

Пошаговая инструкция: от подключения к кластеру до создания тестовых секретов и проверки их шифрования.

> **Репозиторий:** [https://github.com/AssasinNik/kubebao](https://github.com/AssasinNik/kubebao)

---

## Содержание

1. [Требования и подготовка](#1-требования-и-подготовка)
2. [Подключение к кластеру Kubernetes](#2-подключение-к-кластеру-kubernetes)
3. [Развёртывание OpenBao](#3-развёртывание-openbao)
4. [Настройка OpenBao](#4-настройка-openbao)
5. [Сборка и установка KubeBao](#5-сборка-и-установка-kubebao)
6. [Настройка и доступ к KubeBao UI](#6-настройка-и-доступ-к-kubebao-ui)
7. [Настройка KMS-шифрования etcd](#7-настройка-kms-шифрования-etcd)
8. [Создание тестовых секретов и проверка](#8-создание-тестовых-секретов-и-проверка)
9. [Тестирование BaoSecret (Operator)](#9-тестирование-baosecret-operator)
10. [Тестирование CSI Provider](#10-тестирование-csi-provider)
11. [Проверка шифрования etcd](#11-проверка-шифрования-etcd)
12. [Ротация ключей](#12-ротация-ключей)
13. [Production Checklist](#13-production-checklist)
14. [Устранение неполадок](#14-устранение-неполадок)
15. [Очистка окружения](#15-очистка-окружения)

---

## 1. Требования и подготовка

### 1.1 Минимальные версии

| Компонент | Версия | Зачем |
|---|---|---|
| Kubernetes | 1.25+ | KMS Plugin API v2 |
| OpenBao | 2.0+ | KV v2 + Kubernetes Auth |
| Helm | 3.12+ | Установка чартов |
| kubectl | 1.25+ | Управление кластером |
| Docker | 20.10+ | Сборка образов (для dev) |
| Go | 1.26+ | Сборка из исходников (опционально) |

### 1.2 Ресурсы кластера

| Компонент | CPU request / limit | RAM request / limit | Размещение |
|---|---|---|---|
| kubebao-kms | 100m / 200m | 128Mi / 256Mi | Каждый control-plane узел |
| kubebao-csi | 50m / 100m | 64Mi / 128Mi | Каждый узел |
| kubebao-operator | 100m / 200m | 128Mi / 256Mi | Любой узел |
| kubebao-ui | 50m / 100m | 64Mi / 128Mi | Любой узел |
| OpenBao | 250m / 500m | 256Mi / 512Mi | Отдельный namespace |

### 1.3 Установка инструментов (macOS)

```bash
# Homebrew
brew install kubectl helm jq

# OpenBao CLI (опционально — можно работать через curl)
brew install openbao
```

### 1.4 Установка инструментов (Linux)

```bash
# kubectl
curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
chmod +x kubectl && sudo mv kubectl /usr/local/bin/

# Helm
curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash

# jq
sudo apt-get install -y jq
```

---

## 2. Подключение к кластеру Kubernetes

### 2.1 Проверка доступа

```bash
kubectl version
kubectl get nodes
```

Ожидаемый результат: все узлы в статусе `Ready`.

### 2.2 Для локальной разработки (Rancher Desktop / Docker Desktop / minikube)

**Rancher Desktop:**
1. Установите [Rancher Desktop](https://rancherdesktop.io/)
2. Settings → Kubernetes: **ON**, Container runtime: **dockerd (moby)**
3. Дождитесь: Kubernetes: Running

**minikube:**
```bash
minikube start --cpus=4 --memory=8192 --driver=docker
```

### 2.3 Создание namespaces

```bash
kubectl create namespace openbao
kubectl create namespace kubebao-system
```

---

## 3. Развёртывание OpenBao

### Вариант А: Dev-режим (для тестирования)

> Dev-режим не требует unseal, данные хранятся в памяти. **Не для production.**

#### 3.1 ServiceAccount и RBAC

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

#### 3.2 Deployment OpenBao (dev)

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

#### 3.3 Service

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

#### 3.4 Ожидание готовности

```bash
kubectl wait --for=condition=ready pod -l app=openbao -n openbao --timeout=180s
kubectl get pods -n openbao
```

### Вариант Б: Standalone через Helm (Rancher Desktop / k3s / single-node)

> По умолчанию Helm chart OpenBao использует `storage "consul"`, но Consul чаще всего не установлен. Также `storage "file"` несовместим с `service_registration "kubernetes"`. Поэтому задаём конфиг явно.

```bash
helm repo add openbao https://openbao.github.io/openbao-helm
helm repo update

helm install openbao openbao/openbao \
  -n openbao --create-namespace \
  --set server.standalone.enabled=true \
  --set server.dataStorage.enabled=true \
  --set server.dataStorage.size=1Gi \
  --set 'server.standalone.config=ui = true
listener "tcp" {
  tls_disable = 1
  address = "[::]:8200"
  cluster_address = "[::]:8201"
}
storage "file" {
  path = "/openbao/data"
}' \
  --set injector.enabled=true
```

Дождитесь запуска пода (он будет `0/1 Running` — это нормально, ещё не инициализирован):

```bash
kubectl get pods -n openbao -w
# Ожидайте: openbao-0   0/1   Running   0   ...
```

Инициализация (1 ключ для dev/тестирования, 5 ключей для production):

```bash
# Для тестирования (1 ключ):
kubectl exec -n openbao openbao-0 -- bao operator init \
  -key-shares=1 -key-threshold=1 -format=json > openbao-init.json

cat openbao-init.json | jq -r '.unseal_keys_b64[0]'   # Unseal Key
cat openbao-init.json | jq -r '.root_token'             # Root Token
```

Разблокировка (unseal):

```bash
UNSEAL_KEY=$(cat openbao-init.json | jq -r '.unseal_keys_b64[0]')
kubectl exec -n openbao openbao-0 -- bao operator unseal "$UNSEAL_KEY"
```

Проверка — под должен стать `1/1 Running`:

```bash
kubectl get pods -n openbao
# NAME        READY   STATUS    RESTARTS   AGE
# openbao-0   1/1     Running   0          2m
```

> **Сохраните `openbao-init.json` в безопасном месте! Без unseal-ключей вы потеряете доступ к данным.**

### Вариант В: Production HA (Raft, 3 реплики)

```bash
helm install openbao openbao/openbao \
  --namespace openbao --create-namespace \
  --set server.ha.enabled=true \
  --set server.ha.replicas=3 \
  --set server.ha.raft.enabled=true \
  --set server.dataStorage.enabled=true \
  --set server.dataStorage.size=10Gi

# Инициализация (на первом поде)
kubectl exec -n openbao openbao-0 -- bao operator init \
  -key-shares=5 -key-threshold=3 -format=json > openbao-init.json

# Разблокировка каждого пода (3 ключа из 5)
for pod in openbao-0 openbao-1 openbao-2; do
  for i in 0 1 2; do
    KEY=$(cat openbao-init.json | jq -r ".unseal_keys_b64[$i]")
    kubectl exec -n openbao $pod -- bao operator unseal "$KEY"
  done
done
```

> **Сохраните unseal-ключи и root-токен в безопасном месте!**

### Типичные ошибки при развёртывании OpenBao

| Ошибка в логах | Причина | Решение |
|---|---|---|
| `unknown storage type consul` | Helm chart по умолчанию использует Consul | Задайте `server.standalone.config` с `storage "file"` |
| `storage does not support HA` | `service_registration "kubernetes"` несовместим с `file` storage | Уберите `service_registration` из конфига |
| `0/1 Running` после установки | OpenBao не инициализирован/не распечатан | Выполните `bao operator init` + `bao operator unseal` |

---

## 4. Настройка OpenBao

### 4.1 Port-forward

Откройте **отдельный терминал** и оставьте команду работать:

```bash
kubectl port-forward svc/openbao 8200:8200 -n openbao
```

### 4.2 Переменные окружения

```bash
export BAO_ADDR="http://127.0.0.1:8200"
export BAO_TOKEN="root"  # для dev-режима; для production — ваш root-токен
```

### 4.3 Проверка доступа к OpenBao

```bash
# Через CLI (если установлен)
bao status

# Или через curl
curl -s http://127.0.0.1:8200/v1/sys/health | jq .
```

Ожидаемый ответ: `"initialized": true, "sealed": false`.

### 4.4 Включение KV v2 (хранилище секретов)

```bash
# Для dev-режима KV v2 уже включён по адресу secret/
# Для production:
curl -s -X POST "http://127.0.0.1:8200/v1/sys/mounts/secret" \
  -H "X-Vault-Token: $BAO_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"type":"kv","options":{"version":"2"}}'
```

### 4.5 Включение Kubernetes Auth

```bash
# Получение данных кластера
KUBE_HOST="https://kubernetes.default.svc"
KUBE_CA_CERT=$(kubectl config view --raw --minify --flatten \
  -o jsonpath='{.clusters[].cluster.certificate-authority-data}')
TOKEN_JWT=$(kubectl create token openbao -n openbao --duration=87600h)

# Включение auth method
curl -s -X POST "http://127.0.0.1:8200/v1/sys/auth/kubernetes" \
  -H "X-Vault-Token: $BAO_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"type":"kubernetes"}'

# Конфигурация
CA_DECODED=$(echo "$KUBE_CA_CERT" | base64 -d)
AUTH_CONFIG=$(jq -n \
  --arg host "$KUBE_HOST" \
  --arg ca "$CA_DECODED" \
  --arg jwt "$TOKEN_JWT" \
  '{kubernetes_host: $host, kubernetes_ca_cert: $ca, token_reviewer_jwt: $jwt, disable_iss_validation: true}')

curl -s -X POST "http://127.0.0.1:8200/v1/auth/kubernetes/config" \
  -H "X-Vault-Token: $BAO_TOKEN" \
  -H "Content-Type: application/json" \
  -d "$AUTH_CONFIG"
```

> **Примечание:** если `$KUBE_CA_CERT` пустой (Rancher/k3d), уберите `kubernetes_ca_cert` из конфига и добавьте `"disable_local_ca_jwt": true`.

### 4.6 Создание политики для KubeBao

```bash
POLICY='# KMS: чтение/запись ключей шифрования Кузнечик
path "secret/data/kubebao/*" {
  capabilities = ["create", "read", "update", "delete", "list"]
}
path "secret/metadata/kubebao/*" {
  capabilities = ["read", "list", "delete"]
}

# Operator + CSI: чтение секретов приложений
path "secret/data/*" {
  capabilities = ["create", "read", "update", "delete", "list"]
}
path "secret/metadata/*" {
  capabilities = ["read", "list", "delete"]
}

# Transit (legacy, если используется)
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
}'

BODY=$(jq -n --arg policy "$POLICY" '{policy: $policy}')
curl -s -X PUT "http://127.0.0.1:8200/v1/sys/policies/acl/kubebao-policy" \
  -H "X-Vault-Token: $BAO_TOKEN" \
  -H "Content-Type: application/json" \
  -d "$BODY"
```

### 4.7 Создание ролей Kubernetes Auth

```bash
# Роль для компонентов KubeBao (KMS, Operator, CSI)
curl -s -X POST "http://127.0.0.1:8200/v1/auth/kubernetes/role/kubebao" \
  -H "X-Vault-Token: $BAO_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "bound_service_account_names": "kubebao",
    "bound_service_account_namespaces": "kubebao-system",
    "policies": "kubebao-policy",
    "ttl": "1h"
  }'

# Роль для тестовых приложений
curl -s -X POST "http://127.0.0.1:8200/v1/auth/kubernetes/role/my-app" \
  -H "X-Vault-Token: $BAO_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "bound_service_account_names": "demo-app,default",
    "bound_service_account_namespaces": "default,kubebao-system",
    "policies": "kubebao-policy",
    "ttl": "1h"
  }'
```

### 4.8 Создание тестовых секретов в OpenBao

```bash
# База данных
curl -s -X POST "http://127.0.0.1:8200/v1/secret/data/myapp/database" \
  -H "X-Vault-Token: $BAO_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"data":{"username":"dbuser","password":"SuperSecret123!","host":"db.example.com","port":"5432"}}'

# Конфигурация приложения
curl -s -X POST "http://127.0.0.1:8200/v1/secret/data/myapp/config" \
  -H "X-Vault-Token: $BAO_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"data":{"api_key":"sk-test-key-abc123","environment":"production","debug":"false"}}'

# API ключ
curl -s -X POST "http://127.0.0.1:8200/v1/secret/data/myapp/api" \
  -H "X-Vault-Token: $BAO_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"data":{"key":"secret_api_key_xyz_789"}}'
```

### 4.9 Проверка секретов

```bash
curl -s "http://127.0.0.1:8200/v1/secret/data/myapp/database" \
  -H "X-Vault-Token: $BAO_TOKEN" | jq '.data.data'
```

Ожидаемый результат:

```json
{
  "username": "dbuser",
  "password": "SuperSecret123!",
  "host": "db.example.com",
  "port": "5432"
}
```

Также можно проверить через Web UI: http://127.0.0.1:8200 (Token: `root`).

---

## 5. Сборка и установка KubeBao

### 5.1 Клонирование репозитория

```bash
git clone https://github.com/AssasinNik/kubebao.git
cd kubebao
```

### 5.2 Сборка Docker-образов

```bash
docker build -t kubebao/kubebao-kms:dev --build-arg COMPONENT=kubebao-kms .
docker build -t kubebao/kubebao-csi:dev --build-arg COMPONENT=kubebao-csi .
docker build -t kubebao/kubebao-operator:dev --build-arg COMPONENT=kubebao-operator .
docker build -t kubebao/kubebao-ui:dev --build-arg COMPONENT=kubebao-ui .
```

Проверка:
```bash
docker images | grep kubebao
```

> Должны отобразиться 4 образа (~25-40 МБ каждый).

### 5.3 Установка Secrets Store CSI Driver

```bash
helm repo add secrets-store-csi-driver \
  https://kubernetes-sigs.github.io/secrets-store-csi-driver/charts
helm repo update

helm install csi-secrets-store secrets-store-csi-driver/secrets-store-csi-driver \
  -n kubebao-system \
  --set syncSecret.enabled=true \
  --set enableSecretRotation=true \
  --set rotationPollInterval=30s \
  --wait --timeout=120s
```

### 5.4 Установка KubeBao через Helm (из исходников)

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
  --set ui.image.repository=kubebao/kubebao-ui \
  --set csi.driver.install=false \
  --wait --timeout=300s
```

### 5.5 Установка из Helm-репозитория (когда опубликован)

```bash
helm repo add kubebao https://assasinnik.github.io/kubebao
helm repo update

helm install kubebao kubebao/kubebao \
  --namespace kubebao-system --create-namespace \
  --set global.openbao.address="http://openbao.openbao.svc.cluster.local:8200" \
  --set global.openbao.role=kubebao
```

### 5.6 Проверка установки

```bash
kubectl get pods -n kubebao-system
kubectl get daemonsets -n kubebao-system
kubectl get deployments -n kubebao-system
```

Ожидаемый результат: все поды в статусе `Running` / `1/1`.

```bash
# Логи KMS — убедиться, что Кузнечик активирован
kubectl logs -n kubebao-system -l app=kubebao-kms --tail=10
```

Ожидаемая строка в логах:
```
Использование провайдера Kuznyechik (ГОСТ Р 34.12-2015 + ГОСТ Р 34.13-2015)
```

---

## 6. Настройка и доступ к KubeBao UI

KubeBao UI — веб-панель управления в стиле HashiCorp Vault. Позволяет:
- просматривать статус системы (OpenBao, KMS, Kubernetes);
- управлять ключами шифрования (просмотр, ротация);
- просматривать Kubernetes Secrets (зашифрованные данные);
- дешифровать секреты своим мастер-ключом;
- видеть поды с подключённым CSI;
- отслеживать метрики (операции шифрования, память, горутины).

### 6.1 UI включён по умолчанию

UI включается автоматически при установке KubeBao через Helm (`ui.enabled: true` в `values.yaml`). Проверьте, что pod работает:

```bash
kubectl get pods -n kubebao-system -l app.kubernetes.io/component=ui
# NAME                          READY   STATUS    RESTARTS   AGE
# kubebao-ui-7d8f9c6b4-x2k4l   1/1     Running   0          5m
```

### 6.2 Доступ через port-forward (быстрый способ)

```bash
kubectl port-forward svc/kubebao-ui 8443:8443 -n kubebao-system
```

Откройте в браузере: **http://localhost:8443**

### 6.3 Доступ через Ingress

По умолчанию Helm chart создаёт Ingress-ресурс. Для настройки отредактируйте `values.yaml`:

```yaml
ui:
  enabled: true
  ingress:
    enabled: true
    className: nginx
    hosts:
      - host: kubebao.local
        paths:
          - path: /
            pathType: Prefix
    # TLS (опционально)
    tls:
      - secretName: kubebao-ui-tls
        hosts:
          - kubebao.local
```

Или задайте через `--set` при установке:

```bash
helm upgrade --install kubebao ./charts/kubebao \
  --namespace kubebao-system \
  --set ui.ingress.enabled=true \
  --set ui.ingress.className=nginx \
  --set "ui.ingress.hosts[0].host=kubebao.example.com" \
  --set "ui.ingress.hosts[0].paths[0].path=/" \
  --set "ui.ingress.hosts[0].paths[0].pathType=Prefix"
```

Для локальной разработки добавьте в `/etc/hosts`:

```bash
echo "127.0.0.1 kubebao.local" | sudo tee -a /etc/hosts
```

Проверьте Ingress:

```bash
kubectl get ingress -n kubebao-system
# NAME          CLASS   HOSTS           ADDRESS        PORTS   AGE
# kubebao-ui    nginx   kubebao.local   192.168.5.15   80      5m
```

### 6.4 Настройка OpenBao-токена для UI (ротация ключей)

Для функции ротации ключей через UI нужен OpenBao-токен. Задайте его через переменную окружения:

```bash
helm upgrade kubebao ./charts/kubebao \
  --namespace kubebao-system \
  --reuse-values \
  --set "extraEnv[0].name=OPENBAO_TOKEN" \
  --set "extraEnv[0].value=<ваш-root-или-сервисный-токен>"
```

> **Для production:** создайте сервисный токен с минимальными правами вместо root-токена.

### 6.5 Обзор страниц UI

**Dashboard** — общая информация: статус OpenBao, uptime, KMS-провайдер, метрики.

**Keys** — текущий ключ шифрования (имя, путь в OpenBao KV, версия, алгоритм). Кнопка **Rotate Key** генерирует новый 256-битный ключ и записывает в OpenBao. KMS подхватит новый ключ в течение 30 секунд.

**Secrets** — список Kubernetes Secrets в кластере. Показывает имя, namespace, тип, ключи данных и preview зашифрованного значения. Поддерживает фильтрацию по имени.

**CSI Pods** — поды с подключённым Secrets Store CSI Driver. Показывает SecretProviderClass и mount path.

**Metrics** — операции шифрования/дешифрования, средняя задержка, ротации ключей, горутины, heap allocation.

**Decrypt** — вставьте 256-битный мастер-ключ (base64) и зашифрованный текст (hex или base64). UI расшифрует значение через Kuznyechik AEAD (ГОСТ Р 34.12/13-2015). Это позволяет проверить, что секрет действительно зашифрован и расшифровывается только правильным ключом.

### 6.6 Отключение UI

Если UI не нужен:

```bash
helm upgrade kubebao ./charts/kubebao \
  --namespace kubebao-system \
  --reuse-values \
  --set ui.enabled=false
```

---

## 7. Настройка KMS-шифрования etcd

> Этот шаг нужен если вы хотите, чтобы **все Kubernetes Secrets хранились в etcd в зашифрованном виде** алгоритмом Кузнечик.

### 6.1 Создание EncryptionConfiguration

На **каждом control-plane узле** создайте файл:

```bash
sudo tee /etc/kubernetes/encryption-config.yaml << 'EOF'
apiVersion: apiserver.config.k8s.io/v1
kind: EncryptionConfiguration
resources:
  - resources:
      - secrets
    providers:
      - kms:
          apiVersion: v2
          name: kubebao-kms
          endpoint: unix:///var/run/kubebao/kms.sock
          timeout: 10s
      - identity: {}
EOF
```

### 6.2 Обновление kube-apiserver

Добавьте в манифест `/etc/kubernetes/manifests/kube-apiserver.yaml`:

```yaml
spec:
  containers:
  - command:
    - kube-apiserver
    # ... существующие флаги ...
    - --encryption-provider-config=/etc/kubernetes/encryption-config.yaml
    volumeMounts:
    # ... существующие маунты ...
    - name: encryption-config
      mountPath: /etc/kubernetes/encryption-config.yaml
      readOnly: true
    - name: kms-socket
      mountPath: /var/run/kubebao
  volumes:
  # ... существующие volumes ...
  - name: encryption-config
    hostPath:
      path: /etc/kubernetes/encryption-config.yaml
      type: File
  - name: kms-socket
    hostPath:
      path: /var/run/kubebao
      type: DirectoryOrCreate
```

### 6.3 Перезапуск kube-apiserver

kube-apiserver перезапустится автоматически после изменения static pod manifest. Проверьте:

```bash
kubectl get pods -n kube-system -l component=kube-apiserver
```

### 6.4 Перешифровка существующих секретов

Все **новые** секреты будут шифроваться через KMS. Для перешифровки **существующих**:

```bash
kubectl get secrets --all-namespaces -o json | kubectl replace -f -
```

---

## 8. Создание тестовых секретов и проверка

### 7.1 Создание Kubernetes Secret обычным способом

```bash
kubectl create secret generic test-encryption-secret \
  --from-literal=username=admin \
  --from-literal=password=MySecretPassword123
```

### 7.2 Чтение секрета через kubectl (расшифровано)

```bash
kubectl get secret test-encryption-secret -o jsonpath='{.data.password}' | base64 -d
echo
```

Ожидаемый результат: `MySecretPassword123`

### 7.3 Проверка логов KMS

```bash
kubectl logs -n kubebao-system -l app=kubebao-kms --tail=20
```

Если KMS настроен (раздел 7), в логах будут строки:
```
Запрос шифрования uid=... plaintextSize=...
Шифрование выполнено успешно uid=... ciphertextSize=...
```

---

## 9. Тестирование BaoSecret (Operator)

### 8.1 Применение примера BaoSecret

```bash
kubectl apply -f config/samples/baosecret_sample.yaml
```

### 8.2 Проверка статуса

```bash
# Подождите 10-20 секунд
kubectl get baosecrets -o wide
```

Ожидаемый результат:

```
NAME             SECRET PATH              TARGET           LAST SYNC              AGE
my-app-secrets   secret/data/myapp/config my-app-secret    2026-03-21T...         30s
```

### 8.3 Проверка созданного Kubernetes Secret

```bash
kubectl get secret my-app-secret -o yaml
```

Ожидаемый результат: Secret с данными из OpenBao (api_key, environment, debug).

```bash
# Прочитать конкретное значение
kubectl get secret my-app-secret -o jsonpath='{.data.api_key}' | base64 -d
echo
```

Ожидаемый вывод: `sk-test-key-abc123`

### 8.4 Проверка автоматического обновления

Измените секрет в OpenBao:

```bash
curl -s -X POST "http://127.0.0.1:8200/v1/secret/data/myapp/config" \
  -H "X-Vault-Token: $BAO_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"data":{"api_key":"NEW-KEY-UPDATED","environment":"staging","debug":"true"}}'
```

Подождите `refreshInterval` (по умолчанию 1h, в примере — 30s–1h) и проверьте:

```bash
kubectl get secret my-app-secret -o jsonpath='{.data.api_key}' | base64 -d
echo
```

---

## 10. Тестирование CSI Provider

### 9.1 Применение SecretProviderClass

```bash
kubectl apply -f config/samples/secretproviderclass_sample.yaml
```

### 9.2 Создание тестового пода

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

### 9.3 Проверка секретов в поде

```bash
kubectl wait --for=condition=ready pod test-csi-secrets --timeout=60s

# Содержимое смонтированных секретов
kubectl exec test-csi-secrets -- ls -la /mnt/secrets
kubectl exec test-csi-secrets -- cat /mnt/secrets/db-password
kubectl exec test-csi-secrets -- cat /mnt/secrets/api-key
```

### 9.4 Полный демо (BaoSecret + CSI + синхронизация)

```bash
kubectl apply -f examples/dynamic-secrets-demo.yaml

# Ожидание готовности
kubectl wait --for=condition=ready pod -l app=demo-app --timeout=120s

# Логи — под выводит секреты каждые 30 секунд
kubectl logs -l app=demo-app -f

# Синхронизированный Secret
kubectl get secret demo-synced-secret -o jsonpath='{.data.username}' | base64 -d
echo
kubectl get secret demo-synced-secret -o jsonpath='{.data.password}' | base64 -d
echo
```

---

## 11. Проверка шифрования etcd

> Этот раздел актуален только если вы выполнили раздел 7 (настройка KMS).

### 10.1 Проверка через API

```bash
# Статус KMS
kubectl get --raw /healthz 2>&1
```

### 10.2 Проверка через etcdctl (если есть доступ)

```bash
# На control-plane узле
ETCDCTL_API=3 etcdctl \
  --endpoints=https://127.0.0.1:2379 \
  --cacert=/etc/kubernetes/pki/etcd/ca.crt \
  --cert=/etc/kubernetes/pki/etcd/server.crt \
  --key=/etc/kubernetes/pki/etcd/server.key \
  get /registry/secrets/default/test-encryption-secret | hexdump -C | head -20
```

Если KMS работает, данные в etcd будут **зашифрованы** (бинарные данные вместо читаемого текста). В начале записи будет маркер `k8s:enc:kms:v2:kubebao-kms`.

### 10.3 Проверка через логи KMS

```bash
kubectl logs -n kubebao-system -l app=kubebao-kms --tail=50
```

При каждом создании/чтении секрета в логах видны:
- `Запрос шифрования` — при записи
- `Запрос дешифрования` — при чтении
- `Kuznyechik шифрование завершено duration=...` — время операции

---

## 12. Ротация ключей

### 11.1 Ротация ключа шифрования Кузнечик

```bash
# 1. Сгенерировать новый 256-битный ключ
NEW_KEY=$(openssl rand -base64 32)

# 2. Записать в OpenBao KV с новой версией
curl -s -X POST "http://127.0.0.1:8200/v1/secret/data/kubebao/kms-keys/kubebao-kms" \
  -H "X-Vault-Token: $BAO_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"data\":{\"key\":\"$NEW_KEY\",\"version\":2}}"

# 3. KMS-плагин обнаружит новый ключ при health check (≤30 сек)
# Проверка:
kubectl logs -n kubebao-system -l app=kubebao-kms --tail=5
# Ожидаемая строка: "Версия ключа изменилась"

# 4. Перешифровать все существующие секреты новым ключом:
kubectl get secrets --all-namespaces -o json | kubectl replace -f -
```

### 11.2 Обновление версии KubeBao

```bash
helm upgrade kubebao kubebao/kubebao \
  --namespace kubebao-system \
  --reuse-values \
  --set global.image.tag=<new-version>
```

---

## 13. Production Checklist

### Безопасность

- [ ] OpenBao развёрнут в **HA-режиме** (3+ реплики)
- [ ] **TLS** включён между KubeBao и OpenBao
- [ ] Kubernetes Auth настроен (**не используется root-токен**)
- [ ] Политики OpenBao следуют **принципу least privilege**
- [ ] Network Policies ограничивают доступ к OpenBao
- [ ] Секреты OpenBao **бэкапятся** (Raft snapshots)

### Шифрование

- [ ] `EncryptionConfiguration` применён на **всех** control-plane узлах
- [ ] KMS DaemonSet работает на **всех** control-plane узлах
- [ ] Провайдер шифрования: **kuznyechik** (ГОСТ Р 34.12-2015)
- [ ] Ротация ключей задокументирована и протестирована

### UI

- [ ] UI доступен через **Ingress с TLS** (не port-forward)
- [ ] **Не используется root-токен** OpenBao в переменных окружения UI
- [ ] Network Policy ограничивает доступ к UI (только авторизованные пользователи)
- [ ] Функция Decrypt доступна только администраторам

### Инфраструктура

- [ ] Resource limits и requests заданы для всех компонентов
- [ ] Pod Security Standards (**restricted**) включены
- [ ] Seccomp профили установлены (`RuntimeDefault`)
- [ ] Мониторинг и alerting настроены (Prometheus)
- [ ] Логи собираются (Loki / ELK / CloudWatch)

---

## 14. Устранение неполадок

### KMS не запускается

```bash
# Логи
kubectl logs -n kubebao-system -l app=kubebao-kms

# Описание пода
kubectl describe pod -n kubebao-system -l app=kubebao-kms

# Проверить доступность OpenBao из пода
kubectl exec -n kubebao-system $(kubectl get pod -n kubebao-system -l app=kubebao-kms -o jsonpath='{.items[0].metadata.name}') \
  -- wget -q -O- http://openbao.openbao.svc.cluster.local:8200/v1/sys/health
```

### Operator не синхронизирует секреты

```bash
kubectl logs -n kubebao-system -l app.kubernetes.io/name=kubebao-operator
kubectl describe baosecret <name>
kubectl get events --field-selector involvedObject.kind=BaoSecret
```

Частые причины:
- OpenBao недоступен — проверьте Service и port-forward
- Нет прав — проверьте политику в OpenBao
- Секрет не найден — проверьте `secretPath` (без `secret/data/` префикса)

### CSI секреты не монтируются

```bash
kubectl logs -n kubebao-system -l app=kubebao-csi
kubectl describe pod <pod-with-csi-volume>
kubectl get secretproviderclass <name> -o yaml
```

Частые причины:
- CSI Driver не установлен — `kubectl get daemonset -n kubebao-system`
- Неверный `roleName` — проверьте роль в OpenBao
- ServiceAccount не привязан к роли

### UI не открывается

```bash
# Проверьте, что pod UI работает
kubectl get pods -n kubebao-system -l app.kubernetes.io/component=ui
kubectl logs -n kubebao-system -l app.kubernetes.io/component=ui

# Проверьте Service
kubectl get svc kubebao-ui -n kubebao-system

# Проверьте Ingress
kubectl get ingress kubebao-ui -n kubebao-system
kubectl describe ingress kubebao-ui -n kubebao-system
```

Частые причины:
- Ingress Controller не установлен — `kubectl get pods -n ingress-nginx`
- Неверный `ingressClassName` — проверьте `kubectl get ingressclass`
- Для port-forward: порт уже занят — используйте другой: `kubectl port-forward svc/kubebao-ui 9090:8443 -n kubebao-system`

### Общая диагностика

```bash
# Статус всех компонентов
kubectl get pods -n openbao
kubectl get pods -n kubebao-system
kubectl get baosecrets -A
kubectl get baopolicies -A
kubectl get secretproviderclasses -A

# Логи всех компонентов
kubectl logs -n kubebao-system -l app=kubebao-kms --tail=20
kubectl logs -n kubebao-system -l app=kubebao-csi --tail=20
kubectl logs -n kubebao-system -l app.kubernetes.io/name=kubebao-operator --tail=20
kubectl logs -n kubebao-system -l app.kubernetes.io/component=ui --tail=20
```

---

## 15. Очистка окружения

```bash
# Удаление тестовых ресурсов
kubectl delete pod test-csi-secrets --ignore-not-found
kubectl delete secret test-encryption-secret --ignore-not-found
kubectl delete -f examples/dynamic-secrets-demo.yaml --ignore-not-found
kubectl delete -f config/samples/baosecret_sample.yaml --ignore-not-found
kubectl delete -f config/samples/secretproviderclass_sample.yaml --ignore-not-found

# Удаление KubeBao
helm uninstall kubebao -n kubebao-system

# Удаление CSI Driver
helm uninstall csi-secrets-store -n kubebao-system

# Удаление OpenBao
kubectl delete namespace openbao

# Удаление namespace
kubectl delete namespace kubebao-system

# Остановить port-forward (Ctrl+C в окне, где он запущен)
```

---

## Справочник переменных окружения KMS

| Переменная | По умолчанию | Описание |
|---|---|---|
| `KUBEBAO_KMS_SOCKET` | `/var/run/kubebao/kms.sock` | Путь к Unix socket |
| `KUBEBAO_KMS_PROVIDER` | `kuznyechik` | Провайдер: `kuznyechik` или `transit` |
| `KUBEBAO_KMS_KEY_NAME` | `kubebao-kms` | Имя ключа |
| `KUBEBAO_KMS_KEY_TYPE` | `kuznyechik` | Тип ключа |
| `KUBEBAO_KMS_KV_PREFIX` | `kubebao/kms-keys` | Префикс пути в KV |
| `KUBEBAO_KMS_CREATE_KEY` | `true` | Создавать ключ при первом использовании |
| `KUBEBAO_KMS_HEALTH_INTERVAL` | `30s` | Интервал health check |
| `OPENBAO_ADDR` | — | Адрес OpenBao |
| `OPENBAO_TOKEN` | — | Токен (не рекомендуется, используйте K8s Auth) |
| `OPENBAO_K8S_ROLE` | — | Роль Kubernetes Auth |
