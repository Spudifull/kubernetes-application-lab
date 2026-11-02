# GHCR и development GitOps

## Изменённые файлы подэтапа

Относительно исходной dev `b34fe8b`:

```text
.github/workflows/container.yml
.github/workflows/publish.yml
.github/workflows/release-gate.yml
CONTRIBUTING.md
README.md
THIRD_PARTY_DEPENDENCIES.md
Taskfile.yml
cmd/labctl/main.go
cmd/labctl/release.go
cmd/labctl/release_test.go
docker/alert-receiver.Dockerfile
docker/api.Dockerfile
docker/client.Dockerfile
docker/kafka-probe.Dockerfile
docker/publisher.Dockerfile
docker/worker.Dockerfile
docs/design/ci-cd-gitops.md
docs/engineering/ci-cd-gitops-lab.md
docs/roadmap.md
docs/verification/README.md
docs/verification/branching-container.md
docs/verification/gitops-development.md
docs/verification/milestone9/dev-after-self-heal.json
docs/verification/milestone9/dev-at-promotion.json
docs/verification/milestone9/dev-drift-controlled.json
docs/verification/milestone9/dev-drift-outofsync.json
docs/verification/milestone9/dev-drift.json
docs/verification/milestone9/dev-failed-rollout.json
docs/verification/milestone9/dev-first-sync.json
docs/verification/milestone9/dev-git-outofsync.json
docs/verification/milestone9/dev-git-rollout-settled.json
docs/verification/milestone9/dev-git-rollout.json
docs/verification/milestone9/dev-recovered.json
docs/verification/milestone9/promotion-comparison.json
docs/verification/milestone9/registry.json
docs/verification/milestone9/release-6f6521d.json
docs/verification/milestone9/stable-at-promotion.json
docs/verification/milestone9/stable-first-sync.json
gitops/argocd/applications.yaml
gitops/argocd/project.yaml
gitops/candidate.json
gitops/dev/values.yaml
gitops/stable/values.yaml
go.mod
go.sum
helm/lab-api/templates/_helpers.tpl
helm/lab-api/templates/deployment.yaml
helm/lab-api/values.yaml
scripts/ci-helm.sh
scripts/ci-kind.sh
scripts/gitops-failed-rollout.ps1
scripts/gitops-observe.ps1
```

Дата: 2 ноября 2026 года. Source image SHA `6f6521d81226931f42edd2783584cb33c8fc8519`. Promotion SHA `b2b8ccb8c9ab7069ca8a6922de09751a47b33ce7`, Release PR №7.

## Публикация без пересборки: Passed

Повторно выполнен publishing [run 41394590401](https://github.com/Spudifull/kubernetes-application-lab/actions/runs/41394590401), job `125116084451`, success. Загружен checked-api из container/kind run `41394590245`, выполнены identity assertions, tag и push. Docker build в publishing job отсутствует. Временный `ENABLE_GHCR_PUBLISH` удалён после подтверждения success, будущие публикации выключены.

Registry reference: `ghcr.io/spudifull/kubernetes-application-lab-api@sha256:b5a4ea8debd055b9f051b7ac449a5d65cca5fda97685fd203df83bd9fb44e677`. Anonymous pull подтверждён. [Registry receipt](milestone9/registry.json) содержит фактические OCI labels и config digest `sha256:448654cff201f8a8ce424f3b45af79e305d034b741a01459c5c5b9643a38a035`, совпадающий с checked CI image.

CI exporter выдал OCI manifest `sha256:026b8e2b198d065e2a470c4987e654f3416b9da82e8038f101b87ab81ee40022`, registry после Docker save/load/push использует Docker schema2 manifest с другим digest. Они не выдаются за один SHA; неизменность config и отсутствие пересборки проверены отдельно. Для development и stable используется именно опубликованный registry digest.

## Первая manual sync: Passed

Создан один отдельный kind-кластер `kubernetes-application-lab-gitops` на Kubernetes 1.34.0, kubectl 1.35.9, с собственным `.cache/gitops/kubeconfig`. Docker имел 30,18 GiB/32 CPU; существующие кластеры и другие контейнеры только проверены read-only. PostgreSQL/Kafka/PVC/topics/offsets основного стенда не менялись.

Argo CD Core 3.5.6 установлен из официального `manifests/core-install.yaml`, SHA-256 `d3d9d3555ad77eb4b6d27b1ec0d2af7978a91537e84d54631f983c227f0a3d31`. Application controller, repo-server, Redis и ApplicationSet controller Ready. В этом режиме нет постоянно доступного Argo UI/API server; наблюдения получены через Kubernetes API. [Core mode](https://argo-cd.readthedocs.io/en/stable/operator-manual/core/).

Namespaces `lab-dev`, `lab-stable` созданы отдельно. AppProject `lab-release` ограничивает source repo, destination namespace и виды ресурсов Deployment/Service; cluster-scoped управление приложением не разрешено. В этом испытании создана только Application lab-dev; stable ещё не развёрнут.

PR №4 внёс digest в dev. Manual sync Git revision `651cb520f19802c5bf60edc9c52ecd688b39b47b` завершилась Synced/Healthy. Deployment Ready; HTTP `/version` из diagnostic Pod через Service DNS вернул source SHA `6f6521d`, VERSION=dev и build date. Фактический Pod imageID в [первом наблюдении](milestone9/dev-first-sync.json) точно равен опубликованному registry reference. Это не сравнение только Helm values.

Сбор наблюдений: `scripts/gitops-observe.ps1 -Environment lab-dev -OutputPath <file> -RequireHealthy`. Скрипт использует только отдельный kubeconfig/context, проверяет имя единственного node, сохраняет выбранные поля без Secret/kubeconfig/HTTP headers, затем проверяет health/readiness/version/imageID. Сырые локальные credentials в публичные evidence не включены.

## Git-driven rollout: Passed

После успешных [CI 41397105518](https://github.com/Spudifull/kubernetes-application-lab/actions/runs/41397105518) и [container/kind 41397105528](https://github.com/Spudifull/kubernetes-application-lab/actions/runs/41397105528) PR №5 слит в dev как `20ea81988b4310c1b8075608c8446102424d7a4f`. Изменены Pod annotation и development progress deadline=30s; основной image digest прежний.

До sync зафиксированы [OutOfSync/Healthy и HTTP 200](milestone9/dev-git-outofsync.json). После manual sync появилась новая Deployment generation и новый Pod UID; [первый снимок rollout](milestone9/dev-git-rollout.json) ещё содержал старый terminating Pod. Сравнение только первого UID списка ошибочно отклонило наблюдение и не использовано как доказательство отсутствия rollout. [Снимок после завершения](milestone9/dev-git-rollout-settled.json) подтвердил новый Pod по множеству UID, Synced/Healthy, HTTP 200, тот же registry imageID и source revision. После ручной приёмки применена Git-конфигурация development auto-sync/selfHeal, prune=false.

## Drift/self-heal: Passed с ограничением наблюдения

Первая [попытка непрерывного наблюдения](milestone9/dev-drift.json) не увидела промежуточный OutOfSync: в samples уже replicas=1/Synced. Она сохранена как незасчитанная, а не переписана в успех.

Во втором опыте auto-sync временно остановлен на время инъекции replicas=2; Argo зафиксировал [OutOfSync/Progressing при HTTP 200](milestone9/dev-drift-outofsync.json). После восстановления policy из Git без ручной sync operation controller автоматически вернул replicas=1 и [Synced/Healthy](milestone9/dev-after-self-heal.json). [Контрольный результат](milestone9/dev-drift-controlled.json): Git before=after=`20ea819`, live replicas 2→1, HTTP samples 200. Этот опыт подтверждает обнаружение расхождения и автоматическое восстановление после включения policy, но не измеряет минимальное время непрерывного self-heal.

## Неудачный rollout и recovery: Passed

`scripts/gitops-failed-rollout.ps1` проверил здоровый baseline и собственный GitOps cluster, временно отключил auto-sync и заменил только lab-dev Deployment image на заведомо отсутствующий digest. Git desired state не менялся. [Наблюдение отказа](milestone9/dev-failed-rollout.json): Deployment Progressing=False/ProgressDeadlineExceeded, новый Pod ErrImagePull, Argo OutOfSync/Degraded. Старая Ready-реплика продолжала отвечать HTTP 200; это не доказательство недоступности всего Service и не ошибочное объявление rollout успешным.

В finally выполнена manual sync прежней Git revision, затем восстановлена automated/selfHeal policy из Git. [Recovery](milestone9/dev-recovered.json): Synced/Healthy, updated/ready replicas=1, HTTP 200, `/version` source `6f6521d`, фактический Pod imageID совпадает с опубликованным digest. Argo history rollback при включённом auto-sync не использовался.

## Release gate и границы

`labctl release-check` проверяет archived recovery/registry receipts, source/version/build date, готовность и фактический Pod imageID, одинаковый digest обоих profiles и неизменность API build inputs от source commit до HEAD. Сравниваются cmd/api, internal, go.mod/go.sum, API Dockerfile и .dockerignore; lab tooling не линкуется в API и не включено в этот diff. Историческая Git revision наблюдения также должна содержать тот же development image.

Workflow `release-gate` дополнительно проверяет trusted push/dev provenance container/kind и publishing runs, config ID checked image artifact и текущий anonymous registry manifest. Он не обращается к локальному кластеру: runtime часть основана на опубликованной проекции фактических наблюдений, а не на новой независимой live-проверке. Gate зависит от сохранности CI artifacts (container evidence хранится 14 дней); при отсутствии artifact он обязан отказать. Первый [release-gate 41400612212](https://github.com/Spudifull/kubernetes-application-lab/actions/runs/41400612212) на PR №7 прошёл, после чего check включён в required main вместе с go/evidence/helm, strict=true, app ID 15368.

## Stable promotion: Passed

[Release PR №7](https://github.com/Spudifull/kubernetes-application-lab/pull/7) слит после успешных Go/evidence/Helm и release-gate. Promotion commit `b2b8ccb8c9ab7069ca8a6922de09751a47b33ce7` отличается от source commit `6f6521d81226931f42edd2783584cb33c8fc8519`. Docker build для main отключён; stable profile содержит тот же registry digest. VERSION остаётся `dev`, как в исходном проверенном артефакте, и не подменяется новой сборкой для stable.

Application lab-stable создана отдельно и вручную синхронизирована с promotion commit. [Первый stable sync](milestone9/stable-first-sync.json): Synced/Healthy, HTTP 200 и source revision в `/version`; Pod imageID — тот же опубликованный digest. Automated sync и prune в stable не включались.

[Development при promotion](milestone9/dev-at-promotion.json), [stable при promotion](milestone9/stable-at-promotion.json) и [сравнение](milestone9/promotion-comparison.json) фиксируют равенство двух реальных Pod imageID, независимые UID namespaces/Deployments/Services/Applications и разные release names. Development отслеживает dev, stable — main. Namespace lab-stable не использовался как новый адрес development Application.

[Зафиксированная identity релиза](milestone9/release-6f6521d.json) отделена от изменяемого текущего `gitops/candidate.json`. Исторические тесты читают identity этого релиза, а не будущего кандидата. Текущий candidate выбирает dev/registry receipts явными относительными путями внутри milestone9; для следующего кандидата нужны новые файлы наблюдений, прежние измерения не перезаписываются. Gate продолжает сверять текущий candidate с выбранным runtime proof, а не с произвольно успешным историческим релизом.

## Границы результата

Сквозной подэтап выполнен для одного stateless API на linux/amd64, одного отдельного kind и двух namespaces. Failed rollout/recovery выполнялись в development; отдельный неисправный выпуск stable не испытывался. Это не HA и не доказательство сетевой tenant isolation. HTTP samples не заменяют длительное нагрузочное испытание. Регрессия действующего SQL/Kafka/observability стека после обновления Go не выполнялась; другие Dockerfiles получили тот же исправленный builder, но отдельная security/runtime CI-приёмка проведена для API. Все исходные SHA и незасчитанные наблюдения сохранены.
