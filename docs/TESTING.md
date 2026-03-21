# Тестирование KubeBao

## Содержание

1. [Функциональное тестирование](#1-функциональное-тестирование)
2. [Нефункциональное тестирование](#2-нефункциональное-тестирование)
3. [Запуск тестов](#3-запуск-тестов)
4. [Матрица тестовых сценариев](#4-матрица-тестовых-сценариев)

---

## 1. Функциональное тестирование

### 1.1 Модульные тесты шифра «Кузнечик» (ГОСТ Р 34.12-2015)

| ID | Сценарий | Ожидаемый результат |
|---|---|---|
| FT-C-01 | Шифрование с тестовым вектором ГОСТ Р 34.12-2015 (Приложение А.1) | Шифротекст `7f679d90bebc24305a468d42b9d4edcd` |
| FT-C-02 | Дешифрование с тестовым вектором ГОСТ | Открытый текст `1122334455667700ffeeddccbbaa9988` |
| FT-C-03 | Encrypt → Decrypt roundtrip (произвольные данные) | Совпадение |
| FT-C-04 | Ключ неверной длины (не 32 байта) | Ошибка |
| FT-C-05 | BlockSize() = 16 | 16 |

**Расположение:** `internal/kuznyechik/cipher_test.go`

### 1.2 Модульные тесты AEAD (ГОСТ Р 34.13-2015 CTR + CMAC)

| ID | Сценарий | Ожидаемый результат |
|---|---|---|
| FT-A-01 | Encrypt → Decrypt roundtrip | Совпадение открытого текста |
| FT-A-02 | Пустой plaintext | Корректный overhead (33 байта) |
| FT-A-03 | Большой plaintext (64 КБ) | Корректный roundtrip |
| FT-A-04 | Неверный размер ключа | `ErrInvalidKeySize` |
| FT-A-05 | Модификация шифротекста | `ErrAuthFailed` |
| FT-A-06 | Модификация CMAC-тега | `ErrAuthFailed` |
| FT-A-07 | Модификация IV | `ErrAuthFailed` |
| FT-A-08 | Слишком короткий шифротекст | `ErrInvalidCiphertext` |
| FT-A-09 | Неверная версия формата | `ErrUnsupportedVersion` |
| FT-A-10 | Дешифрование чужим ключом | `ErrAuthFailed` |
| FT-A-11 | Два шифрования одного текста → различные IV | Различные шифротексты |

**Расположение:** `internal/crypto/kuznyechik_mgm_test.go`

### 1.3 Тесты ГОСТ-примитивов

| ID | Сценарий | Ожидаемый результат |
|---|---|---|
| FT-G-01 | CTR инкремент нижних 64 бит | Корректный инкремент |
| FT-G-02 | CTR инкремент с переполнением | Верхние 64 бит не изменяются |
| FT-G-03 | CMAC подключи K1 ≠ K2 ≠ 0 | Различные ненулевые подключи |
| FT-G-04 | shiftLeft (1-битовый сдвиг влево) | Корректный сдвиг |

**Расположение:** `internal/crypto/kuznyechik_mgm_test.go`

### 1.4 Интеграционные тесты (E2E)

| ID | Сценарий | Ожидаемый результат |
|---|---|---|
| FT-E-01 | BaoSecret: создание → появление K8s Secret | Secret создан с данными из OpenBao |
| FT-E-02 | BaoSecret: обновление секрета в OpenBao → обновление K8s Secret | Данные обновлены после `refreshInterval` |
| FT-E-03 | BaoSecret: удаление BaoSecret с `Owner` policy → удаление Secret | Secret удалён |
| FT-E-04 | BaoSecret: `suspendSync: true` → синхронизация остановлена | Secret не обновляется |
| FT-E-05 | BaoPolicy: создание → политика появляется в OpenBao | Политика читается через API |
| FT-E-06 | CSI: SecretProviderClass + Pod → секреты доступны в файловой системе | Файлы с секретами смонтированы |
| FT-E-07 | KMS: шифрование → дешифрование через gRPC | Plaintext совпадает |

---

## 2. Нефункциональное тестирование

### 2.1 Тесты производительности (бенчмарки)

| ID | Сценарий | Метрика |
|---|---|---|
| NF-P-01 | Шифрование блока (16 байт) | > 100 MB/s |
| NF-P-02 | Дешифрование блока (16 байт) | > 100 MB/s |
| NF-P-03 | AEAD Encrypt 64 байт | < 5 мкс |
| NF-P-04 | AEAD Encrypt 1 КБ | < 25 мкс |
| NF-P-05 | AEAD Encrypt 64 КБ | < 2 мс |
| NF-P-06 | AEAD Decrypt 1 КБ | < 25 мкс |

**Расположение:** `internal/kuznyechik/cipher_test.go`, `internal/crypto/kuznyechik_mgm_test.go`

### 2.2 Тесты безопасности

| ID | Сценарий | Критерий |
|---|---|---|
| NF-S-01 | Модификация 1 бита шифротекста → CMAC fail | 100% обнаружение |
| NF-S-02 | Модификация 1 бита тега → CMAC fail | 100% обнаружение |
| NF-S-03 | Модификация 1 бита IV → CMAC fail | 100% обнаружение |
| NF-S-04 | Дешифрование другим ключом → fail | 100% отказ |
| NF-S-05 | Уникальность IV при множественном шифровании | Все IV различны |
| NF-S-06 | Race condition detector | Отсутствие data race |
| NF-S-07 | Зануление ключевого материала в памяти | `defer zeroSlice()` во всех путях |

### 2.3 Тесты надёжности

| ID | Сценарий | Критерий |
|---|---|---|
| NF-R-01 | OpenBao недоступен → KMS health = unhealthy | Статус корректно обновляется |
| NF-R-02 | OpenBao восстановлен → KMS health = ok | Автоматическое восстановление |
| NF-R-03 | Graceful shutdown по SIGTERM | Завершение без потери данных |
| NF-R-04 | gRPC keepalive и reconnect | Соединение восстанавливается |

---

## 3. Запуск тестов

### 3.1 Все модульные тесты

```bash
make test
# или напрямую:
go test -v -race -coverprofile=coverage.out ./...
```

### 3.2 Только криптографические тесты

```bash
# Шифр Кузнечик (ГОСТ Р 34.12-2015)
go test -v -race ./internal/kuznyechik/

# AEAD (ГОСТ Р 34.13-2015 CTR + CMAC)
go test -v -race ./internal/crypto/
```

### 3.3 Бенчмарки производительности

```bash
# Все бенчмарки
go test -bench=. -benchmem ./internal/kuznyechik/ ./internal/crypto/

# Только шифр
go test -bench=BenchmarkEncrypt -benchmem ./internal/kuznyechik/

# Только AEAD
go test -bench=BenchmarkAEAD -benchmem ./internal/crypto/
```

### 3.4 Покрытие кода

```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
open coverage.html
```

### 3.5 Линтинг

```bash
make lint
# или:
golangci-lint run ./...
```

### 3.6 E2E тестирование

Требуется работающий кластер Kubernetes с развёрнутым OpenBao:

```bash
# 1. Развернуть окружение (см. docs/DEPLOYMENT.md)
# 2. Запустить E2E тесты
make test-e2e

# 3. Быстрые E2E тесты (только smoke)
make test-e2e-quick
```

Ручная проверка E2E:

```bash
# BaoSecret
kubectl apply -f config/samples/baosecret_sample.yaml
kubectl get baosecrets -o wide
kubectl get secret my-app-secret -o yaml

# CSI
kubectl apply -f config/samples/secretproviderclass_sample.yaml
# (создать Pod с монтированием — см. docs/DEPLOYMENT.md)

# BaoPolicy
kubectl apply -f config/samples/baopolicy_sample.yaml
kubectl get baopolicies -o wide
```

---

## 4. Матрица тестовых сценариев

| Уровень | Кол-во тестов | Автоматизация | Файлы |
|---|---|---|---|
| Шифр Кузнечик | 5 + 2 bench | CI | `internal/kuznyechik/cipher_test.go` |
| AEAD CTR+CMAC | 15 + 4 bench | CI | `internal/crypto/kuznyechik_mgm_test.go` |
| KMS Server | E2E | Ручное | `config/samples/` |
| Operator | E2E | Ручное | `config/samples/baosecret_sample.yaml` |
| CSI Provider | E2E | Ручное | `config/samples/secretproviderclass_sample.yaml` |
| Helm Chart | CI | CI | `helm lint ./charts/kubebao` |

**Итого:** 20 автоматизированных unit-тестов + 6 бенчмарков + 7 E2E сценариев.
