# Проверка CI/CD и GitOps

Быстрый CI [проверен](../verification/evidence-restoration-ci.md). Контейнерный workflow и ограниченный kind smoke подготовлены для приёмки в отдельном PR. Архитектурные границы — в [контракте](../design/ci-cd-gitops.md).

Перед отправкой изменений выполнить Go test/race/vet/build, evidence tests, actionlint и `bash scripts/ci-helm.sh`. Последняя команда использует только локальное формирование YAML и не меняет кластер. Для Windows доступны эквивалентные `helm lint` / `helm template`; установленные версии фиксируются в отчёте.

После разрешённых владельцем commit/push дождаться jobs workflow CI в GitHub Actions. Сохранить run URL/ID, точный Git SHA, runner и результаты каждого job. При отказе сохранить диагностику и повторить после исправления. До этого workflow остаётся Prepared.

Контейнерный workflow `container.yml` выполняет Buildx, govulncheck, Trivy и передаёт проверенный image archive в отдельный kind job. `scripts/ci-kind.sh` создаёт исключительно свой временный кластер `lab-ci-*`, использует отдельный kubeconfig, проверяет `/readyz` и `/version` из client Pod через Service DNS, ломает selector только этого Service и подтверждает отказ и восстановление. Диагностика сохраняется перед удалением собственного временного кластера. Команды: `task ci-container`; для Linux `IMAGE=lab-ci-api:<revision> REVISION=<revision> task ci-kind`. Локальные образы имеют dirty revision до фиксации исходников.

GHCR publication и runtime GitOps остаются незавершёнными. Profiles и Argo CD Applications в `gitops/` имеют статус Prepared: digest/tag пусты. Не применять их к действующим кластерам. Для первого GitOps sync потребуется отдельный кластер и одобренный опубликованный digest. Dev проверяется перед stable; переносить только тот же digest, сохраняя source и promotion SHA. Release gate ещё не включён в required checks.

После отдельного разрешения владельца включить repository variable `ENABLE_GHCR_PUBLISH=true`. Последующий соответствующий push в dev запускает `publish.yml`, ожидающий успешные CI для того же SHA. Workflow не собирает новый образ: загружает `checked-api`, проверяет identity, публикует его и сохраняет `promotion-candidate/candidate.json`. До разрешения переменную не устанавливать. Перед GitOps pull отдельно подтвердить доступ к package; токены и kubeconfig не вносить в Git. По окончании публикации дождаться registry digest; локальный image ID не подменяет его.

## Защита main

Проверка 2 ноября 2026 года: репозиторий Public. GitHub API обеих веток `dev` и `main` подтвердил обязательный PR, checks `go`, `evidence`, `helm` от GitHub Actions (app ID 15368), strict=true, применение к администраторам, запрет force push и удаления. Обязательных чужих approvals нет: владелец может работать один. PR №1 закрыт с merged=true, merge SHA `b34fe8b19da5cfa67e0b16745586a3b2d18f1568`; CI подтверждён на PR и push в dev.

Новые required checks включать только после успешного запуска на соответствующих PR. Дополнительный release gate для main пока Target; базовая защита не доказывает успешный promotion. Доступность защиты описана в [документации GitHub](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches).

После успешного PR №2 для dev через API добавлены required checks `container`, `kind`, с сохранением `go`, `evidence`, `helm` и strict=true. На push merge SHA `6f6521d` все пять checks успешны. Публикация `41394590401` пропущена: `ENABLE_GHCR_PUBLISH` отсутствует; registry digest ещё не получен. Для публикации уже проверенного dev archive после отдельного разрешения владельца нужно включить переменную и повторно запустить этот publishing run, а не пересобирать образ.
