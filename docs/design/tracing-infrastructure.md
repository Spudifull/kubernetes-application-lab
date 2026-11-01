# Приём и хранение трассировок

**Состояние: Current для установленного транспорта одного диагностического span и ограниченного наблюдения ресурсов.** Приём Collector, чтение Tempo/Grafana, новые UP targets и 15 минут без перезапусков подтверждены в отчёте. SDK и трассировка заданий остаются Target: пока действуют метрики, журналы stdout и диагностика labctl; разрыв закрывается SDK и сохранённым контекстом outbox/Kafka с реальными spans задания.

## Владение и версии

Только основной kind/Kubernetes 1.34, namespace `kubernetes-application-lab-observability`. Collector — один Deployment, Tempo — один StatefulSet в monolithic mode, без отдельного Kafka для трассировок, object storage, metrics-generator или дополнительного Prometheus. Используются закреплённые upstream charts, а не копии их ресурсов в проекте:

| Release | Chart / версия | Образ |
|---|---|---|
| lab-otel-collector | opentelemetry-collector / 0.178.1 | otel/opentelemetry-collector:0.163.2 |
| lab-tempo | tempo / 3.2.1 | docker.io/grafana/tempo:3.1.1 |
| lab-tracing-controls | локальный chart / 0.1.0 | нет контейнеров, только NetworkPolicy |

Выбор проверен по [Collector Chart.yaml](https://github.com/open-telemetry/opentelemetry-helm-charts/blob/opentelemetry-collector-0.178.1/charts/opentelemetry-collector/Chart.yaml), [Collector values](https://github.com/open-telemetry/opentelemetry-helm-charts/blob/opentelemetry-collector-0.178.1/charts/opentelemetry-collector/values.yaml), [Tempo Chart.yaml](https://github.com/grafana-community/helm-charts/blob/tempo-3.2.1/charts/tempo/Chart.yaml) и [Tempo values](https://github.com/grafana-community/helm-charts/blob/tempo-3.2.1/charts/tempo/values.yaml). Tempo chart поддерживается сообществом Grafana; это не обещание проверенной совместимости с нашим кластером. Tempo 3 использует live_store/backend_scheduler/backend_worker; конфигурация ingester/compactor Tempo 2 не копируется. [Описание режимов](https://grafana.com/docs/tempo/latest/set-up-for-tracing/setup-tempo/) допускает monolithic для небольшого стенда.

## Путь и ограничения

Будущие API/publisher/worker направляют OTLP в Collector (4317 gRPC или 4318 HTTP), только Collector направляет traces в Tempo (4317). Grafana читает Tempo HTTP API на 3200 через datasource UID `tempo`. Приложения не получают прямой endpoint Tempo. Регистрация datasource до установки Tempo даёт недоступный источник, а не доказательство трассировки. Существующие данные приложения/миграции и Kafka offsets не меняются.

Collector принимает только traces: лишние receivers и pipelines выключены. Порядок processors — memory_limiter, batch. Memory limiter: 192Mi, spike 32Mi, проверка каждую секунду; batch до 256 spans, отправка 128 или через секунду. Очередь exporter в памяти: 64 batch, один consumer, timeout 5s, retry до 30s с интервалами 1–5s. Очередь не переживает пересоздание Collector; переполнение, перезапуск или истечение retry могут терять spans. Отказ Tempo не должен блокировать приложение, но это будет проверено только после подключения SDK с ограниченной очередью. Лабораторная потеря telemetry допустима и должна быть видимой; exactly-once доставки traces не заявляется.

Collector и Tempo имеют собственные ServiceMonitor с release=lab-monitoring и интервалом 15s; Collector sampleLimit=3000. Ready, UP и запись одного диагностического span — разные уровни доказательства. Основной путь задания не считается реализованным до SDK, новой миграции outbox, W3C headers Kafka и сохранённых реальных spans API/publisher/worker.

## Ресурсы, хранилище и доступ

| Компонент | CPU requests / limits | Память requests / limits |
|---|---|---|
| Collector | 50m / 300m | 128Mi / 256Mi |
| Tempo | 100m / 700m | 512Mi / 1Gi |

Это начальный бюджет, не измеренные требования. CLI резервирует дополнительно 128Mi по requests и limits: 768Mi/1408Mi сверх обычной проверки monitoring. При повторной установке существующие tracing Pod также учитываются консервативно. MemAvailable должен быть не меньше 3968Mi (monitoring limit reserve + 512Mi + 1408Mi). Другие workload не урезаются. Предыдущее наблюдение Grafana не гарантирует достаточность её памяти при поиске трасс.

Tempo получает PVC 1Gi/standard, RWO; WAL и локальные blocks находятся на PVC, временный /tmp — ограниченный emptyDir 64Mi. Retention 1h асинхронная и не ограничивает мгновенное заполнение диска; наблюдать свободное место. Автоудаление PVC отключено. StorageClass Delete/WaitForFirstConsumer не даёт сохранности после удаления PVC/узла kind. HA и восстановление traces после потери кластера не заявляются.

Оба Service — ClusterIP. Loopback forward ограничен 12 минутами: Collector 14318→4318, Tempo 13200→3200. Внутри лаборатории OTLP не зашифрован и не аутентифицирован; Tempo не предоставляет встроенную аутентификацию. NetworkPolicy задаёт только нужные источники/порты: main namespace и три компонента к Collector, Collector к Tempo, Grafana/Prometheus к Tempo HTTP, DNS и self-traffic Tempo. Наличие policy не доказывает её исполнение основным kindnet CNI. Прямые loopback проверки не доказывают сетевой путь из Pod приложения.

Ни Collector, ни Tempo не нужны Kubernetes token, Roles или hostPath. Ограниченные контейнеры без privilege escalation, drop ALL, seccomp RuntimeDefault, read-only rootfs. PVC fsGroup=10001; init chown с root не вводится. При ошибке прав диагностировать volume, а не отключать ограничения. Не ослаблять securityContext приложения.

## Проверка и отказ

[Порядок запуска](../engineering/tracing-infrastructure-lab.md), [доказательства Passed в ограниченном объёме](../verification/tracing-infrastructure.md). Команды render/install/status/probe принадлежат Taskfile и labctl. Диагностический span имеет service.name=labctl-trace-probe и не изображает бизнес-задание. Проверка возвращённого span требует совпадения trace ID, span ID и имени; один HTTP 200 на приём не считается доставкой.

При сбое сохранять evidence, events, config и PVC, не выполнять автоматический uninstall/rollback. После исправления повторять закреплённый release. Явный rollback выполняется отдельно для соответствующего release к сохранённой revision; политика и конфигурация exporter должны оставаться согласованными. Не удалять Tempo PVC ради перезапуска. End-to-end SDK, sampling, контекст outbox/retries/DLQ, потеря telemetry и восстановление будут отдельной следующей итерацией.

Прикладной SDK и перенос контекста имеют отдельный [контракт](application-tracing.md) и статус Prepared. Проверенный транспорт не доказывает их работу.
