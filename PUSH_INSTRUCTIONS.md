# Инструкция: Публикация KubeBao на GitHub

Пошаговая инструкция для первого push репозитория.

## 1. Создайте репозиторий на GitHub

1. Перейдите на https://github.com/new
2. **Repository name:** `kubebao`
3. **Description:** `Kubernetes Secrets Management System powered by OpenBao`
4. Выберите **Public**
5. **НЕ** добавляйте README, .gitignore, LICENSE
6. Нажмите **Create repository**

## 2. Выполните команды в терминале

Замените `YOUR_USERNAME` на ваш GitHub username (или имя организации):

```powershell
cd "e:\kubebao-main\kubebao-main"

# Инициализация Git
git init

# Добавить все файлы
git add .

# Первый коммит
git commit -m "Initial commit: KubeBao v0.1.0 with Kuznyechik support"

# Переименовать ветку в main
git branch -M main

# Добавить remote (замените YOUR_USERNAME!)
git remote add origin https://github.com/YOUR_USERNAME/kubebao.git

# Push
git push -u origin main
```

## 3. Настройте GitHub

### 3.1 Actions

**Settings** → **Actions** → **General**:
- Workflow permissions: **Read and write permissions**

### 3.2 GitHub Pages (для Helm)

**Settings** → **Pages**:
- Source: **Deploy from a branch**
- Branch: `gh-pages` / `/(root)`

> Ветка `gh-pages` создастся автоматически при первом push в `main` (workflow helm-publish) или при создании тега (release).

## 4. Первый push — что произойдёт

После `git push origin main`:
- ✅ **CI** запустится: lint, тесты, сборка, Docker images
- ✅ **Helm Publish** соберёт chart и создаст ветку `gh-pages`

## 5. Первый релиз (опционально)

```powershell
git tag -a v0.1.0 -m "Release v0.1.0"
git push origin v0.1.0
```

Будет выполнено:
- Сборка Docker образов (linux/amd64, linux/arm64)
- Публикация Helm chart в gh-pages
- Создание GitHub Release

## 6. Установка через Helm

После публикации:

```bash
helm repo add kubebao https://YOUR_USERNAME.github.io/kubebao
helm repo update
helm install kubebao kubebao/kubebao --namespace kubebao-system --create-namespace
```

## Устранение проблем

| Проблема | Решение |
|----------|---------|
| CI не запускается | Проверьте, что push был в ветку `main` |
| gh-pages не создаётся | Запустите workflow "Publish Helm Chart" вручную (Actions → Run workflow) |
| Docker образы не публикуются | Нужны права на Packages в настройках репозитория |
