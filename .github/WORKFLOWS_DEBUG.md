# Почему пайплайн не запускается

## 1. Проверьте, что Actions включены

**Settings** → **Actions** → **General**:
- **Actions permissions**: выберите "Allow all actions and reusable workflows"
- **Workflow permissions**: "Read and write permissions"

## 2. Проверьте ветку по умолчанию

**Settings** → **General** → **Default branch**:
- Должна быть `main` (или `master`)
- Workflows в `.github/workflows/` должны быть в этой ветке

## 3. Ручной запуск

1. **Actions** → выберите workflow **CI**
2. **Run workflow** → **Run workflow**

Если запустится — workflows работают, проблема была в триггерах.

## 4. Проверьте историю push

Убедитесь, что последний push действительно ушёл в ветку `main`:
- **Code** → выберите ветку `main`
- Проверьте наличие файлов в `.github/workflows/`
