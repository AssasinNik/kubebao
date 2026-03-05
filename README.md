<p align="center">
  <img src="docs/images/logo.png" alt="KubeBao Logo" width="200">
</p>

<h1 align="center">KubeBao</h1>

<p align="center">
  <strong>Kubernetes Secrets Management System powered by OpenBao</strong>
</p>

<p align="center">
  <a href="https://github.com/kubebao/kubebao/releases"><img src="https://img.shields.io/github/v/release/kubebao/kubebao?style=flat-square" alt="Release"></a>
  <a href="https://github.com/kubebao/kubebao/actions"><img src="https://img.shields.io/github/actions/workflow/status/kubebao/kubebao/ci.yaml?style=flat-square" alt="Build Status"></a>
  <a href="https://goreportcard.com/report/github.com/kubebao/kubebao"><img src="https://goreportcard.com/badge/github.com/kubebao/kubebao?style=flat-square" alt="Go Report Card"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/kubebao/kubebao?style=flat-square" alt="License"></a>
</p>

<p align="center">
  <a href="#features">Features</a> •
  <a href="#quick-start">Quick Start</a> •
  <a href="#installation">Installation</a> •
  <a href="#usage">Usage</a> •
  <a href="#documentation">Documentation</a>
</p>

---

## Overview

**KubeBao** — это комплексное решение для управления секретами в Kubernetes с использованием [OpenBao](https://openbao.org/) (открытый форк HashiCorp Vault). Система объединяет несколько подходов к защите секретов:

- 🔐 **KMS Plugin** — шифрование Kubernetes Secrets в etcd
- 📦 **CSI Provider** — прямая инъекция секретов в поды
- 🔄 **Operator** — автоматическая синхронизация секретов из OpenBao

## Features

| Компонент | Описание |
|-----------|----------|
| **KubeBao Operator** | Kubernetes operator для управления `BaoSecret` и `BaoPolicy` CRDs |
| **KubeBao KMS** | KMS plugin для шифрования секретов в etcd через OpenBao Transit |
| **KubeBao CSI** | CSI provider для инъекции секретов напрямую в поды |

### Ключевые возможности

- ✅ **Автоматическая синхронизация** — секреты из OpenBao автоматически синхронизируются в Kubernetes
- ✅ **Динамическое обновление** — изменения в OpenBao отражаются в K8s без перезапуска подов
- ✅ **CSI инъекция** — секреты монтируются как файлы напрямую в поды
- ✅ **Декларативное управление** — политики OpenBao управляются через Kubernetes CRDs
- ✅ **Шифрование at-rest** — KMS plugin для шифрования секретов в etcd
- ✅ **Helm установка** — простая установка через Helm chart

## Quick Start

### Требования

- Kubernetes 1.26+
- Helm 3.0+
- OpenBao или HashiCorp Vault

### Установка за 3 шага

```bash
# 1. Добавьте Helm репозиторий
helm repo add kubebao https://kubebao.github.io/kubebao
helm repo update

# 2. Установите KubeBao
helm install kubebao kubebao/kubebao \
  --namespace kubebao-system \
  --create-namespace \
  --set global.openbao.address="http://openbao.openbao.svc:8200"

# 3. Создайте первый BaoSecret
cat <<EOF | kubectl apply -f -
apiVersion: kubebao.io/v1alpha1
kind: BaoSecret
metadata:
  name: my-secret
spec:
  secretPath: "secret/myapp/config"
  target:
    name: my-k8s-secret
  refreshInterval: "1m"
EOF
```

## Installation

### Helm (рекомендуется)

```bash
# Добавление репозитория
helm repo add kubebao https://kubebao.github.io/kubebao
helm repo update

# Просмотр доступных версий
helm search repo kubebao

# Установка с кастомными параметрами
helm install kubebao kubebao/kubebao \
  --namespace kubebao-system \
  --create-namespace \
  -f values.yaml
```

### Из исходников

```bash
git clone https://github.com/kubebao/kubebao.git
cd kubebao

# Локальная разработка
./scripts/setup-all.sh

# Тестирование
./scripts/e2e-test.sh
```

## Usage

### BaoSecret — Синхронизация секретов

Автоматическая синхронизация секретов из OpenBao в Kubernetes:

```yaml
apiVersion: kubebao.io/v1alpha1
kind: BaoSecret
metadata:
  name: database-credentials
  namespace: default
spec:
  # Путь к секрету в OpenBao
  secretPath: "myapp/database"
  
  # Целевой Kubernetes Secret
  target:
    name: db-secret
    labels:
      app: myapp
  
  # Интервал обновления
  refreshInterval: "30s"
```

### BaoPolicy — Управление политиками

Декларативное управление политиками OpenBao:

```yaml
apiVersion: kubebao.io/v1alpha1
kind: BaoPolicy
metadata:
  name: myapp-policy
spec:
  policyName: "k8s-myapp-policy"
  rules:
    - path: "secret/data/myapp/*"
      capabilities: ["read", "list"]
    - path: "secret/metadata/myapp/*"
      capabilities: ["read", "list"]
```

### CSI Provider — Инъекция в поды

Прямая инъекция секретов в поды через CSI:

```yaml
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: kubebao-secrets
spec:
  provider: kubebao
  parameters:
    roleName: "my-app-role"
    objects: |
      - objectName: "password"
        secretPath: "myapp/database"
        secretKey: "password"
---
apiVersion: v1
kind: Pod
metadata:
  name: my-app
spec:
  containers:
    - name: app
      image: myapp:latest
      volumeMounts:
        - name: secrets
          mountPath: "/mnt/secrets"
          readOnly: true
  volumes:
    - name: secrets
      csi:
        driver: secrets-store.csi.k8s.io
        readOnly: true
        volumeAttributes:
          secretProviderClass: "kubebao-secrets"
```

## Configuration

### Helm Values

| Параметр | Описание | По умолчанию |
|----------|----------|--------------|
| `global.openbao.address` | Адрес OpenBao сервера | `""` |
| `global.openbao.role` | Роль для Kubernetes auth | `kubebao` |
| `operator.enabled` | Включить Operator | `true` |
| `kms.enabled` | Включить KMS Plugin | `true` |
| `kms.encryptionProvider` | `transit` (OpenBao) или `kuznyechik` (ГОСТ) | `transit` |
| `csi.enabled` | Включить CSI Provider | `true` |
| `csi.enableSecretRotation` | Автообновление секретов | `true` |

Полный список параметров: [values.yaml](charts/kubebao/values.yaml)

### Kuznyechik (GOST R 34.12-2015)

Для использования российских алгоритмов шифрования включите провайдер `kuznyechik`:

```yaml
# Helm values
kms:
  encryptionProvider: kuznyechik
  keyName: kubebao-kms
  kuznyechik:
    kvPathPrefix: kubebao/kms-keys
```

Или через переменные окружения:
- `KUBEBAO_KMS_PROVIDER=kuznyechik`
- `KUBEBAO_KMS_KV_PREFIX=kubebao/kms-keys`

Ключи шифрования (KEK) хранятся в OpenBao KV по пути `secret/data/{kvPathPrefix}/{keyName}`. Требуется **KV v2** (не Transit).

### OpenBao Configuration

KubeBao требует настроенный OpenBao с:

1. **Kubernetes Auth Method**
2. **KV Secrets Engine v2** (обязательно для kuznyechik, опционально для transit)
3. **Transit Secrets Engine** (только для KMS с `encryptionProvider: transit`)

Пример настройки:

```bash
# Включение Kubernetes auth
bao auth enable kubernetes
bao write auth/kubernetes/config \
    kubernetes_host="https://kubernetes.default.svc"

# Создание роли
bao write auth/kubernetes/role/kubebao \
    bound_service_account_names=kubebao \
    bound_service_account_namespaces=kubebao-system \
    policies=kubebao-policy \
    ttl=1h

# Политика
bao policy write kubebao-policy - <<EOF
path "secret/*" {
  capabilities = ["read", "list"]
}
EOF
```

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Kubernetes Cluster                        │
│  ┌───────────────┐  ┌───────────────┐  ┌───────────────┐       │
│  │   KubeBao     │  │   KubeBao     │  │   KubeBao     │       │
│  │   Operator    │  │   KMS Plugin  │  │ CSI Provider  │       │
│  └───────┬───────┘  └───────┬───────┘  └───────┬───────┘       │
│          │                  │                  │                │
│          ▼                  ▼                  ▼                │
│  ┌───────────────────────────────────────────────────────┐     │
│  │                      OpenBao                           │     │
│  │  ┌─────────┐  ┌─────────┐  ┌─────────┐               │     │
│  │  │   KV    │  │ Transit │  │  Auth   │               │     │
│  │  │ Engine  │  │ Engine  │  │  K8s    │               │     │
│  │  └─────────┘  └─────────┘  └─────────┘               │     │
│  └───────────────────────────────────────────────────────┘     │
└─────────────────────────────────────────────────────────────────┘
```

## Documentation

- [Installation Guide](docs/installation.md)
- [Configuration Reference](docs/configuration.md)
- [BaoSecret CRD](docs/api/baosecret.md)
- [BaoPolicy CRD](docs/api/baopolicy.md)
- [CSI Provider](docs/csi-provider.md)
- [KMS Plugin](docs/kms-plugin.md)
- [Troubleshooting](docs/troubleshooting.md)

## Development

### Требования для разработки

- Go 1.23+
- Docker
- Minikube
- Make

### Локальная разработка

```bash
# Клонирование репозитория
git clone https://github.com/kubebao/kubebao.git
cd kubebao

# Полная установка локального окружения
./scripts/setup-all.sh

# Запуск тестов
./scripts/e2e-test.sh

# Очистка
./scripts/cleanup.sh
```

### Сборка

```bash
# Сборка всех компонентов
make build

# Сборка Docker образов
make docker-build

# Запуск unit-тестов
make test
```

## Contributing

Мы приветствуем вклад в развитие проекта! Пожалуйста, ознакомьтесь с [CONTRIBUTING.md](CONTRIBUTING.md).

1. Fork репозитория
2. Создайте feature branch (`git checkout -b feature/amazing-feature`)
3. Commit изменения (`git commit -m 'Add amazing feature'`)
4. Push в branch (`git push origin feature/amazing-feature`)
5. Откройте Pull Request

## License

Distributed under the Apache License 2.0. See [LICENSE](LICENSE) for more information.

## Acknowledgments

- [OpenBao](https://openbao.org/) — Open source secrets management
- [Kubernetes Secrets Store CSI Driver](https://github.com/kubernetes-sigs/secrets-store-csi-driver)
- [HashiCorp Vault](https://www.vaultproject.io/) — Original inspiration

---

<p align="center">
  Made with ❤️ for the Kubernetes community
</p>
 
 