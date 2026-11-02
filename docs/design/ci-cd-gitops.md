# Контракт CI/CD и GitOps

Состояние: Target для полной цепочки поставки. Быстрый CI прошёл в GitHub Actions; [проверки и ограничения](../verification/evidence-restoration-ci.md).

Обычные PR получают только `contents: read`. Workflow не использует `pull_request_target`, secrets, registry login или доступ к Kubernetes. Быстрые Go/Helm/evidence checks отделены от будущих container/kind и publishing workflows. Actions закрепляются полными SHA; tools — версиями. Ошибка проверки должна останавливать соответствующий job.

Целевая поставка связывает Git SHA → проверенный image digest → Helm desired state → Pod imageID → `/version`. Публикация GHCR допускается только после проверок и с минимальными permissions отдельного job. PR из fork не получает право публикации. Локальные Helm-профили `pullPolicy: Never` сохраняют своё назначение.

Argo CD должен работать в отдельном kind-кластере, управлять только выделенным приложением и не менять основной PostgreSQL/Kafka/PVC. Git-driven rollout, drift/self-heal и неисправный rollout с восстановлением требуют фактических наблюдений. При auto-sync восстановление выполняется через возврат desired state в Git. Подготовленные конфигурации не считаются доказательством этих свойств.

Passed требует сохранённых Git SHA, workflow run ID/URL, digest, версий Kubernetes/Argo CD, наблюдений Service HTTP и ограничений. Эти результаты добавляются по выполнении; текущий статус Milestone 9 — незавершён.
