# GHCR и development GitOps

Дата: 2 ноября 2026 года. Source image SHA `6f6521d81226931f42edd2783584cb33c8fc8519`. Stable promotion пока не разрешён и не выполнен.

## Публикация без пересборки: Passed

После отдельного разрешения владельца повторно выполнен publishing [run 41394590401](https://github.com/Spudifull/kubernetes-application-lab/actions/runs/41394590401), job `125116084451`, success. Загружен checked-api из container/kind run `41394590245`, выполнены identity assertions, tag и push. Docker build в publishing job отсутствует. Временный `ENABLE_GHCR_PUBLISH` удалён после подтверждения success, будущие публикации выключены.

Registry reference: `ghcr.io/spudifull/kubernetes-application-lab-api@sha256:b5a4ea8debd055b9f051b7ac449a5d65cca5fda97685fd203df83bd9fb44e677`. Anonymous pull подтверждён. [Registry receipt](milestone9/registry.json) содержит фактические OCI labels и config digest `sha256:448654cff201f8a8ce424f3b45af79e305d034b741a01459c5c5b9643a38a035`, совпадающий с checked CI image.

CI exporter выдал OCI manifest `sha256:026b8e2b198d065e2a470c4987e654f3416b9da82e8038f101b87ab81ee40022`, registry после Docker save/load/push использует Docker schema2 manifest с другим digest. Они не выдаются за один SHA; неизменность config и отсутствие пересборки проверены отдельно. Для development и stable используется именно опубликованный registry digest.

## Первая manual sync: Passed

Создан один отдельный kind-кластер `kubernetes-application-lab-gitops` на Kubernetes 1.34.0, kubectl 1.35.9, с собственным `.cache/gitops/kubeconfig`. Docker имел 30,18 GiB/32 CPU; существующие кластеры и другие контейнеры только проверены read-only. PostgreSQL/Kafka/PVC/topics/offsets основного стенда не менялись.

Argo CD Core 3.5.6 установлен из официального `manifests/core-install.yaml`, SHA-256 `d3d9d3555ad77eb4b6d27b1ec0d2af7978a91537e84d54631f983c227f0a3d31`. Application controller, repo-server, Redis и ApplicationSet controller Ready. В этом режиме нет постоянно доступного Argo UI/API server; наблюдения получены через Kubernetes API. [Core mode](https://argo-cd.readthedocs.io/en/stable/operator-manual/core/).

Namespaces `lab-dev`, `lab-stable` созданы отдельно. AppProject `lab-release` ограничивает source repo, destination namespace и виды ресурсов Deployment/Service; cluster-scoped управление приложением не разрешено. В этом испытании создана только Application lab-dev; stable ещё не развёрнут.

PR №4 внёс digest в dev. Manual sync Git revision `651cb520f19802c5bf60edc9c52ecd688b39b47b` завершилась Synced/Healthy. Deployment Ready; HTTP `/version` из diagnostic Pod через Service DNS вернул source SHA `6f6521d`, VERSION=dev и build date. Фактический Pod imageID в [первом наблюдении](milestone9/dev-first-sync.json) точно равен опубликованному registry reference. Это не сравнение только Helm values.

Сбор наблюдений: `scripts/gitops-observe.ps1 -Environment lab-dev -OutputPath <file> -RequireHealthy`. Скрипт использует только отдельный kubeconfig/context, проверяет имя единственного node, сохраняет выбранные поля без Secret/kubeconfig/HTTP headers, затем проверяет health/readiness/version/imageID. Сырые локальные credentials в публичные evidence не включены.

## Следующие проверки

Git-driven rollout, auto-sync/self-heal, controlled failed rollout/recovery и stable promotion требуют отдельных наблюдений. До их выполнения статус Prepared. Добавляемые development annotation и короткий progress deadline служат контролируемым изменением в изолированном окружении. Ни одна из этих будущих проверок не объявляется Passed первой синхронизацией.
