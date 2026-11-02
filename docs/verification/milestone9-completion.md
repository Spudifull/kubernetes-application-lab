# Завершающая приёмка Milestone 9

Исходное состояние: dev `f70e723129278d2f8ddbb843d0cdec5733486d3b`, main `50f80f452bf168330ed7025e7d7679ec97bfb1c0`, одинаковые trees. Ветка `codex/ci-gitops-completion` создана от актуальной dev и fast-forward до main для strict base. Исторические результаты, image source `6f6521d` и promotion `b2b8ccb` сохранены; новый образ не публикуется.

Статус: Passed для новых CI-сценариев и перечисленных локальных команд. Milestone 9 — Done в границах stateless лабораторной цепочки; исключения перечислены ниже.

Изменены `.github/workflows/ci.yml`, `.github/workflows/container.yml`, `.github/workflows/release-gate.yml`, `scripts/ci-images.sh`, `scripts/ci-kind.sh`, `scripts/ci-release-proof.sh`, `scripts/ci-release-proof-test.sh`, `scripts/gitops-lab.ps1`, `Taskfile.yml`, `gitops/argocd/applications.yaml`, `docs/design/ci-cd-gitops.md`, `docs/engineering/ci-cd-gitops-lab.md`, `docs/roadmap.md` и этот отчёт. Контейнеры publisher/worker включены в один required container job; добавлены реальный bad rollout, EndpointSlice assertions и логи. GitOps-команды используют только выделенный kubeconfig. Release gate сохраняет доверие к уже принятой identity через protected main base; новые кандидаты требуют оригинального CI artifact. Тест проверяет reuse принятой identity без artifact, новый run ID и отклонение подменённого config digest.

2 ноября 2026 года локально прошли Go test/race/vet/build, actionlint 1.7.7, Bash syntax check и PowerShell parser, release-check, Helm lint/render 21 профиля и двух GitOps profiles. `gitops-preflight` и `gitops-status` подтвердили только выделенный GitOps-кластер; оба приложения Synced/Healthy, по одному Ready Pod. `gitops-smoke` успешно выполнен в обоих namespaces: readyz, HTTP 200, version revision `6f6521d81226931f42edd2783584cb33c8fc8519`, imageID `ghcr.io/spudifull/kubernetes-application-lab-api@sha256:b5a4ea8debd055b9f051b7ac449a5d65cca5fda97685fd203df83bd9fb44e677`. Созданные diagnostic Pod удалены в finally; исходные workload Pod сохранены.

Связанные доказательства полного выпуска, drift/self-heal и recovery — [runtime-отчёт](gitops-development.md). 22 исторических файла Milestone 8 не изменялись. Full-stack SQL/Kafka регрессия, сетевая изоляция, отдельный отказ stable, bootstrap нового кластера и фактическое удаление существующего стенда этим прогоном не проверяются. Ветви fresh bootstrap/cleanup имеют Prepared; успешный status не означает их Passed.

Повторный `task gitops-install` на существующем стенде прошёл: checksum закреплённого manifest совпал, Argo Core Ready, AppProject неизменён, Applications остались Synced/Healthy. Cleanup с `-ConfirmCluster wrong-name` завершился ожидаемым отказом; кластер сохранён. Новое создание и настоящее удаление не выполнялись. Наблюдения smoke сохранены в [development](milestone9/completion-dev.json) и [stable](milestone9/completion-stable.json).

Первый CI run PR №10 `41493543316` обнаружил ShellCheck SC2034: переменная registry осталась неиспользованной после переноса сверки в отдельный скрипт. Переменная удалена, проверки не ослаблялись. Локальный actionlint без установленного ShellCheck этого предупреждения не обнаружил; результат GitHub CI является обязательным.

В Linux-контейнере Alpine 3.23 (digest `sha256:ff7f0c727dd796aa36787bfcd9a64bf35a6ce72a73451b3359fde849b6703d63`) с read-only исходниками прошли ShellCheck четырёх Bash scripts и три сценария границы доверия release proof. Для ожидания Service добавлен вывод номера попытки вместо неиспользуемой переменной. ShellCheck внешних scripts добавлен в evidence job, чтобы эти проверки постоянно выполнялись в CI.

## GitHub CI и фактические результаты

[PR №10](https://github.com/Spudifull/kubernetes-application-lab/pull/10), head `68d34ce95b9ff553dff04bbc9acdba365ffa877f`, слит в dev как `6278b2871dac8e7a4761b761f58a62f56d08ca6d`. Все required checks успешны: [CI 41493918255](https://github.com/Spudifull/kubernetes-application-lab/actions/runs/41493918255) (go/evidence/helm), [container/kind 41493918264](https://github.com/Spudifull/kubernetes-application-lab/actions/runs/41493918264). Проверялся GitHub merge ref `c7842d0e14549b2322c5bde3ea11f308d27a8cd6`; он не является source SHA опубликованного GHCR image.

Derived [квитанция CI](milestone9/completion-ci.json) извлечена из реально скачанных container/kind artifacts. SHA-256 ZIP сверены с GitHub API; сохранены job IDs, component image/config IDs, hashes govulncheck, нулевые HIGH/CRITICAL Trivy 0.76.1, настоящий ProgressDeadlineExceeded и ErrImageNeverPull, по одному Ready EndpointSlice address до/после recovery и все четыре успешных отрицательных/recovery assertions. Журнал приложения непустой, его hash сохранён. Это выбранная проекция первичных Actions artifacts, не подмена исходных результатов.

После merge выполнены `task gitops-sync GITOPS_ENV=lab-dev GITOPS_REVISION=6278b2871dac8e7a4761b761f58a62f56d08ca6d` и smoke. [Отдельное наблюдение](milestone9/completion-dev-synced.json): last sync соответствует точному merge SHA, Synced/Healthy, HTTP 200; исходный GHCR digest и workload Pod UID сохранены. В изолированной Git-копии проверен reuse настоящей identity из main base `50f80f4` без скачивания artifacts. Никакие GHCR packages или новые image не публиковались; publishing flag отсутствует.

Документирование результата дополнительно меняет `README.md`, `docs/verification/gitops-development.md` и добавляет `docs/verification/milestone9/completion-{dev,stable,ci,dev-synced}.json`. Markdown-файлы в этой итерации не удалялись. Коммиты реализации: `48affb7` — «Завершены проверки контейнеров и команды GitOps», `68d34ce` — «Исправлена проверка scripts и подтверждены команды стенда»; merge `6278b28` имеет русский заголовок. История и авторство не переписывались.

В `.gitleaks.toml` добавлено узкое исключение точной строки `lab-ci-api:c7842d0e14549b2322c5bde3ea11f308d27a8cd6`: generic-api-key ошибочно распознал публичный image tag из архивируемого CI receipt. Это проверенный merge SHA, не credential. Новые значения и другие строки этим исключением не покрываются; повторный staged scan обязателен.

Push/dev run `41494877715` после первого merge обнаружил гонку recovery: после возврата правильного image `kubectl rollout status` прочитал прежнее ProgressDeadlineExceeded до обработки новой generation controller. Container job прошёл для всех трёх компонентов, отрицательный rollout корректно обнаружен; recovery преждевременно завершил job. PR-приёмка выше остаётся реальной успешной попыткой, но одна попытка не доказывает отсутствие гонки. В `ci-kind.sh` добавлен bounded barrier: observedGeneration должна догнать текущую metadata.generation, Progressing стать True, затем выполняется обычный rollout status. Снимок recovery Deployment сохраняется; проверка не заменена задержкой или игнорированием ошибки.

## Покрытие обязательных требований

| Область | Результат и доказательство |
|---|---|
| Исторические evidence | 22 файла: 20 Original, 2 Sanitized; 21 JSON и 1 TXT. Новых исключений или приватных originals не добавлено; происхождение и причины прежних исключений в [provenance](../provenance.md). Исторические инварианты/JSON/Markdown links проверяет evidence CI. |
| Go, Helm, секреты | test/race/vet/build, 21 Helm profile, actionlint, Gitleaks, ShellCheck и trust-boundary tests прошли в CI; неразобранных находок нет. |
| Контейнеры и kind | Три образа проверены, API Service/version, Ready EndpointSlice, failed rollout и Service failure с восстановлением Passed. |
| GHCR и promotion | Один неизменный digest, source `6f6521d` и promotion `b2b8ccb` сохранены отдельно; Release PR №7 и равные фактические imageID двух окружений — [Passed](gitops-development.md). |
| Argo и recovery | Отдельный kind, две Applications/namespaces, Git rollout, drift/self-heal, bad rollout/recovery development — [Passed](gitops-development.md). Новые render/preflight/status/sync/smoke и повторный install проверены. |
| Безопасные границы | Основной и network-кластеры не изменялись; PostgreSQL/Kafka PVC/topics/offsets не затронуты. Новые receipts не содержат Secret/kubeconfig/credentials. Force push и обход защиты не выполнялись. |

Не проверены fresh bootstrap и настоящее удаление стенда новыми командами (Prepared), отдельный stable fault/recovery, full-stack SQL/Kafka regression после обновления Go, сетевая изоляция namespaces, HA и длительная нагрузка. Эти свойства не выдаются за Passed и не входят в завершённую stateless цепочку. Следующие эксперименты выбираются отдельной задачей, действующий стенд сохранён.
