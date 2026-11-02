# Контракт CI/CD и GitOps

Состояние: Target для полной цепочки поставки. Быстрый CI прошёл в GitHub Actions; [проверки и ограничения](../verification/evidence-restoration-ci.md).

Обычные PR получают только `contents: read`. Workflow не использует `pull_request_target`, secrets, registry login или доступ к Kubernetes. Быстрые Go/Helm/evidence checks отделены от будущих container/kind и publishing workflows. Actions закрепляются полными SHA; tools — версиями. Ошибка проверки должна останавливать соответствующий job.

Целевая поставка связывает Git SHA → проверенный image digest → Helm desired state → Pod imageID → `/version`. Публикация GHCR допускается только после проверок и с минимальными permissions отдельного job. PR из fork не получает право публикации. Локальные Helm-профили `pullPolicy: Never` сохраняют своё назначение.

Argo CD должен работать в отдельном kind-кластере, управлять только выделенным приложением и не менять основной PostgreSQL/Kafka/PVC. Git-driven rollout, drift/self-heal и неисправный rollout с восстановлением требуют фактических наблюдений. При auto-sync восстановление выполняется через возврат desired state в Git. Подготовленные конфигурации не считаются доказательством этих свойств.

Passed требует сохранённых Git SHA, workflow run ID/URL, digest, версий Kubernetes/Argo CD, наблюдений Service HTTP и ограничений. Эти результаты добавляются по выполнении; текущий статус Milestone 9 — незавершён.

## Ветки и продвижение

Изменения проходят `feature/*` или `codex/*` → PR в `dev` → Release PR в `main`. `dev` содержит интеграционные кандидаты, `main` — подтверждённую стабильную версию. Required checks обеих веток сейчас `go`, `evidence`, `helm`; container/kind становятся обязательными только после успешного выполнения на соответствующих PR. Release gate остаётся Target до фактической реализации и успешной проверки.

Один опубликованный image digest сначала проверяется в development, затем переносится в stable без повторной сборки. `source_commit_sha` обозначает исходники сборки; `promotion_commit_sha` — commit, утвердивший выпуск. Gate должен сравнить влияющие на бинарный файл входы сборки (`cmd`, `internal`, `go.mod`, `go.sum`, Dockerfile и build arguments) с источником проверенного артефакта. Совпадение двух строк values не заменяет проверку фактического Pod imageID и `/version`.

## Окружения

Один отдельный GitOps kind-кластер, namespaces `lab-dev` и `lab-stable`. Независимы Deployment, Service, release, values, digest и Argo CD Application. Подготовлены profiles и Applications в `gitops/`; пустые digest/tag намеренно запрещают развёртывание до выбора реального артефакта. Namespaces создаются явно в выделенном кластере. AppProject разрешает только этот репозиторий, две destination namespace и namespaced Deployment/Service; cluster-scoped ресурсы, PVC и Secret не разрешены.

Первый sync обоих окружений ручной. Development auto-sync/self-heal включается только после первой runtime-проверки. Stable остаётся manual, без автоматического prune. Recovery возвращает известный рабочий Git desired state. Ветка не меняет destination. Успешный release, failed rollout/recovery и drift/self-heal требуют отдельных реальных наблюдений; сейчас они не Passed.

## Контейнерная проверка

Подготовленный `container.yml` отделён от быстрого CI. Изменения только документации не создают kind-кластер. PR получает contents read; job загрузки собственного CI artifact дополнительно actions read, без packages write. Сборка API — Buildx linux/amd64, непривилегированный scratch runtime, OCI revision/version/source/created. Проверяются вызываемые уязвимости Go через govulncheck и HIGH/CRITICAL образа через закреплённый Trivy. Обходы и ignore-unfixed не используются. Один проверенный Docker archive передаётся в kind job; этот archive ещё не является опубликованным GHCR digest.

Отдельный `publish.yml` запускается только на push в dev, а привилегированный job — только при `ENABLE_GHCR_PUBLISH=true`, устанавливаемом после разрешения владельца. Он ждёт успешных быстрых и container/kind runs для точного SHA и события push/dev, проверяет реальные container/kind jobs, загружает проверенный archive и сверяет OCI revision/non-root. Затем выполняет tag/push без Docker build, сохраняет registry digest и source SHA; promotion SHA ещё null. Обычные PR не получают packages write. Пока переменная не включена, этот путь Prepared, registry digest отсутствует. Публичная доступность GHCR package должна проверяться отдельно перед anonymous pull из GitOps.

На PR/push main контейнерная сборка отключена: stable должен использовать проверенный dev artifact, а не новый image с SHA merge commit. Быстрый CI обеих веток продолжает работать. До реализации release gate это не подтверждает готовность main к promotion; слияние Release PR требует отдельной runtime-приёмки.
