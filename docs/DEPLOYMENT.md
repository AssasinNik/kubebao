# Руководство по развёртыванию KubeBao

## Содержание

1. [Требования](#1-требования)
2. [Подготовка OpenBao](#2-подготовка-openbao)
3. [Установка KubeBao](#3-установка-kubebao)
4. [Конфигурация KMS (Кузнечик)](#4-конфигурация-kms-кузнечик)
5. [Конфигурация Operator](#5-конфигурация-operator)
6. [Конфигурация CSI Provider](#6-конфигурация-csi-provider)
7. [Проверка работоспособности](#7-проверка-работоспособности)
8. [Production Checklist](#8-production-checklist)
9. [Обновление и ротация ключей](#9-обновление-и-ротация-ключей)
10. [Устранение неполадок](#10-устранение-неполадок)

---

## 1. Требования

| Компонент | Минимальная версия |
|---|---|
| Kubernetes | 1.25+ (для KMS Plugin API v2) |
| OpenBao | 2.0+ |
| Helm | 3.12+ |
| kubectl | 1.25+ |

**Ресурсы кластера:**

| Компонент | CPU (request/limit) | RAM (request/limit) |
|---|---|---|
| KMS Plugin | 100m / 200m | 128Mi / 256Mi |
| CSI Provider | 50m / 100m | 64Mi / 128Mi |
| Operator | 100m / 200m | 128Mi / 256Mi |

---

## 2. Подготовка OpenBao

### 2.1 Развёртывание OpenBao

Для production рекомендуется использовать Helm-чарт OpenBao в режиме HA:

```bash
helm repo add openbao https://openbao.github.io/openbao-helm
helm repo update

helm install openbao openbao/openbao \
  --namespace openbao --create-namespace \
  --set server.ha.enabled=true \
  --set server.ha.replicas=3
```

### 2.2 Инициализация и разблокировка

```bash
kubectl exec -n openbao openbao-0 -- bao operator init -key-shares=5 -key-threshold=3
kubectl exec -n openbao openbao-0 -- bao operator unseal <unseal-key-1>
kubectl exec -n openbao openbao-0 -- bao operator unseal <unseal-key-2>
kubectl exec -n openbao openbao-0 -- bao operator unseal <unseal-key-3>
```

### 2.3 Настройка KV v2 (для хранения ключей Кузнечика)

```bash
export BAO_ADDR="http://127.0.0.1:8200"
export BAO_TOKEN="<root-token>"

# Port-forward (в отдельном терминале)
kubectl port-forward svc/openbao 8200:8200 -n openbao

# Включить KV v2
bao secrets enable -path=secret kv-v2
```

### 2.4 Настройка Kubernetes Auth

```bash
bao auth enable kubernetes

bao write auth/kubernetes/config \
  kubernetes_host="https://kubernetes.default.svc" \
  kubernetes_ca_cert=@/var/run/secrets/kubernetes.io/serviceaccount/ca.crt
```

### 2.5 Политика и роль для KubeBao

```bash
bao policy write kubebao-policy - <<'EOF'
# KV — хранение ключей шифрования Кузнечик
path "secret/data/kubebao/*" {
  capabilities = ["create", "read", "update", "delete", "list"]
}
path "secret/metadata/kubebao/*" {
  capabilities = ["read", "list", "delete"]
}

# KV — чтение секретов приложений (operator + CSI)
path "secret/data/*" {
  capabilities = ["read", "list"]
}
path "secret/metadata/*" {
  capabilities = ["read", "list"]
}
EOF

bao write auth/kubernetes/role/kubebao \
  bound_service_account_names=kubebao \
  bound_service_account_namespaces=kubebao-system \
  policies=kubebao-policy \
  ttl=1h
```

---

## 3. Установка KubeBao

### 3.1 Установка Secrets Store CSI Driver (для CSI-компонента)

```bash
helm repo add secrets-store-csi-driver \
  https://kubernetes-sigs.github.io/secrets-store-csi-driver/charts
helm repo update

helm install csi-secrets-store secrets-store-csi-driver/secrets-store-csi-driver \
  -n kubebao-system --create-namespace \
  --set syncSecret.enabled=true \
  --set enableSecretRotation=true \
  --set rotationPollInterval=30s
```

### 3.2 Установка KubeBao через Helm

```bash
helm repo add kubebao https://<your-org>.github.io/kubebao
helm repo update

helm install kubebao kubebao/kubebao \
  --namespace kubebao-system --create-namespace \
  --set global.openbao.address="http://openbao.openbao.svc.cluster.local:8200" \
  --set global.openbao.role=kubebao \
  --set kms.encryptionProvider=kuznyechik
```

### 3.3 Установка из исходников (разработка)

```bash
git clone https://github.com/kubebao/kubebao.git
cd kubebao

# Сборка образов
make docker-build

# Установка
helm upgrade --install kubebao ./charts/kubebao \
  --namespace kubebao-system --create-namespace \
  --set global.openbao.address="http://openbao.openbao.svc.cluster.local:8200" \
  --set global.openbao.role=kubebao \
  --set global.image.tag=dev \
  --set global.image.pullPolicy=Never \
  --set global.image.registry="" \
  --set kms.image.repository=kubebao/kubebao-kms \
  --set csi.image.repository=kubebao/kubebao-csi \
  --set operator.image.repository=kubebao/kubebao-operator \
  --set csi.driver.install=false
```

### 3.4 Проверка установки

```bash
kubectl get pods -n kubebao-system
kubectl get daemonsets -n kubebao-system
kubectl get deployments -n kubebao-system
```

Все поды должны быть в статусе `Running`.

---

## 4. Конфигурация KMS (Кузнечик)

### 4.1 EncryptionConfiguration для kube-apiserver

Создайте файл `/etc/kubernetes/encryption-config.yaml` на каждом control-plane узле:

```yaml
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
```

### 4.2 Обновление kube-apiserver

Добавьте флаг в манифест kube-apiserver:

```yaml
spec:
  containers:
  - command:
    - kube-apiserver
    - --encryption-provider-config=/etc/kubernetes/encryption-config.yaml
    volumeMounts:
    - name: encryption-config
      mountPath: /etc/kubernetes/encryption-config.yaml
      readOnly: true
    - name: kms-socket
      mountPath: /var/run/kubebao
  volumes:
  - name: encryption-config
    hostPath:
      path: /etc/kubernetes/encryption-config.yaml
      type: File
  - name: kms-socket
    hostPath:
      path: /var/run/kubebao
      type: DirectoryOrCreate
```

### 4.3 Переменные окружения KMS

| Переменная | По умолчанию | Описание |
|---|---|---|
| `KUBEBAO_KMS_SOCKET` | `/var/run/kubebao/kms.sock` | Путь к Unix socket |
| `KUBEBAO_KMS_PROVIDER` | `kuznyechik` | Провайдер шифрования |
| `KUBEBAO_KMS_KEY_NAME` | `kubebao-kms` | Имя ключа в OpenBao KV |
| `KUBEBAO_KMS_KV_PREFIX` | `kubebao/kms-keys` | Префикс пути в KV |
| `KUBEBAO_KMS_CREATE_KEY` | `true` | Создавать ключ автоматически |
| `KUBEBAO_KMS_HEALTH_INTERVAL` | `30s` | Интервал health check |

---

## 5. Конфигурация Operator

### 5.1 BaoSecret — синхронизация секретов

```yaml
apiVersion: kubebao.io/v1alpha1
kind: BaoSecret
metadata:
  name: app-database-creds
spec:
  secretPath: myapp/database
  target:
    name: database-secret
    namespace: default
    type: Opaque
  refreshInterval: 5m
```

### 5.2 BaoPolicy — управление политиками

```yaml
apiVersion: kubebao.io/v1alpha1
kind: BaoPolicy
metadata:
  name: app-readonly
spec:
  policyName: app-readonly
  rules:
    - path: "secret/data/myapp/*"
      capabilities: [read, list]
```

---

## 6. Конфигурация CSI Provider

### 6.1 SecretProviderClass

```yaml
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: kubebao-secrets
spec:
  provider: kubebao
  parameters:
    roleName: my-app
    openbaoAddr: "http://openbao.openbao.svc.cluster.local:8200"
    objects: |
      - objectName: "db-password"
        secretPath: "secret/data/myapp/database"
        secretKey: "password"
```

### 6.2 Монтирование в Pod

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-app
spec:
  serviceAccountName: my-app
  containers:
  - name: app
    image: my-app:latest
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
        secretProviderClass: kubebao-secrets
```

---

## 7. Проверка работоспособности

```bash
# KMS — лог шифрования
kubectl logs -n kubebao-system -l app=kubebao-kms --tail=20

# Operator — синхронизация
kubectl get baosecrets -A
kubectl get baopolicies -A

# CSI — монтирование
kubectl get secretproviderclasses -A

# Health
kubectl get --raw /healthz -v=6 2>&1 | grep kms
```

---

## 8. Production Checklist

- [ ] OpenBao развёрнут в HA-режиме (3+ реплики)
- [ ] TLS включён между KubeBao и OpenBao
- [ ] Kubernetes auth настроен (не используется root-токен)
- [ ] Политики OpenBao следуют принципу least privilege
- [ ] `EncryptionConfiguration` применён на всех control-plane узлах
- [ ] KMS DaemonSet работает на всех control-plane узлах
- [ ] Мониторинг и alerting настроены (Prometheus metrics)
- [ ] Network Policies ограничивают доступ к OpenBao
- [ ] Секреты OpenBao бэкапятся
- [ ] Ротация ключей задокументирована и протестирована
- [ ] Resource limits и requests заданы для всех компонентов
- [ ] Pod Security Standards (restricted) включены
- [ ] Seccomp профили установлены (RuntimeDefault)

---

## 9. Обновление и ротация ключей

### 9.1 Ротация ключа Кузнечика

```bash
# 1. Сгенерировать новый ключ и записать в OpenBao KV
bao kv put secret/kubebao/kms-keys/kubebao-kms \
  key=$(openssl rand -base64 32) \
  version=2

# 2. KMS-плагин обнаружит новый ключ при health check (30 сек)
# 3. Все новые секреты будут зашифрованы новым ключом
# 4. Перешифровать существующие секреты:
kubectl get secrets --all-namespaces -o json | \
  kubectl replace -f -
```

### 9.2 Обновление KubeBao

```bash
helm upgrade kubebao kubebao/kubebao \
  --namespace kubebao-system \
  --reuse-values \
  --set global.image.tag=<new-version>
```

---

## 10. Устранение неполадок

### KMS не запускается

```bash
kubectl logs -n kubebao-system -l app=kubebao-kms
kubectl describe pod -n kubebao-system -l app=kubebao-kms
# Проверить доступность OpenBao:
kubectl exec -n kubebao-system <kms-pod> -- wget -q -O- http://openbao.openbao.svc.cluster.local:8200/v1/sys/health
```

### Operator не синхронизирует секреты

```bash
kubectl logs -n kubebao-system -l app.kubernetes.io/name=kubebao-operator
kubectl describe baosecret <name>
kubectl get events --field-selector involvedObject.kind=BaoSecret
```

### CSI секреты не монтируются

```bash
kubectl logs -n kubebao-system -l app=kubebao-csi
kubectl describe pod <pod-with-csi-volume>
kubectl get secretproviderclass <name> -o yaml
```
