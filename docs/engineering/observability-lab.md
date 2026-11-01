# Наблюдаемость: первая инфраструктурная итерация

Контракт — [архитектура](../design/observability-architecture.md). Инфраструктурный сценарий [Passed](../verification/prometheus-scraping.md), с ограничениями, указанными в отчёте. Использовать основной kind.

## Подготовка

Нужны Go, Task, Helm, Docker и подходящий kubectl. Если новый PowerShell выбирает kubectl 1.29, добавить существующий `.cache/kubectl/v1.35.9` в начало PATH текущего процесса. Допустимый skew с server 1.34 — одна minor-версия.

Выполнить `task observability-preflight`. Сохраняются SHA/dirty, Helm releases, версии, namespace/CRD, allocatable, requests/limits, Docker/kubelet/host memory, StorageClass, Pod images/imageIDs, приложение, Kafka, CronJob и KEDA. Изучить напечатанный каталог: pressure, доступную память, свободное место, готовность приложения, причины перезапусков Strimzi. Отсутствие Metrics API не означает нулевое потребление. Ресурсы действующих workload не уменьшать.

Владельцу выполнить `go test ./cmd/labctl`, `go test -race ./cmd/labctl`, `go vet ./cmd/labctl`. Затем `task observability-helm-lint`, `task observability-render`. Просмотреть оба YAML: образы, ClusterIP, ресурсы, securityContext, RBAC, PVC и селекторы. Пароль Grafana в render не попадает.

## Установка и сигналы

После проверок выполнить `task observability-install`. Проверяются владельцы и ресурсы, создаются namespace платформы, Secret Grafana и два monitoring releases с CRD/RBAC/webhooks. Releases приложения и его PVC не обновляются. При ошибке сохранить каталог, выполнить `task observability-status`, разобрать причину; PVC не удалять.

Operator создаёт StatefulSet асинхронно. Helm deployed не заменяет готовность Prometheus/Alertmanager. После Ready Pod выполнить:

1. `task observability-status` — imageIDs, CR, Service/PVC, events, Helm revisions, readiness API Prometheus/Alertmanager.
2. `task prometheus-targets` — настоящий discovery; требуется API target и UP всех выбранных targets.
3. `task prometheus-query QUERY=build` — настоящий `lab_build_info` API.
4. `task prometheus-query QUERY=api-recording-rule` — вычисленное правило, после scrape/evaluation interval.
5. `task prometheus-query QUERY=worker-ready` — доступные реплики Deployment из kube-state-metrics, ещё не метрики самого worker.
6. `task grafana-status` — API Grafana сообщает database ok.

Пустые данные возвращают ошибку. Default dashboards и правила для loopback endpoints kind отключены; etcd/scheduler/controller-manager/kube-proxy не объявляются собранными. Kubelet, API server, kube-state-metrics и внутренние targets требуют фактического UP. Затем перепроверить основной API, Kafka/SQL и отсутствие новых отказов.

## UI и восстановление

В отдельных терминалах: `task prometheus-forward`, `task grafana-forward`, `task alertmanager-forward`. Loopback-порты: 19090, 13000, 19093. Через 12 минут команду нужно запустить снова. Пользователь Grafana `lab-admin`; пароль только в Secret `lab-grafana-admin` namespace платформы. Получать локально через kubectl, не включать в присылаемые журналы или Git.

При Pending/OOM изучить events, статусы и память. При пустых targets проверить CRD, monitor namespace/labels, Service labels/port, generated configuration. При up=0 изучить lastError и сетевой путь; исчезновение ряда рассматривать отдельно. Команды удаления всего stack/PVC нет.

Вход в Grafana и запрос `lab_build_info` в Explore через Prometheus подтверждены владельцем. После повышения памяти Grafana наблюдение 15 минут не выявило новых перезапусков; это ограниченный опыт, не гарантия отсутствия утечки. Receiver local-null не доказывает Routed/received/Resolved.

## Увеличение запаса памяти Grafana

После прикладной панели измерены 232–497Mi при лимите 512Mi за 15 минут. С двумя панелями профиль 512Mi/768Mi показал 628–712Mi за 10 минут без перезапусков, оставив только 56Mi до лимита. Профиль request=768Mi и limit=1Gi применён на dd444e40f65f, 31 снимок за 15 минут показал 225–762Mi без перезапусков (см. отчёт метрик зависимостей); CPU и бюджеты других workload не меняются. [Бюджет и обоснование](../design/observability-architecture.md#начальный-бюджет) не являются доказательством применения. Прежние результаты Passed сохраняют границы прежних профилей.

Владельцу выполнить `go test -count=1 -race ./cmd/labctl`, `go vet ./cmd/labctl`, `task observability-helm-lint`, затем создать commit; после него сохранить `task observability-render` и `task observability-preflight` из чистого дерева. В render проверить Grafana resources 768Mi/1Gi, прежние версии, Recreate, PVC и отсутствие изменений ресурсов приложения. Preflight должен проверить фактическую ёмкость и давление; сообщение «машина позволяет» не заменяет свежие измерения.

Сохранить `task observability-status` и прежнюю deployed Helm revision lab-monitoring до обновления. Затем выполнить `task observability-install`: это обновит оба monitoring release, Grafana будет кратковременно недоступна при Recreate; приложения пересобирать не нужно. Остановить прежний grafana-forward и после готовности нового Pod запустить его снова. Проверить фактические requests/limits Deployment, панель с прежним UID, `task grafana-status`, `task application-metrics-verify`, `task smoke`. Повторить 15-минутное наблюдение памяти и RESTARTS с открытой панелью; считать максимум только максимумом выборки. Рост к новому пределу требует диагностики, не очередного автоматического повышения лимита.

Если обновление не прошло, сохранить events/журналы и каталог доказательств. Возврат — явный Helm rollback только lab-monitoring к сохранённой прежней deployed revision с теми же context/namespace, не удаление release/PVC/Secret. Старый профиль имеет известный малый запас памяти, поэтому rollback не считается устранением причины. Автоматический rollback и изменение зависимостей не выполняются.

## Отказ scrape без остановки API

Владельцу выполнить профильные Go-проверки и `task observability-helm-lint` (включает отрицательные values), затем `task observability-scrape-render`. В сохранённом YAML единственное намеренное изменение ServiceMonitor API — scheme=https вместо http, порт/путь/селекторы сохраняются. Не устанавливать второй monitoring stack. Для канонического опыта сначала создать commit и начать с чистого дерева; образы приложения не пересобирать.

1. Сохранить `task observability-status`, `task prometheus-targets`, `task prometheus-query QUERY=api-up`, `task prometheus-query QUERY=build` и успешный `task smoke`. Исходное api-up=1.
2. Выполнить `task observability-scrape-fail`. Команда проверяет установленный release, сохраняет Git provenance и values, выполняет render/server dry-run и обновляет только lab-observability. Другие значения release сохраняются. Не запускать observability-install во время опыта: обычная установка вернёт штатный профиль раньше фиксации результата.
3. После reconciliation повторить targets/api-up. Обычно требуется несколько scrape-интервалов (30s), но фиксированное ожидание не является доказательством. Требуется API target DOWN с https и ошибкой TLS/HTTP, api-up=0. `prometheus-targets` при этом ожидаемо завершается ошибкой; остальные targets должны оставаться UP. Пустой ряд не равен нулю и не закрывает этот сценарий.
4. Во время DOWN выполнить `task smoke`: HTTP API должен работать. Старый build info на графике за час не доказывает свежий сбор. Сохранить вывод до восстановления.
5. Обязательно выполнить `task observability-scrape-restore`, в том числе после прерывания опыта. После reconciliation повторить targets/api-up/build/api-recording-rule и smoke: http, UP, 1, свежие samples и успешный запрос API. Финальный observability-status сохраняет Helm revisions и состояние приложения. Не удалять releases/PVC для восстановления.

Если target не появляется или все endpoints DOWN, сохранить диагностику и восстановить профиль; не выдавать другой отказ за ожидаемый. При длительности более двух минут может активироваться LabAPIMetricsUnavailable, но доставка уведомлений не является предметом этого опыта. После успешного восстановления можно перейти к метрикам publisher/worker/outbox и зависимостей, затем Collector/Tempo, Loki/Alloy, панелям и отказам. Milestone 8 остаётся незавершённым.

## Прикладные метрики и первая панель

Следующая подготовленная итерация — [метрики Kafka/PostgreSQL](dependency-metrics-lab.md): отдельный release exporters и явное включение JMX; не запускать её установку как обычную повторную проверку прикладных counters.

Это первая часть итерации 2: прикладные сигналы B/C и одна панель, без Kafka/PostgreSQL server exporter. Статус [Passed в границах отчёта](../verification/application-metrics.md). Для воспроизведения сначала владелец выполняет `go test -count=1 ./...`, `go test -count=1 -race ./...`, `go vet ./...`, затем observability/jobs/worker Helm lint и observability-render. Render включает dashboard JSON как native values и два PodMonitor с точным namespace/name/instance/health port. Новые SQL-проверки OutboxStats входят в существующий opt-in `jobs-sql-integration-test` на отдельной пустой БД `lab_jobs_test_*`; основная БД для теста запрещена.

После commit чистого дерева сохранить preflight, собрать `task image-build`, `task publisher-build`, `task worker-build`. Новых миграций нет. Развернуть `task jobs-api-deploy`, `task publisher-deploy`, `task worker-deploy`: это штатный jobs-профиль, одна worker-реплика без активного ScaledObject/HPA. Если scaler активен, сначала разобрать его владение по существующему сценарию KEDA, не конкурировать с ним. Сохранить API/publisher/worker images/revisions и выполнить smoke. Образы старых процессов не имеют новых метрик: сначала обновить приложение, затем `task observability-install` обновляет monitoring и локальный chart 0.3.0. При первоначальном discovery дождаться фактического сбора, а не просто Helm deployed.

`task application-metrics-verify` сохраняет targets, требует publisher/worker UP и свежий успешный outbox sample, затем получает process counters и outbox gauges. Предварительно неизвестные NaN/отсутствующие ряды не считаются здоровыми нулями. Новые ключи `task prometheus-query QUERY=...`:

| QUERY | Назначение |
|---|---|
| build-all | данные сборки API/publisher/worker; различать job/pod и сверить чистую ревизию новых образов |
| publisher / publisher-errors | durable outbox marks / ошибки Kafka delivery |
| worker / worker-errors | новые SQL effects / неуспешные попытки |
| worker-duplicates / worker-retries / worker-dlq | обнаруженные дубликаты / сохранённые повторы / новые durable DLQ receipts |
| outbox / outbox-age | последнее измеренное число pending / возраст самого старого события |
| outbox-health / outbox-freshness | успешность последней SQL-выборки / секунд с последней успешной выборки |
| application-metrics-health | отфильтрованный ряд со значением 1 только при исправных publisher/worker targets и свежем успешном outbox sample |
| api-p95 | оценка p95 по histogram за 5 минут, без probes/metrics/version; до трафика возможен NaN |

Обычная `prometheus-query` требует непустой ответ, но raw outbox не проверяет его свежесть вместо оператора. Для приёмки использовать application-metrics-verify и сравнение before/after, не просто наличие нулевого counter.

Запустить API port-forward на 18081 по прежней инструкции worker. Сохранить исходные counters, выполнить `task worker-batch`, затем получить counters/outbox и `task worker-group-status`; SQL-статус сверяется существующим `task outbox-status`. Сохранить 12 job ID и убедиться в одном эффекте у каждого. Для replay, retry и DLQ использовать существующие управляемые процедуры с явным job ID, не сбрасывать offsets. После publisher-unreachable-start создать новое задание, наблюдать ошибки/возраст/pending и обязательно выполнить publisher-unreachable-restore, затем дождаться результата того же задания и исходного backlog. Смена Pod обнуляет process counters; сравнивать серии с учётом Pod и reset.

Через `task grafana-forward` открыть `/d/lab-application-metrics`: панель в папке Kubernetes Application Lab, datasource Prometheus. До трафика пустой p95 не является ошибкой графика. Сохранить PromQL и samples; затем отдельной явной заменой Grafana Pod подтвердить повторное появление панели из файлов. Прежние ручные настройки временной БД могут потеряться, поэтому сначала сохранить их при необходимости. Повторить наблюдение памяти Grafana под новой панелью: прежний Passed на 512Mi относится к более простой нагрузке. Ресурсы других workload не уменьшать. Сохранить итоговый status/health/smoke и диагностику; не удалять PVC/Secret/topics/DLQ receipts. Метрики зависимостей, дополнительные панели, tracing и alerts затем проверены отдельными опытами; [итоговая приёмка](../verification/grafana-dashboards.md#итоговая-приёмка-milestone-8) фиксирует завершение Milestone 8 в лабораторных границах.
