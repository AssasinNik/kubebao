# Contributing to KubeBao

Спасибо за интерес к развитию KubeBao! Мы приветствуем любой вклад.

## Code of Conduct

Участвуя в проекте, вы соглашаетесь соблюдать уважительное отношение к другим участникам.

## How to Contribute

### Reporting Bugs

1. Проверьте [Issues](https://github.com/kubebao/kubebao/issues) — возможно, проблема уже известна
2. Создайте новый Issue с тегом `bug`
3. Опишите:
   - Версию KubeBao
   - Версию Kubernetes
   - Шаги для воспроизведения
   - Ожидаемое поведение
   - Фактическое поведение
   - Логи (если применимо)

### Suggesting Features

1. Создайте Issue с тегом `enhancement`
2. Опишите:
   - Use case
   - Предлагаемое решение
   - Альтернативы (если рассматривали)

### Pull Requests

1. Fork репозитория
2. Создайте feature branch:
   ```bash
   git checkout -b feature/my-feature
   ```
3. Внесите изменения
4. Добавьте тесты
5. Убедитесь что все тесты проходят:
   ```bash
   make test
   ./scripts/e2e-test.sh
   ```
6. Commit с осмысленным сообщением:
   ```bash
   git commit -m "feat: add amazing feature"
   ```
7. Push:
   ```bash
   git push origin feature/my-feature
   ```
8. Откройте Pull Request

## Development Setup

### Requirements

- Go 1.23+
- Docker
- Minikube
- Make
- Helm 3+

### Quick Start

```bash
# Клонируйте репозиторий
git clone https://github.com/kubebao/kubebao.git
cd kubebao

# Полная установка локального окружения
./scripts/setup-all.sh

# Запуск тестов
./scripts/e2e-test.sh

# Очистка
./scripts/cleanup.sh
```

### Building

```bash
# Сборка всех компонентов
make build

# Сборка конкретного компонента
go build -o bin/kubebao-operator ./cmd/kubebao-operator

# Сборка Docker образов
make docker-build
```

### Testing

```bash
# Unit тесты
make test

# E2E тесты (требуют запущенный кластер)
./scripts/e2e-test.sh

# Быстрые E2E тесты
./scripts/e2e-test.sh --quick
```

## Project Structure

```
kubebao/
├── cmd/                    # Entry points
│   ├── kubebao-kms/       # KMS plugin
│   ├── kubebao-csi/       # CSI provider
│   └── kubebao-operator/  # Kubernetes operator
├── internal/              # Internal packages
│   ├── controller/        # Kubernetes controllers
│   ├── csi/              # CSI implementation
│   ├── kms/              # KMS implementation
│   └── openbao/          # OpenBao client
├── api/                   # API definitions
│   └── v1alpha1/         # CRD types
├── config/               # Kubernetes manifests
│   └── crd/             # CRD definitions
├── charts/               # Helm chart
│   └── kubebao/
├── scripts/              # Utility scripts
├── docs/                 # Documentation
└── examples/             # Usage examples
```

## Coding Guidelines

### Go

- Следуйте [Effective Go](https://golang.org/doc/effective_go)
- Используйте `gofmt` для форматирования
- Все экспортируемые функции должны иметь комментарии
- Покрытие тестами > 70%

### Commit Messages

Используйте [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add new feature
fix: fix bug
docs: update documentation
test: add tests
refactor: refactor code
chore: update dependencies
```

### Pull Request Checklist

- [ ] Код соответствует стилю проекта
- [ ] Добавлены тесты
- [ ] Все тесты проходят
- [ ] Обновлена документация
- [ ] Добавлена запись в CHANGELOG.md (для значимых изменений)

## Release Process

Релизы создаются автоматически при создании Git tag:

```bash
git tag v1.0.0
git push origin v1.0.0
```

GitHub Actions автоматически:
1. Соберёт и опубликует Docker образы
2. Опубликует Helm chart
3. Создаст GitHub Release

## Getting Help

- [Issues](https://github.com/kubebao/kubebao/issues) — баги и features
- [Discussions](https://github.com/kubebao/kubebao/discussions) — вопросы и обсуждения

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.
