# Наблюдаемость приложения и зависимостей

## Состояние

**Current:** API сохраняет текстовый `/metrics` и структурированные журналы. На восстановленной машине подтверждены готовность Prometheus/Alertmanager/Grafana, 14 UP targets, samples API и вычисление recording rule. После OOM Grafana при 256Mi установлен лимит 512Mi: повторное наблюдение UI в течение 15 минут прошло без перезапусков, datasource Prometheus вернул lab_build_info=1. Обратимый отказ scrape подтвердил up=1 → 0 → 1 при работающем HTTP API. [Отчёт](../verification/prometheus-scraping.md) имеет `Passed` только для инфраструктурной итерации и сохраняет историю прежних отказов; весь Milestone 8 не завершён.

**Current:** HTTP histogram API, /metrics publisher/worker, периодические SQL-снимки outbox, PodMonitor и первая панель Grafana проверены на новой нагрузке, повторной доставке и отказе publisher с восстановлением. Панель повторно загрузилась после замены Grafana Pod; [отчёт Passed](../verification/application-metrics.md) ограничивает доказательство памяти 15 минутами без перезапуска, с максимумом 497Mi при лимите 512Mi. Запас для дальнейшего расширения не доказан.

**Current:** [SDK → Collector → Tempo](application-tracing.md) проверены на обычном задании, замене publisher Pod, retries/DLQ, недоступном telemetry endpoint с восстановлением и выключенном SDK. Фактические parent ID, SQL/offset порядок, Grafana, 15 минут ресурсов лёгкого режима и финальные targets/smoke отражены в [отчёте](../verification/application-tracing.md).

**Current, журналы:** [Loki/Alloy](application-logging.md) установлены и имеют [Passed в ограниченном объёме](../verification/application-logging.md): два новых задания, поиск событий трёх процессов, повторы, переход Loki → Tempo, 15 минут ресурсов без перезапусков и финальные 23/23 UP targets. Первоначальная краткая недоступность Grafana scrape сохранена в отчёте.

**Current, оповещения:** [правила и внутренний receiver](alerting.md) установлены; [первый опыт outbox](../verification/alerting.md) подтвердил Pending/Firing/Received/Resolved, один SQL-эффект и восстановление lag=0.

**Target:** runtime-проверки остальных правил, совместная диагностика оставшихся отказов метриками/Loki/Tempo и итоговая сквозная проверка. До завершения действуют проверенные сбор telemetry и цикл одного alert; разрыв закрывается отдельными результатами остальных сценариев, а не наличием правил в конфигурации.

**Current, часть D:** отдельный release exporters, JMX, выделенные удостоверения и панель зависимостей имеют [Passed в ограниченном объёме](../verification/dependency-metrics.md): samples, цикл lag, SQL/Kafka права, сохранность PVC и измеренные ресурсы. Профиль Grafana 768Mi/1Gi применён и наблюдался 15 минут без перезапусков; длительная устойчивость не заявляется.

## Владение и версии

Основной контекст — `kind-kubernetes-application-lab`, Kubernetes 1.34. Приложение сохраняет свой namespace. Платформа получает `kubernetes-application-lab-observability`, PSA `restricted` версии 1.34.

Release `lab-monitoring` использует официальный kube-prometheus-stack, values — `cluster/observability/monitoring-values.yaml`. Release `lab-observability` владеет ServiceMonitor/PrometheusRule из одноимённого собственного chart. Другой stack, чужие CRD и другая версия chart требуют разбора: CLI отказывается выполнять неявное присвоение/обновление CRD.

| Компонент первой итерации | Версия |
|---|---|
| kube-prometheus-stack | `92.7.0` |
| Operator / config reloader | `v0.94.1` |
| Prometheus | `v3.15.0-distroless` |
| Alertmanager | `v0.34.1` |
| Grafana chart / приложение | `13.6.0` / `13.2.4-distroless` |
| kube-state-metrics chart / приложение | `8.6.0` / `2.20.0`; imageID фиксируется при установке |
| webhook certgen | `1.11.0` |

Источники: [Chart.yaml выбранного выпуска](https://github.com/prometheus-community/helm-charts/blob/kube-prometheus-stack-92.7.0/charts/kube-prometheus-stack/Chart.yaml), [values](https://github.com/prometheus-community/helm-charts/blob/kube-prometheus-stack-92.7.0/charts/kube-prometheus-stack/values.yaml), [Grafana chart](https://github.com/grafana-community/helm-charts/blob/grafana-13.6.0/charts/grafana/Chart.yaml), [совместимость Operator](https://prometheus-operator.dev/docs/getting-started/compatibility/). Заявленная совместимость включает Kubernetes 1.34; установка в нашем kind этим не доказана. Исполняемый профиль не использует `latest`. Версии SDK/Collector/Tempo/Loki/Alloy/exporter закрепляются перед соответствующими итерациями, сейчас они не устанавливаются.

## Начальный бюджет

| Компонент | CPU requests / limits | Память requests / limits |
|---|---|---|
| Prometheus | 100m / 700m | 256Mi / 512Mi |
| Operator | 50m / 300m | 64Mi / 128Mi |
| Alertmanager | 25m / 200m | 32Mi / 64Mi |
| Grafana | 50m / 300m | 768Mi / 1Gi |
| kube-state-metrics | 25m / 200m | 48Mi / 64Mi |
| Два config reloader, суммарно | 20m / 200m | 32Mi / 96Mi |
| Временный webhook Job | 10m / 100m | 16Mi / 32Mi |

Суммарный бюджет работающих компонентов monitoring в конфигурации: 270m / 1900m, 1200Mi / 1888Mi без hook и отдельного release exporters. После OOMKilled при 256Mi Grafana последовательно получила лимиты 512Mi и 768Mi. С двумя панелями 21 снимок за 10 минут показал 628–712Mi без перезапусков при лимите 768Mi: запас до лимита составил только 56Mi. Поэтому установлен request 768Mi и limit 1Gi. Последующее наблюдение нового профиля показало 225–762Mi за 15 минут 5 секунд без перезапусков; запас до лимита по максимуму выборки — 262Mi. CPU и остальные компоненты не изменены. Это резерв памяти, не исправление доказанной утечки; абсолютный пик, длительная устойчивость и достаточность для будущих панелей неизвестны. Применение профиля 768Mi/1Gi подтверждено на чистой ревизии dd444e40f65f; результаты обоих профилей сохранены в отчёте метрик зависимостей. Реальный размер TSDB требует измерений; прежние отказы сохранены в отчётах.

CLI требует один Ready-узел без pressure, запас requests 1408Mi + 512Mi, сумму заданных memory limits с дополнительными 2048Mi + 256Mi не выше allocatable и запас CPU requests 500m + 500m. Учитываются обычные init-контейнеры и memory overhead; неизвестные форматы и restartable init требуют разбора. На повторе собственный release заменяется бюджетом; Grafana использует Recreate, резерв учитывает перекрытие Operator/exporter. Незаданные limits системных компонентов не дают гарантии отсутствия давления.

Отдельно требуется Linux `MemAvailable` минимум 2048Mi + 512Mi (2560Mi) через `/proc/meminfo` в kind. Это память всей Linux-среды, не Pod. Preflight сохраняет Docker/kubelet: исходные снимки занижали память до неправдоподобных величин, поэтому не служат единственным основанием установки. Ресурсы Kafka/PostgreSQL/KEDA автоматически не уменьшаются.

## Обнаружение, хранение и доступ

Operator наблюдает свой namespace. Prometheus выбирает ServiceMonitor/PodMonitor/PrometheusRule в нём по `release: lab-monitoring`. ServiceMonitor API выбирает Service в точном namespace приложения по меткам name/instance, порт `http`, путь `/metrics`. Обнаруженный объект ещё не доказывает UP и samples.

Prometheus: одна реплика, PVC 2Gi, StorageClass `standard`, retention 6 часов, целевой размер блоков 512MB. WAL/head и асинхронное удаление не позволяют считать retentionSize строгой квотой диска. PVC retention — Retain, но reclaim policy `standard` — Delete: удаление PVC опасно. Потеря kind-кластера не покрывается гарантией сохранности метрик.

Grafana/Alertmanager используют временное хранение. Datasource Prometheus определяется chart; будущие панели будут храниться в Git. Ручные настройки и silences могут потеряться после замены Pod. Receiver первой итерации `local-null` не доказывает доставку и Resolved.

Случайный пароль Grafana хранится в Secret `lab-grafana-admin`: создаётся через stdin командой create, без last-applied annotation, не печатается, не записывается локально и не заменяет существующий Secret. Anonymous access и регистрация отключены. UI — ClusterIP; port-forward слушает 127.0.0.1 не более 12 минут. Диагностика API использует Kubernetes Service proxy с RBAC пользователя.

Grafana использует штатное provisioning datasource через смонтированный chart ConfigMap, без двух sidecar и без API token/RBAC для наблюдения конфигурации Kubernetes. Панели далее также должны задаваться файлами в Git. Namespace контроллера не устраняет все cluster-scoped RBAC Operator/Prometheus. Node exporter, hostPath и hostNetwork не добавляются. Kindnet не доказывает изоляцию по наличию NetworkPolicy.

## Сигналы и дальнейшие итерации

Имена API-метрик сохраняются; p95 из sum/count не вычисляется. Подготовленная histogram и метрики publisher/worker/outbox описаны ниже. Запрещены metric labels с job/event/trace ID, idempotency key, raw URL, SQL-параметрами и payload.

Правило записывает `lab:api_targets_up:sum`. `LabAPIMetricsUnavailable` через две минуты учитывает down и отсутствие ряда. Это недоступность сбора, а не самостоятельное доказательство отказа API. Диагностика: discovery/selector, Service endpoints, сеть, `/metrics`; восстановление — UP и свежий build info. Полный набор оповещений ещё отсутствует.

Учебный профиль локального chart `apiScrapeFailure` по умолчанию false. Значение true переключает только ServiceMonitor API на HTTPS при неизменном HTTP-порту workload, сохраняя discovery. CLI применяет и восстанавливает профиль через существующий release lab-observability, не обновляя lab-monitoring или приложение; Helm wait не доказывает reconciliation Prometheus. Это управляемая несовместимость протоколов клиента сбора, не включение TLS в приложении и не проверка NetworkPolicy.

Прикладная трассировка: [контракт](application-tracing.md), [Passed в ограниченном объёме](../verification/application-tracing.md). W3C-контекст через миграцию 004 и Kafka headers, асинхронная parent-модель, повторы/DLQ, восстановление, отказ telemetry endpoint и SDK disabled проверены указанными опытами. Прежние миграции и гарантии SQL/offset сохраняются; sampling и пределы определены в контракте. Job/event/trace ID допустимы в журналах/spans, но не в индексных Loki labels.

CLI сохраняет отдельный UTC-каталог `.local-observability/` с командами, exit codes и ответами. Ошибка установки сохраняет объекты/PVC и собирает status. Автоматического удаления stack нет. Восстановление начинается с events и причин NotReady/OOM/Pending, затем повторяется тот же chart после устранения причины. Процедура — [инструкция](../engineering/observability-lab.md).

## Метрики зависимостей

Подготовлен отдельный Helm chart/release `lab-dependency-metrics` в namespace наблюдаемости. Он не дублирует lab-observability: владеет двумя exporter Deployment/ServiceAccount/PodMonitor, JMX ConfigMap/PodMonitor и дополнительными NetworkPolicy. Нет публичных Services, RBAC на Secret или токенов Kubernetes внутри exporter Pod. Секреты монтируются файлами. Сетевые правила задают потоки Prometheus → metrics, exporters → DNS/нужная зависимость; они не доказывают изоляцию в основном kind без проверенного enforcement CNI.

PostgreSQL: `quay.io/prometheuscommunity/postgres-exporter:v0.18.1`, версия с поддержкой PostgreSQL 16 и password-file в [закреплённой документации](https://github.com/prometheus-community/postgres_exporter/blob/v0.18.1/README.md). Пользователь lab_metrics получает pg_monitor/CONNECT, не superuser/read_all_data или права jobs/outbox. [pg_monitor](https://www.postgresql.org/docs/16/predefined-roles.html) предоставляет статистические представления и функции, не бизнес-таблицы. Подготовка сверяет атрибуты, memberships, эффективные права public-таблиц и CREATE; чужая роль/Secret не присваиваются. Пароль случайный, только Secret/память/stdin, не миграция/Helm values/вывод. Лимит два подключения, statement_timeout=3s, lock_timeout=1s, default_transaction_read_only=on. Последнее — защита сеанса, не замена grants. Сохраняется существующий внутренний PostgreSQL без TLS; шифрование этого пути не заявляется. SQL query text не экспортируется.

Kafka: `docker.io/danielqsj/kafka-exporter:v1.10.0`, TLS/фильтры и семантика lag из [закреплённого README](https://github.com/danielqsj/kafka_exporter/blob/v1.10.0/README.md). KafkaUser lab-metrics получает Describe topic lab.jobs.v1, group lab-workers и cluster для metadata/listing, без Write/Read/IdempotentWrite/изменения offsets. Describe cluster раскрывает metadata шире одного topic; output ограничен фильтрами. Клиент использует mTLS 9093 с проверкой CA/имени; протокол 4.0.0 требует проверки с broker 4.2.2. offset.show-all сохраняет наблюдение при пустой группе, workers ограничены одним, concurrent scrapes отключены. При отличии сертификатов повтор подготовки требует явного разбора ротации. Group members не считается состоянием группы, missing/-1 lag не считается нулём.

Broker requests/errors принадлежат JMX, не lag exporter. [Strimzi metricsConfig](https://github.com/strimzi/strimzi-kafka-operator/blob/1.3.0/examples/metrics/kafka-metrics.yaml) ссылается на ConfigMap в Kafka namespace, источник включения — `cluster/kafka/metrics-patch.json`. Частичный patch не является второй копией Kafka/KafkaNodePool. Он не меняет storage/listeners/version, но может вызвать restart единственного broker/controller. Применяется только явной dependency-metrics-install после сохранения Kafka/PVC и проверки Operator. Перед удалением ConfigMap нужно убрать JMX reference и дождаться reconciliation.

Новые exporters: каждый CPU 25/200m и память 64/128Mi, Recreate, одна реплика. Начальная суммарная оценка — 50m/128Mi requests и 400m/256Mi limits, не измеренное потребление. Установка дополнительно проверяет запас 256Mi requests / 384Mi limits и 100m CPU поверх monitoring-бюджета: exporters плюс allowance 128Mi/50m для JMX. Это не увеличение broker heap/cgroup-limit. На повторе действующие exporters могут учитываться ещё раз: проверка намеренно консервативна, не исключает чужие Pod. Ресурсы приложения не уменьшаются; actual host MemAvailable проверяется прежним preflight.

Панель `Lab Dependency Metrics`, UID `lab-dependency-metrics`, имеет JSON `cluster/observability/dashboards/dependencies.json`. Оба JSON попадают в Grafana native provisioning при observability-install; exporter release не удаляется. До установки collectors No data ожидаемо. Отношение numbackends/max_connections учитывает все БД и настроенный максимум, не только пулы приложения и не вычитает reserved slots. Счётчики транзакций не равны числу jobs. Нельзя подменять пустые сигналы vector(0) или считать UP exporter доказательством pg_up/доставки Kafka. Приёмка имеет [Passed](../verification/dependency-metrics.md) в границах сохранённых samples, сравнения lag, SQL/Kafka прав, PVC и интервала ресурсов.

## Прикладные метрики

Прикладной набор установлен и проверен в границах [отчёта](../verification/application-metrics.md). Go module не изменён: существующий текстовый exporter API сохраняется, небольшой пакет `internal/telemetry` обслуживает фиксированные семейства publisher/worker и общие classic buckets. Native histogram и перенос всего приложения на другой framework не вводятся. Classic histogram выбрана как совместимое расширение существующего текстового формата; [Prometheus](https://prometheus.io/docs/practices/histograms/) различает её агрегируемые buckets и summary.

API сохраняет `lab_http_request_duration_seconds_sum/count` и добавляет `lab_http_request_latency_seconds_bucket/sum/count` с прежними ограниченными method/route/status. Общие buckets: 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30 секунд и +Inf. Это лабораторные границы для быстрых HTTP и управляемой работы до 8 секунд, не production SLO. p95 вычисляется через histogram_quantile по rate buckets; при отсутствии наблюдений допустим NaN. Среднее старого summary не называется p95.

Publisher и worker предоставляют /metrics на существующих health listener 8081/8082 независимо от readiness зависимостей. Никаких новых Service или публичных портов. PodMonitor в namespace платформы выбирает точный namespace приложения, name/instance процесса и named port health. Job label берётся из app.kubernetes.io/name; interval=15s, timeout=5s, sampleLimit=2000, targetLimit=8. Прежде чем обновлять локальный chart до 0.3.0, нужно обновить образы процессов: старый /metrics может вернуть 404. Worker fixed/KEDA ownership не меняется.

| Сигнал | Источник и точка изменения |
|---|---|
| `lab_publisher_attempts_total`, `retries_total` | Полученный SQL claim; retries — claim с persisted attempt>1, не число уникальных событий |
| `lab_publisher_publish_success_total`, `publish_errors_total` | Результат синхронной Kafka delivery; успех означает broker ack, не SQL mark |
| `lab_publisher_outbox_published_total`, `outbox_mark_errors_total` | Успешная/неуспешная фиксация published_at после ack |
| `lab_publisher_claim_errors_total` | Ошибка SQL claim кроме отсутствия ожидающей работы |
| `lab_publisher_last_broker_ack_timestamp_seconds` | Время последнего наблюдаемого успешного ack; 0 до первого |
| `lab_worker_jobs_processed_total` | Новый SQL-эффект после успешного Complete, до offset commit; duplicate не увеличивает его |
| `lab_worker_jobs_duplicates_total` | Обнаруженный готовый эффект при Start либо гонка при Complete |
| `lab_worker_jobs_failed_total` | Неуспешные попытки Handle кроме обычной отмены/ожидания retry, плюс сохранённые управляемые ошибки; не число terminal failed jobs. Ошибка offset после SQL может увеличить failed и processed одновременно |
| `lab_worker_jobs_retried_total` | Успешный RecordFailure, не каждый poll/ожидание срока повтора |
| `lab_worker_jobs_dlq_total` | Новый broker ack, затем сохранённая DLQ-квитанция; существующая квитанция не увеличивает счётчик |
| `lab_worker_kafka_offset_commits_total`, `kafka_offset_commit_failures_total` | Результат вызова ручного Kafka commit; не смешивается с SQL-эффектом |
| `lab_{publisher,worker}_active`, `attempt_duration_seconds` | Число принятых попыток и histogram времени до возврата, включая DB/Kafka/работу; idle publisher claim не входит |

Все process counters обнуляются при перезапуске; crash между durable действием и scrape может скрыть increment. Они не заменяют SQL/offset доказательства. Для worker безошибочная DLQ-обработка имеет собственный счётчик и не считается успешным SQL-эффектом. Прямых labels с job/event ID, topic из сообщения или текстом ошибки нет; registry разрешает только заранее зарегистрированные имена. lab_build_info использует только данные сборки. Метаданные Pod/instance добавляет Prometheus при discovery, а не приложение из payload.

Outbox измеряет PostgreSQL: count неопубликованных строк и возраст минимального created_at, включая lease/retry. Период 10 секунд, запрос до 2 секунд через существующий publisher pool (max 4); работа SQL не выполняется при scrape. При первом запуске pending/age=NaN до удачного снимка. При ошибке сохраняется прежний снимок, stats_up=0 и растёт `lab_publisher_outbox_stats_errors_total`; время последней удачи не меняется. Gauges: `lab_outbox_pending_events`, `lab_outbox_oldest_pending_age_seconds`, `lab_outbox_stats_up`, `lab_outbox_stats_last_success_timestamp_seconds`. Возраст — значение на момент SQL-измерения, не экстраполяция до текущего времени. Для нескольких publisher это повторные снимки одной очереди: не суммировать их как разные очереди. Панель использует max только для успешных снимков младше 30 секунд; иначе No data, а не достоверный ноль. Sampler отменяется и ожидается перед закрытием DB; новых соединений сверх существующего pool limit не добавляется.

Первая панель `Lab Application Metrics`, UID `lab-application-metrics`, имеет один источник JSON в `cluster/observability/dashboards/application.json`. CLI формирует из него отдельные native chart values для render/install; JSON не загружается по внешнему URL, sidecar и Kubernetes token для provisioning не используются. При установке закреплённый chart создал временный init-контейнер `download-dashboards`; его наличие не означает внешнее происхождение JSON. Файлы принадлежат lab-monitoring; повторная загрузка после замены Grafana Pod подтверждена [сценарием](../verification/application-metrics.md). Панель показывает прикладные сигналы, не фиктивные Kafka lag/pg_up/трассировки. Измерения памяти и границы применения профилей приведены в разделе бюджета выше и профильных отчётах.

`application-metrics-verify` требует реальный API target и все targets UP, отдельные publisher/worker UP, outbox stats_up=1 и возраст успешного снимка меньше 30 секунд. Непустой raw gauge или нулевой counter сами по себе не закрывают эксперимент. После добавления двух PodMonitor прежний опыт отказа API scrape относится только к API; его повтор не должен ломать метрики процессов.
