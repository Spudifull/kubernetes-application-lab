# Проверка метрик зависимостей

Статус [Passed в ограниченном объёме](../verification/dependency-metrics.md). Только основной kind; приложение, PostgreSQL и Kafka уже установлены. Секреты не сохранять в локальные отчёты и не выводить. Новых Go modules или миграций приложения нет. Chart exporters отделён от chart прикладных monitors, чтобы обычное обновление последних не удаляло JMX config или collectors.

## Статическая проверка и подготовка

Владелец выполняет `go test -count=1 -race ./cmd/labctl`, `go vet ./cmd/labctl`, `task dependency-metrics-helm-lint`, `task dependency-metrics-render`, `task observability-render`. Просмотреть dependencies-rendered.yaml, dashboard-values.json и Kafka metrics-patch.json: нет Write/Read Kafka ACL, административных SQL credentials, публичных Services, wildcard-параметров сборщиков и SQL query text labels. Для Kubernetes kinds выполнить schema validation под 1.34, для CR — server dry-run после установленного Operator. Install выполняет server dry-run; это не доказательство поведения.

После commit чистого дерева сохранить `task observability-preflight`, `task observability-status`, `task worker-group-status`, `task outbox-status` и `task kafka-status`. Если worker/HPA управляется KEDA, сначала согласовать возврат фиксированного режима по существующей инструкции, не конкурировать со scaler. Прилагаемый сценарий предполагает worker=1, Cron suspended и отсутствие активного отказа.

Выполнить `task dependency-metrics-prepare`. Команда создаёт выделенный Secret/SQL role и KafkaUser, ждёт Ready удостоверения и копирует только CA/client cert/key в namespace наблюдаемости. Доступ к SQL административный только на этом шаге, password передаётся psql на stdin. Команда не записывает secret outputs. Повтор использует прежний пароль и отказывается перезаписать чужой Secret или молча заменить сертификат. Сбой может оставить Secret и/или роль: сохранить состояние, повторить после диагностики; автоматического удаления нет.

## Установка

Перед изменением сохранить PVC UID/PV и Kafka generation. Выполнить `task dependency-metrics-install`. Это явное согласие на установку отдельного release и включение JMX, которое может перезапустить единственный broker/controller. Не запускать опыт отказа брокера одновременно. Команда проверяет прежний Kafka metricsConfig, готовность Operator, ёмкость и Secret, сохраняет прежний Kafka CR/PVC, устанавливает Helm chart, затем patch только metricsConfig. Kafka version/listeners/storage/replicas не меняются. Другие workloads не урезаются.

После patch просмотреть Kafka CR:

```powershell
$ctx = 'kind-kubernetes-application-lab'
$k = kubectl --context $ctx get kafka lab-kafka -n kubernetes-application-lab-kafka -o json | ConvertFrom-Json
$k.metadata.generation
$k.status.observedGeneration
$k.status.conditions | Format-Table type,status,reason,message
kubectl --context $ctx get pods -n kubernetes-application-lab-kafka
```

Требуются одинаковые generation/observedGeneration и актуальный Ready; если Operator ещё согласует изменение, повторить чтение. Сохранить новые Pod UID/imageIDs и прежние PVC UID/PV. Затем выполнить `task dependency-metrics-verify`, `task worker-group-status`, `task application-metrics-verify`, `task smoke`. JMX request series должны появиться от реального трафика; ошибка/отсутствие series не засчитываются как нулевые ошибки. Полный verify требует collector error=0 и три неотрицательных partition lag, но не требует отсутствующих error series. Отсутствие группы или несохранённый offset требуют диагностики, не создания нулевых рядов.

## Панель и запросы

После успешной установки зависимостей обновить provisioning через `task observability-install`: официальный monitoring release получит оба JSON из Git. Это не устанавливает exporters повторно и не сбрасывает их отдельный release; может пересоздать Grafana. Повторить targets/health/smoke и запустить новый `task grafana-forward`. Панель `/d/lab-dependency-metrics` в папке Kubernetes Application Lab. До установки collectors No data здесь ожидается и не является Passed.

Ключи `task prometheus-query QUERY=...`:

| Ключ | Смысл |
|---|---|
| pg-up / pg-collector-errors | соединение SQL / общий признак ошибки старого набора collectors; новый набор проверяется отдельно |
| pg-collectors | успешность каждого нового collector; все значения должны быть 1, отсутствие ряда не равно успеху |
| pg-connections / pg-max-connections / pg-connection-ratio | текущие client backends во всех БД / max_connections / отношение; reserved slots не вычитаются |
| pg-states | число сессий по состоянию, включая idle in transaction; не текст запросов |
| pg-transactions / pg-rollbacks | накопленные commits/rollbacks БД lab; не число jobs |
| kafka-brokers | обнаруженные брокеры; не доказательство возможности Produce |
| kafka-lag / kafka-members | отставание по partition / число участников lab-workers; members не заменяет состояние группы |
| kafka-requests / kafka-errors | broker JMX counters; ошибки исключают error=NONE |
| dependencies-health | UP трёх источников, pg_up, отсутствие collector errors, один broker и три валидных lag; не end-to-end обработка |

На управляемом backlog сравнить каждый partition с `task worker-group-status` до нагрузки, при остановленном worker и после его возврата. Статистика не синхронна; фиксировать UTC и координаты. Outbox pending — события до публикации, consumer lag — после публикации и до offset commit. Не вычислять один сигнал из другого. PostgreSQL numbackends/max_connections не показывает отдельно лимит пула приложения; при масштабировании суммировать пулы всех реплик и служебные подключения.

## Проверка прав и возврат

Отрицательная Kafka-проверка подготовлена командой `task dependency-metrics-acl-test`. Сначала владелец выполняет `go test -count=1 -race ./cmd/labctl` и `go vet ./cmd/labctl`, затем создаёт commit. Образы приложения пересобирать не нужно: проверка выполняется локальным labctl. В отдельном окне с kubectl 1.35.9 открыть `kubectl --context kind-kubernetes-application-lab port-forward pod/lab-kafka-dual-role-0 19094:9093 -n kubernetes-application-lab-kafka --address 127.0.0.1`. Перед и после команды сохранить `task worker-group-status`, `task prometheus-query QUERY=kafka-lag` и `task outbox-status`.

Проверка читает только выделенный Secret lab-metrics-kafka в память, проверяет CA/имя сервера и использует loopback 19094 для всех соединений с единственным broker. Никакие ключи не пишутся на диск. Она сначала читает metadata существующего topic, затем посылает непосредственно broker запрос Produce без записей и OffsetCommit с offset=-1, оба только для несуществующей partition -1. Реальные partition 0/1/2 не адресуются. Участник группы и producer ID не создаются; auto topic creation отключено. В Kafka проверка Write topic и Read group выполняется до обработки partition ([KafkaApis](https://github.com/apache/kafka/blob/4.2.0/core/src/main/scala/kafka/server/KafkaApis.scala)). Допустимы только ответы TOPIC_AUTHORIZATION_FAILED (29) и GROUP_AUTHORIZATION_FAILED (30); INVALID_REQUEST, UNKNOWN_TOPIC_OR_PARTITION, таймаут или ошибка TLS не означают успех. Результаты сохраняются в `.local-observability/<UTC>-dependencies-acl-check`. Это ограниченная проверка отказа двух конкретных протокольных операций, а не всех возможных Kafka API.

После сохранения результатов закрыть только новое окно Kafka port-forward через Ctrl+C. Команда не создаёт диагностических Pod, не меняет Helm, ACL, topics или PVC. Рабочие offsets/end до и после должны совпасть при отсутствии новой нагрузки. Владелец выполнил команду на чистой ревизии 405c58554a8c: оба ожидаемых отказа и неизменные offsets/end подтверждены в отчёте. Диагностические Pod от worker-group-status после сохранения результатов удаляются явной командой `task worker-diagnostics-cleanup`.

Просмотреть только metadata/ACL KafkaUser, не Secret. SQL-скрипт проверяет эффективные права на public-таблицы и CREATE; отдельно сохранить has_table_privilege=false и отказ UPDATE под lab_metrics (операция с WHERE false в rollback-транзакции), не изменяя jobs. Для отрицательной Kafka ACL-проверки использовать описанную выше команду: запрещено проверять запись настоящим job event или сбрасывать рабочий offset. Права проверены отдельно в [отчёте зависимостей](../verification/dependency-metrics.md); при новом прогоне одни targets UP по-прежнему не доказывают минимальные права.

После прогона вернуть worker штатным `task worker-deploy`, проверить выбранные задания, outbox, lag, SQL, PVC и API. Диагностические Pod удалить только после фиксации доказательств существующей явной командой. Для отказов exporter изучать startup/logs, cert permissions, mTLS, ACL и точные имена collector metrics, а не расширять права автоматически. При необходимости согласованного отключения сначала снять JMX reference и дождаться Kafka reconciliation, затем разбирать exporter release; данные не удалять.
