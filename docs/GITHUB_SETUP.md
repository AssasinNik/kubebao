# Настройка GitHub репозитория для KubeBao

Пошаговая инструкция по размещению проекта на GitHub и настройке автоматической публикации.

## 1. Создание репозитория

### На GitHub.com:

1. Перейдите на https://github.com/new
2. Имя репозитория: `kubebao`
3. Описание: "Kubernetes Secrets Management System powered by OpenBao"
4. Выберите **Public**
5. **НЕ** добавляйте README, .gitignore, LICENSE (они уже есть)
6. Нажмите **Create repository**

### Инициализация локального репозитория:

```bash
cd /path/to/kubebao

# Инициализация Git (если ещё не сделано)
git init

# Добавление всех файлов
git add .

# Первый коммит
git commit -m "Initial commit: KubeBao v0.1.0"

# Добавление remote
git remote add origin git@github.com:YOUR_USERNAME/kubebao.git

# Push в main
git branch -M main
git push -u origin main
```

## 2. Настройка GitHub Pages для Helm

### Создание ветки gh-pages:

```bash
# Создаём пустую ветку
git checkout --orphan gh-pages

# Удаляем все файлы
git rm -rf .

# Создаём index.yaml для Helm
cat > index.yaml << 'EOF'
apiVersion: v1
entries: {}
generated: "2024-01-01T00:00:00Z"
EOF

# Коммит и push
git add index.yaml
git commit -m "Initialize Helm repository"
git push origin gh-pages

# Возврат на main
git checkout main
```

### Включение GitHub Pages:

1. Перейдите в **Settings** → **Pages**
2. Source: **Deploy from a branch**
3. Branch: `gh-pages` / `/ (root)`
4. Нажмите **Save**

После этого Helm repo будет доступен по адресу:
```
https://YOUR_USERNAME.github.io/kubebao
```

## 3. Настройка GitHub Container Registry (GHCR)

### Включение пакетов:

1. Перейдите в **Settings** → **Packages**
2. Убедитесь что Container registry включен

### Настройка прав для Actions:

1. **Settings** → **Actions** → **General**
2. В разделе "Workflow permissions":
   - Выберите **Read and write permissions**
   - ✅ Allow GitHub Actions to create and approve pull requests

## 4. Первый релиз

### Создание тега:

```bash
# Убедитесь что вы на main
git checkout main
git pull

# Создайте тег
git tag -a v0.1.0 -m "Release v0.1.0"

# Push тега
git push origin v0.1.0
```

GitHub Actions автоматически:
- Соберёт Docker образы для linux/amd64 и linux/arm64
- Опубликует образы в GHCR
- Опубликует Helm chart
- Создаст GitHub Release

## 5. Проверка публикации

### Helm repo:

```bash
# Добавление репозитория
helm repo add kubebao https://YOUR_USERNAME.github.io/kubebao
helm repo update

# Поиск charts
helm search repo kubebao

# Просмотр доступных версий
helm search repo kubebao --versions
```

### Docker образы:

```bash
# Pull образов
docker pull ghcr.io/YOUR_USERNAME/kubebao-operator:v0.1.0
docker pull ghcr.io/YOUR_USERNAME/kubebao-kms:v0.1.0
docker pull ghcr.io/YOUR_USERNAME/kubebao-csi:v0.1.0
```

## 6. Обновление Helm values для production

Обновите `charts/kubebao/values.yaml` для использования GHCR:

```yaml
global:
  image:
    registry: ghcr.io
    pullPolicy: IfNotPresent
    tag: ""  # Defaults to appVersion

kms:
  image:
    repository: YOUR_USERNAME/kubebao-kms

csi:
  image:
    repository: YOUR_USERNAME/kubebao-csi

operator:
  image:
    repository: YOUR_USERNAME/kubebao-operator
```

## 7. Документация

### Настройка Wiki:

1. **Settings** → **Features** → ✅ Wikis
2. Создайте страницы документации

### Настройка Discussions:

1. **Settings** → **Features** → ✅ Discussions
2. Создайте категории: Q&A, Ideas, Show and tell

## 8. Badges для README

Добавьте актуальные badges в README.md:

```markdown
[![Release](https://img.shields.io/github/v/release/YOUR_USERNAME/kubebao?style=flat-square)](https://github.com/YOUR_USERNAME/kubebao/releases)
[![Build](https://img.shields.io/github/actions/workflow/status/YOUR_USERNAME/kubebao/ci.yaml?style=flat-square)](https://github.com/YOUR_USERNAME/kubebao/actions)
[![Go Report](https://goreportcard.com/badge/github.com/YOUR_USERNAME/kubebao?style=flat-square)](https://goreportcard.com/report/github.com/YOUR_USERNAME/kubebao)
[![License](https://img.shields.io/github/license/YOUR_USERNAME/kubebao?style=flat-square)](LICENSE)
```

## 9. Защита веток

### Настройка branch protection:

1. **Settings** → **Branches** → **Add rule**
2. Branch name pattern: `main`
3. ✅ Require a pull request before merging
4. ✅ Require status checks to pass before merging
   - Добавьте: lint, test, build
5. ✅ Require branches to be up to date before merging

## 10. Secrets (опционально)

Если нужны дополнительные секреты для CI/CD:

1. **Settings** → **Secrets and variables** → **Actions**
2. Добавьте необходимые секреты

## Итоговая структура

После настройки у вас будет:

```
https://github.com/YOUR_USERNAME/kubebao              # Репозиторий
https://YOUR_USERNAME.github.io/kubebao               # Helm repo
https://github.com/YOUR_USERNAME/kubebao/releases     # Releases
ghcr.io/YOUR_USERNAME/kubebao-operator               # Docker image
ghcr.io/YOUR_USERNAME/kubebao-kms                    # Docker image
ghcr.io/YOUR_USERNAME/kubebao-csi                    # Docker image
```

## Использование пользователями

Пользователи смогут установить KubeBao одной командой:

```bash
# Добавить репозиторий
helm repo add kubebao https://YOUR_USERNAME.github.io/kubebao

# Установить
helm install kubebao kubebao/kubebao \
  --namespace kubebao-system \
  --create-namespace \
  --set global.openbao.address="http://your-openbao:8200"
```
