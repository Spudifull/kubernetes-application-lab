# Контракт CI/CD и GitOps

Состояние: Current для лабораторной цепочки stateless API feature → dev → main и одного отдельного GitOps kind с двумя namespaces. [CI](../verification/evidence-restoration-ci.md), [runtime, promotion и ограничения](../verification/gitops-development.md).

Обычные PR получают `contents: read`; jobs чтения собственных CI artifacts дополнительно `actions: read`. Они не используют `pull_request_target`, registry login или credentials действующих кластеров. Быстрые Go/Helm/evidence checks отделены от container/kind и publishing workflows. Actions закрепляются полными SHA; tools — версиями. Ошибка проверки останавливает соответствующий job.

Поставка связывает Git SHA → проверенный image digest → Helm desired state → Pod imageID → `/version`. Публикация GHCR выполняется после проверок с минимальными permissions отдельного job. PR из fork не получает право публикации. Локальные Helm-профили `pullPolicy: Never` сохраняют своё назначение.

Argo CD должен работать в отдельном kind-кластере, управлять только выделенным приложением и не менять основной PostgreSQL/Kafka/PVC. Git-driven rollout, drift/self-heal и неисправный rollout с восстановлением требуют фактических наблюдений. При auto-sync восстановление выполняется через возврат desired state в Git. Подготовленные конфигурации не считаются доказательством этих свойств.

Passed требует сохранённых Git SHA, workflow run ID/URL, digest, версий Kubernetes/Argo CD, наблюдений Service HTTP и ограничений. Для лабораторной stateless-цепочки эти результаты сохранены в verification; остальные свойства из её границ не выводятся.

## Ветки и продвижение

Изменения проходят `feature/*` или `codex/*` → PR в `dev` → Release PR в `main`. `dev` содержит интеграционные кандидаты, `main` — подтверждённую стабильную версию. Required checks обеих веток: `go`, `evidence`, `helm`; для dev после успешного PR №2 добавлены `container`, `kind`. Для main после первого успешного Release PR №7 добавлен `release-gate`. Он проверяет архивированный development runtime proof, trusted source CI/publishing runs, текущую registry identity и неизменность API build inputs. Это не новая live-проверка локального кластера. Подробные ограничения — в [verification](../verification/gitops-development.md).

Один опубликованный image digest сначала проверяется в development, затем переносится в stable без повторной сборки. `source_commit_sha` обозначает исходники сборки; `promotion_commit_sha` — commit, утвердивший выпуск. Gate должен сравнить влияющие на бинарный файл входы сборки (`cmd`, `internal`, `go.mod`, `go.sum`, Dockerfile и build arguments) с источником проверенного артефакта. Совпадение двух строк values не заменяет проверку фактического Pod imageID и `/version`.

## Окружения

Один отдельный GitOps kind-кластер, namespaces `lab-dev` и `lab-stable`. Независимы Deployment, Service, release, values, desired digest и Argo CD Application. Оба окружения развёрнуты на одном проверенном GHCR digest; равенство реальных Pod imageID и независимость UID ресурсов подтверждены. Development следует dev с self-heal после первой manual sync; stable следует main с manual sync. Namespaces создаются явно в выделенном кластере. AppProject разрешает только этот репозиторий, две destination namespace и namespaced Deployment/Service; cluster-scoped ресурсы, PVC и Secret не разрешены. Это логическое разделение workloads, не HA и не проверенная сетевая изоляция.

Первый sync обоих окружений ручной. Development auto-sync/self-heal включается только после первой runtime-проверки. Stable остаётся manual, без автоматического prune. Recovery возвращает известный рабочий Git desired state. Ветка не меняет destination. Успешный release, failed rollout/recovery и drift/self-heal требуют отдельных реальных наблюдений; сейчас они не Passed.

## Контейнерная проверка

`container.yml` отделён от быстрого CI. Изменения только документации не создают kind-кластер. PR получает contents read; job загрузки собственного CI artifact дополнительно actions read, без packages write. Сборка API — Buildx linux/amd64, непривилегированный scratch runtime, OCI revision/version/source/created. Проверяются вызываемые уязвимости Go через govulncheck и HIGH/CRITICAL образа через закреплённый Trivy. Обходы и ignore-unfixed не используются. Один проверенный Docker archive передаётся в kind job и затем публикуется без сборки. CI OCI manifest и registry Docker manifest могут иметь разные digest; их config ID и source identity сверяются отдельно.

Отдельный `publish.yml` запускается только на push в dev, а привилегированный job — только при `ENABLE_GHCR_PUBLISH=true`. Он ждёт успешных быстрых и container/kind runs для точного SHA и события push/dev, проверяет реальные container/kind jobs, загружает проверенный archive и сверяет OCI revision/non-root. Затем выполняет tag/push без Docker build, сохраняет registry digest и source SHA; promotion SHA до выпуска null. Обычные PR не получают packages write. Первый publishing run выполнен и проверен; временный флаг удалён, будущие автоматические публикации выключены. Anonymous pull GHCR package проверяется перед GitOps.

На PR/push main контейнерная сборка отключена: stable использует проверенный dev artifact, а не новый image с SHA merge commit. Быстрый CI обеих веток продолжает работать; готовность Release PR проверяет release-gate. Сам gate не заменяет фактическую stable runtime-приёмку после merge.
