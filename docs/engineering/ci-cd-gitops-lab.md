# Проверка CI/CD и GitOps

Текущее выполнение ограничено [проверенным быстрым CI](../verification/evidence-restoration-ci.md). Архитектурные границы — в [контракте](../design/ci-cd-gitops.md).

Перед отправкой изменений выполнить Go test/race/vet/build, evidence tests, actionlint и `bash scripts/ci-helm.sh`. Последняя команда использует только локальное формирование YAML и не меняет кластер. Для Windows доступны эквивалентные `helm lint` / `helm template`; установленные версии фиксируются в отчёте.

После разрешённых владельцем commit/push дождаться jobs workflow CI в GitHub Actions. Сохранить run URL/ID, точный Git SHA, runner и результаты каждого job. При отказе сохранить диагностику и повторить после исправления. До этого workflow остаётся Prepared.

Контейнерный workflow, GHCR и отдельное GitOps-окружение ещё требуют реализации. Команды установки Argo CD и fault experiments появятся после подготовки исполняемой конфигурации и выбора изолированного окружения; текущая инструкция не разрешает менять действующий кластер. Public visibility требует отдельного решения владельца.

## Защита main

Проверка 2 ноября 2026 года: репозиторий Private, права администратора доступны. GitHub API для branch protection и rulesets возвращает HTTP 403: `Upgrade to GitHub Pro or make this repository public to enable this feature.` Поэтому обязательные status checks на уровне GitHub не настроены; успешный workflow сам по себе не запрещает слияние или прямой push. Visibility и тариф не изменялись.

До появления доступной защиты изменения следует проверять через PR и дожидаться успешных `go`, `evidence`, `helm`. Это ручной порядок, технической блокировки слияния сейчас нет. После подключения подходящего тарифа настроить защиту `main`: обязательный PR, обязательные checks `go`, `evidence`, `helm` от GitHub Actions (app ID 15368), актуальность ветки перед слиянием, применение правил к администраторам, запрет force push и удаления ветки. Эти настройки пока только целевые. Доступность защиты описана в [документации GitHub](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches).
