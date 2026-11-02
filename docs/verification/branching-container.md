# Ветки, контейнерная проверка и подготовка окружений

## Изменённые файлы подэтапа

```text
.github/workflows/container.yml
.github/workflows/publish.yml
CONTRIBUTING.md
README.md
THIRD_PARTY_DEPENDENCIES.md
Taskfile.yml
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
gitops/argocd/applications.yaml
gitops/argocd/project.yaml
gitops/dev/values.yaml
gitops/stable/values.yaml
go.mod
go.sum
helm/lab-api/templates/_helpers.tpl
helm/lab-api/templates/deployment.yaml
helm/lab-api/values.yaml
scripts/ci-helm.sh
scripts/ci-kind.sh
```

Дата: 2 ноября 2026 года. Исходный `dev`: `b34fe8b19da5cfa67e0b16745586a3b2d18f1568`; `main`: `562e23c2ddfee56a28c112a9bb465b7259d3db62`. Работа начата в `codex/release-promotion` от актуальной `origin/dev`, исходное рабочее дерево чистое.

## Проверенные исходные условия

Репозиторий Public. Открытых PR при начале работы нет. PR №1 уже closed/merged, merge SHA соответствует dev. [PR CI](https://github.com/Spudifull/kubernetes-application-lab/actions/runs/41390729252) и [push CI](https://github.com/Spudifull/kubernetes-application-lab/actions/runs/41390839339) успешны. Повторный PR с прежним изменением не создавался.

API GET protection обеих веток подтвердил: обязательный PR; checks `go`, `evidence`, `helm`, app ID 15368; strict=true; enforce_admins=true; force push=false; deletion=false. Это проверка конкретных настроек, а не только флага protected. Container/kind и release gate пока не required.

## Локальные результаты

Buildx Docker driver, Docker Engine 29.8.2, linux/amd64. Первый build API на прежнем Go 1.26.0 прошёл. Govulncheck 1.1.4 обнаружил 22 вызываемые уязвимости на локальной Go 1.26.5. Выбран исправленный Go 1.26.10, официальный builder image закреплён digest `sha256:34414731924cf2263160f0021f5cf6e1807382d85248f7cb419d4187b96d5e43`; Dockerfiles всех entrypoints согласованы с go.mod.

Обновлены x/net до v0.60.0 и gRPC: сначала v1.82.1, после Trivy HIGH-находок CVE-2026-84304 и CVE-2026-84445 — v1.83.2. x/sys обновлён до v0.48.0 как требование графа зависимостей. Исключения уязвимостей не добавлялись. Первоначальные отказы безопасности сохранены как часть результата, а не Passed.

Финальные локальные Go test/race/vet/build, govulncheck и actionlint прошли на Go 1.26.10. Trivy v0.76.1 (закреплён digest scanner image) завершился с exit 0, HIGH/CRITICAL не обнаружены. Buildx API manifest digest `sha256:7762620c6750993d5b353ce39fce79cd3a87ba20a09116ac7b9b13c939a07b86`; build arguments VERSION=dev, REVISION=b34fe8b-dirty, BUILD_DATE=2026-11-02T00:00:00Z. Это локальный образ рабочего дерева, не GHCR digest и не разрешённый релизный артефакт. Машиночитаемые локальные отчёты находятся в игнорируемом `.cache/container/`; CI сохраняет независимые artifacts для своей точной ревизии.

## Подготовленные проверки

### Первая приёмка PR №2

[Быстрый CI 41393459228](https://github.com/Spudifull/kubernetes-application-lab/actions/runs/41393459228) и [Container/kind 41393459288](https://github.com/Spudifull/kubernetes-application-lab/actions/runs/41393459288) завершились успешно на PR head `17a36d5d3731f35992e9a81299983f0ec3fb0c69`, проверенном merge SHA `a47764d5b08f8d78bb897fbde0375b21099216c6`. Image manifest `sha256:197bf4784439491a4f9046013e619fc5193aaa2874effc102572b64ef18b3a8e`, config ID `sha256:f7cb610beb1b01235115f49df3ffa8aa9685cb65c95c3e6f48df8c9a6ce87329`. Build arguments REVISION=merge SHA, VERSION=dev, BUILD_DATE=2026-11-02T08:20:33+03:00. Это проверенный CI archive, ещё не GHCR artifact.

Container job `125109654155` выполнил dependency scan, Buildx, identity assertion и Trivy; kind job `125110207832` загрузил именно checked-api archive, подтвердил Service readiness/version, обнаружил ошибку selector и восстановил Service. Кластер `lab-ci-41393459288-1` удалён собственным cleanup. Отдельные artifacts `container-evidence`, `kind-evidence`, `checked-api` доступны в run с ограниченным сроком хранения; это не бессрочный публичный архив.

Первый kind run использовал Kubernetes 1.34, но обнаружено предупреждение о встроенном kubectl 1.37 на runner. После этого installer явно закреплён на kubectl 1.35.9 с checksum verification; исправленная версия требует повторного run. Подготовлен выключенный `publish.yml`, ожидающий отдельного разрешения владельца. GHCR публикация ещё не выполнялась.

Повторный быстрый CI `41393938941` на head `29b9d17` выявил ShellCheck SC2034 в выключенном publishing workflow: счётчик ожидания объявлялся, но не использовался. Исправлено добавлением диагностического номера попытки; ShellCheck не отключался. Дополнительно запрещена Docker-пересборка на PR/push main: stable promotion должен использовать ранее проверенный dev artifact. Изменения проходят новый run до слияния.

`container.yml`: path classification, govulncheck, Buildx API, проверка non-root/OCI revision, Trivy HIGH/CRITICAL с exit-code=1, передача Docker archive отдельному kind job. Публикации GHCR и registry secrets нет. Отчёты и build metadata сохраняются как CI artifacts. На PR проверяется merge revision из `github.sha`, а не произвольно объявленная source revision.

`ci-kind.sh`: исключительно собственный временный кластер `lab-ci-*`, отдельный kubeconfig; Helm stateless API; client Pod → Service DNS → readyz/version; намеренно неверный Service selector; обнаружение HTTP-отказа; восстановление selector и HTTP. Диагностика до cleanup собственного кластера. Время отказа ограничено; отрицательный сценарий не меняет чужие ресурсы. Полный Kubernetes/PostgreSQL/Kafka стек не нужен.

## Приёмка последней версии и интеграция в dev

[PR №2](https://github.com/Spudifull/kubernetes-application-lab/pull/2) слит после успешных [CI 41394140792](https://github.com/Spudifull/kubernetes-application-lab/actions/runs/41394140792) и [Container/kind 41394140833](https://github.com/Spudifull/kubernetes-application-lab/actions/runs/41394140833) на head `327ccccce293181758937dbc1c21d333191c39b4`.

Merge в dev: `6f6521d81226931f42edd2783584cb33c8fc8519`. На нём успешны [push CI 41394590333](https://github.com/Spudifull/kubernetes-application-lab/actions/runs/41394590333) и [Container/kind 41394590245](https://github.com/Spudifull/kubernetes-application-lab/actions/runs/41394590245). Container job `125113106774`, kind job `125113711993`; API image manifest `sha256:026b8e2b198d065e2a470c4987e654f3416b9da82e8038f101b87ab81ee40022`, config ID `sha256:448654cff201f8a8ce424f3b45af79e305d034b741a01459c5c5b9643a38a035`. Build arguments: REVISION=dev merge SHA, VERSION=dev, BUILD_DATE=2026-11-02T08:32:08+03:00. Dockerfile `docker/api.Dockerfile`, Go 1.26.10, Helm 3.17.3, kind 0.33.0, Kubernetes 1.34.0, kubectl 1.35.9. OCI source/revision/version/created и non-root проверены job.

Service smoke, отрицательный selector и восстановление прошли; кластер `lab-ci-41394590245-1` удалён. В artifacts сохранены version, готовые Pod с imageID, ресурсы, события, отрицательная проверка, build metadata и security reports. Это ещё не проверка GHCR digest в Argo CD или stable.

После успешного PR добавлены required `container`, `kind` только для dev. Main осталась на `562e23c`, с базовыми checks. Release PR не создавался. Publishing [41394590401](https://github.com/Spudifull/kubernetes-application-lab/actions/runs/41394590401) skipped: переменная `ENABLE_GHCR_PUBLISH` отсутствует. Публикация и дальнейший promotion ожидают отдельного разрешения владельца согласно разделу 12 задания; подготовленный workflow допускает повторный запуск для этого же dev SHA без пересборки.

## Prepared и ещё не проверенные свойства

Первый Container/kind run Passed в указанном выше объёме; последующие изменения требуют своего run. Helm digest support проверяется отдельно от локальных tag/Never profiles. GitOps values и Applications подготовлены с пустыми digest/tag: они намеренно не допускают deployment без реального артефакта. AppProject ограничивает source/destination и виды ресурсов.

GHCR digest отсутствует. Argo CD не установлен; его версия и runtime статусы не зафиксированы. Development/stable deployment, Pod imageID, Release PR, source/promotion SHA, продвижение без пересборки, drift/self-heal и failed rollout/recovery не Passed. Release gate остаётся Target и не включён в защиту. Основной и сетевой кластеры только перечислены read-only, их ресурсы/PVC/topics/offsets не менялись.

Milestone 9 и дополнение не Done. Публикация образа и продвижение в main выполняются только в рамках отдельного разрешения владельца после появления конкретного проверенного кандидата.
