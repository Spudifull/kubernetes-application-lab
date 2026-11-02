# Ветки, контейнерная проверка и подготовка окружений

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

`container.yml`: path classification, govulncheck, Buildx API, проверка non-root/OCI revision, Trivy HIGH/CRITICAL с exit-code=1, передача Docker archive отдельному kind job. Публикации GHCR и registry secrets нет. Отчёты и build metadata сохраняются как CI artifacts. На PR проверяется merge revision из `github.sha`, а не произвольно объявленная source revision.

`ci-kind.sh`: исключительно собственный временный кластер `lab-ci-*`, отдельный kubeconfig; Helm stateless API; client Pod → Service DNS → readyz/version; намеренно неверный Service selector; обнаружение HTTP-отказа; восстановление selector и HTTP. Диагностика до cleanup собственного кластера. Время отказа ограничено; отрицательный сценарий не меняет чужие ресурсы. Полный Kubernetes/PostgreSQL/Kafka стек не нужен.

## Prepared и ещё не проверенные свойства

Первый Container/kind run Passed в указанном выше объёме; последующие изменения требуют своего run. Helm digest support проверяется отдельно от локальных tag/Never profiles. GitOps values и Applications подготовлены с пустыми digest/tag: они намеренно не допускают deployment без реального артефакта. AppProject ограничивает source/destination и виды ресурсов.

GHCR digest отсутствует. Argo CD не установлен; его версия и runtime статусы не зафиксированы. Development/stable deployment, Pod imageID, Release PR, source/promotion SHA, продвижение без пересборки, drift/self-heal и failed rollout/recovery не Passed. Release gate остаётся Target и не включён в защиту. Основной и сетевой кластеры только перечислены read-only, их ресурсы/PVC/topics/offsets не менялись.

Milestone 9 и дополнение не Done. Публикация образа и продвижение в main выполняются только в рамках отдельного разрешения владельца после появления конкретного проверенного кандидата.
