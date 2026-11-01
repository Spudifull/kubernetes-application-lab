# KEDA: масштабирование worker по Kafka lag

Состояние — [Passed](../verification/keda.md) для выполненного масштабирования 1–3 и штатного возврата Helm. Контракт — [Kafka и обработка заданий](../design/kafka-event-processing.md). Сценарий измеряет цепочку lag → External Metrics/HPA → replicas → consumer assignment → уменьшение lag → возврат к одной реплике. Наличие ScaledObject или завершённой партии само по себе не доказывает масштабирование.

## До установки

Выполнить `go test ./...`, `go test -race ./cmd/api ./cmd/labctl ./internal/scheduled`, `go vet ./...`, `task worker-helm-lint`, `task keda-worker-helm-lint`, `task cron-helm-lint`. Проверки запускает владелец; результаты выполненного прогона сохранены в отчёте. После успеха зафиксировать код, сохранить SHA/чистоту дерева. Закончить и очистить опыты отказов CronJob. Нужны Ready Kafka/PostgreSQL/publisher/worker, миграция 003, pending=0, lag=0, worker без armed faults, CronJob normal/suspended/inactive.

```powershell
task keda-preflight
task keda-render
```

Сохранить вывод preflight и осмотреть `.local-kafka/keda-rendered.yaml`. Установка использует [официальный chart 2.21.0](https://github.com/kedacore/charts/releases/tag/v2.21.0), совместимый с Kubernetes 1.34. Namespace и watchNamespace — kubernetes-application-lab. RBAC, APIService и CRD имеют также кластерные части: это не изолированная установка исключительно namespaced ресурсов. Действующий другой External Metrics provider не заменяется. Сертификаты APIService управляются официальным chart; CRD не удаляются при завершении опыта.

Бюджет KEDA: requests 250m CPU/320Mi памяти, limits 1250m/896Mi для operator/metrics/webhooks в сумме. Две дополнительные worker-реплики требуют ещё 100m/128Mi requests, limits 1000m/256Mi. Проверить свободный бюджет узла по reservations и фактической памяти; статическая сумма не гарантирует достаточности среды. Prometheus/Grafana не устанавливаются.

```powershell
task keda-install
task keda-identity-create
task keda-secret-create
task keda-enable
task keda-status
```

Отдельный KafkaUser lab-keda имеет Describe только для lab.jobs.v1 и lab-workers. CA/client certificate/key копируются в lab-keda-kafka в памяти, не печатаются и не сохраняются на диск. TriggerAuthentication использует mTLS с проверкой CA, SASL отключён. Certificate rotation не перехватывается автоматически: несовпадение существующего Secret требует отдельного разбора. Проверить отсутствие Write/Read/IdempotentWrite ACL у lab-keda; рабочие identities не меняются.

Secret access ограничен namespace установки, но не только именами lab-keda-kafka/kedaorg-certs: официальный chart сохраняет namespaced чтение/list/watch Secret для работы контроллера и сертификатов. Значение namesAllowList при restrict.secret=true не применяется, поэтому оно удалено. Не считать этот режим изоляцией от PostgreSQL Secret того же namespace. Custom scale targets отключены: правила с wildcard */scale и get всех ресурсов для них не нужны. Итоговый RBAC проверить в rendered YAML и после установки. Проверить APIService Available, ScaledObject Ready, HPA ScalingActive и числовую текущую метрику вместо unknown. До Ready нагрузку не создавать. Kafka protocol version=4.0.0 выбран для актуальных request versions Kafka 4; совместимость с фактическим брокером подтверждается успешным чтением метрики, не наличием поля.

## Партия и наблюдение

Нужен существующий localhost port-forward API на 18081. Новую сборку worker для смены масштабирования не требуется развёртывать: chart использует проверенный записанный образ, соответствие image ID проверяется. Не выполнять обычные worker-deploy/stop/fault-команды, пока ScaledObject владеет worker: CLI блокирует их.

В отдельном окне запустить наблюдение до создания нагрузки:

```powershell
task keda-observe | Tee-Object .local-kafka/keda-observe.jsonl
```

Команда каждые пять секунд до пяти минут сохраняет Deployment/HPA snapshots с временем. В основном окне:

```powershell
task keda-load
```

Партия — 30 уникальных заданий по 8 секунд, ожидание результата до пяти минут. Идентификаторы сохраняются в `.local-kafka/worker-batch.json` после приёма партии; вывод enqueue содержит каждый job_id. Не запускать параллельно worker-batch: он заменит файл. Если приём прервался, сохранить уже выведенные IDs и SQL; не считать частичную партию полным опытом.

Во время нагрузки в третьем окне выполнить:

```powershell
task keda-status
task worker-group-status
task keda-check
```

Нужны положительный lag/числовая External Metric, рост desired/current/ready replicas выше одного и не выше трёх, несколько членов группы и assignment partitions 0/1/2. Topic имеет три partitions, allowIdleConsumers=false. MinReplicaCount=1: масштабирование до нуля в этом опыте не проверяется.

Worker имеет MaxOpenConns=4, три реплики дают верхний предел 12 соединений только worker, не всей БД. `keda-check` выводит текущий pg_stat_activity по client_addr и max_connections: сопоставить адреса с Pod IP, учесть отдельные пулы API/publisher, cron и диагностический psql. Одного снимка после окончания нагрузки недостаточно для утверждения о давлении во время неё.

## Завершение и возврат управления

После worker_batch_completed проверить `task keda-check`: все 30 IDs succeeded, один outbox и один эффект на каждый. Группа Stable и lag=0; после stabilizationWindowSeconds=60 плюс интервалы HPA/polling наблюдать возврат к одной реплике. Завершение партии на одной реплике без зафиксированного роста — не Passed масштабирования. Повторы/перераспределения могут менять длительность: жёсткий SLA обработки не заявляется.

```powershell
task keda-status
task worker-group-status
task keda-check
task keda-disable
task worker-status
task outbox-status
task db-status
task smoke
task worker-diagnostics-cleanup
```

Disable через Helm ставит paused-replicas=1, ждёт одну реплику, удаляет только принадлежащий worker release ScaledObject, ждёт удаления его HPA, затем Helm возвращает fixed replicas=1. Промежуточное удаление ресурса останавливает управляющий цикл до возврата поля replicas; chart после upgrade больше не содержит ScaledObject/TriggerAuthentication. Ошибку handoff сохранить, не использовать force ownership или ручное редактирование replicas параллельно KEDA. Штатный disable проверен в кластере; повтор после частичного отказа требует отдельного опыта.

Официальный KEDA release, read-only KafkaUser и Secret остаются установленными, но больше не управляют worker. Ни uninstall всей KEDA, ни удаление CRD/External Metrics API автоматически не выполняются. Все SQL/Kafka данные сохраняются. Сохранить SHA/образы/UID, времена, peak replicas, lag/assignment, метрику HPA, connection snapshots, результаты партии и handoff. Сбой брокера, scale-to-zero и пропажа метрик требуют отдельных опытов.
