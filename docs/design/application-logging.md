# Централизованные журналы приложения

## Current и Target

**Current:** Helm chart `lab-logging`, команды `labctl` и datasource Loki установлены и имеют [Passed в ограниченном объёме](../verification/application-logging.md): обычное задание, два управляемых повтора, связь трёх процессов по контексту, переход Loki → Tempo, 31 снимок ресурсов за 15 минут без перезапусков и финальные 23/23 UP targets/smoke. `kubectl logs` сохраняется как независимый способ диагностики. Потеря/повторная доставка журналов при отказе сборщика или backend, фактическое удаление по retention и длительная устойчивость не проверены. Отдельный сценарий отказа наблюдаемости и оповещения остаются Target Milestone 8.

## Граница и сбор

Один `StatefulSet` Loki и один `Deployment` Alloy работают в существующем namespace наблюдаемости с `restricted` Pod Security. Локальный chart содержит только необходимые ресурсы фиксированного стенда; универсальный upstream chart с распределёнными компонентами, gateway и кэшами здесь не нужен. Образы закреплены: `grafana/loki:3.7.9`, `grafana/alloy:v1.21.3`; источники версий — [Loki](https://github.com/grafana/loki/tree/v3.7.9), [Alloy](https://github.com/grafana/alloy/tree/v1.21.3). Теги не являются фиксацией digest; фактический `imageID` нужно сохранить при выполнении.

`loki.source.kubernetes` читает через API журналы Pod с именами приложения `lab-api`, `lab-publisher`, `lab-worker` в `kubernetes-application-lab`. Нет `hostPath`, `hostNetwork`, привилегированного контейнера или DaemonSet. Это уменьшает права среды выполнения, но увеличивает нагрузку на API/kubelet по сравнению с чтением файлов; см. [контракт компонента](https://grafana.com/docs/alloy/latest/reference/components/loki/loki.source.kubernetes/).

Alloy имеет отдельный ServiceAccount и namespace Role: `get/list/watch pods`, `get pods/log`. RBAC разрешает чтение всех Pod/журналов этого namespace; фильтр приложения — конфигурация сборщика, не граница авторизации. Role не даёт Secret, exec, nodes или изменения ресурсов. Для API необходим автоматически проецируемый токен. Loki и прежние процессы приложения токен для сбора не получают.

В индекс допускаются только `namespace`, `service`, `pod`, `container`. Метаданные discovery нужны для API и удаляются после чтения. `level`, `job_id`, `event_id`, `trace_id`, `span_id`, request ID остаются в исходном JSON. Существующий `slog` с UTC, service/event и контекстом не заменяется; полные сообщения Kafka, SQL и пароли не добавляются в журнал. Поиск идентификатора выполняется по содержимому, а не по индексному label. В CLI ответ проверяется на посторонние индексные labels.

## Хранилище, ресурсы и отказ

Loki — одиночный процесс `-target=all`, TSDB v13, filesystem, replication=1, внутренний `ClusterIP:3100`. PVC `data-lab-loki-0`: 2Gi, `standard`, `ReadWriteOnce`, сохраняется при удалении StatefulSet/масштабировании. Удаление PVC при StorageClass `Delete` уничтожает данные; сценарий не удаляет PVC. Узел kind остаётся единственной границей хранения, отказоустойчивость или сохранность после удаления кластера не заявляются.

Retention 24 часа, удаление compactor отложено ещё на 2 часа. Это срок хранения, а не гарантия свободного места: при большой нагрузке 2Gi могут закончиться раньше. Ограничены скорость приёма, число streams, размер строки, длительность/результат запроса. В опыте используется несколько заданий, без массовой нагрузки. WAL Loki расположен на том же PVC.

Loki: CPU 100m/500m, память 512Mi/1Gi. Alloy: CPU 50m/300m, память 128Mi/256Mi. Установка проверяет фактические node allocations/pressure и host MemAvailable; дополнительный бюджет 768Mi requests/1408Mi limits включает 128Mi запаса поверх двух процессов. При повторе действующие Pod учитываются консервативно ещё раз. Это начальные ограничения, не результаты измерений.

Alloy использует ограниченный `emptyDir`, конечные повторы отправки, batch 256KiB и таймаут 5 секунд; параметры соответствуют [loki.write](https://grafana.com/docs/alloy/latest/reference/components/loki/loki.write/). Позиции API-чтения хранятся в `emptyDir` и теряются при замене Pod; дисковая очередь доставки не включена. При длительном отказе Loki или замене Alloy возможны потеря и повторное чтение строк; сборщик не участвует в транзакции приложения. Сбор журналов не даёт exactly-once delivery. Readiness проверяет локальную готовность; liveness — открытый локальный порт, а не здоровье внешнего backend.

NetworkPolicy задают Alloy → DNS/API/Loki, Grafana/Prometheus → Loki и Prometheus → Alloy. Для API оставлено правило только по портам TCP 443/6443 из-за Service DNAT; это не allowlist адресов API. В основном kind с kindnet enforcement не подтверждён. Loki без аутентификации допустим только для этого локального внутреннего стенда; политика не заменяет аутентификацию. Публичных Services/Ingress нет.

## Проверка и Grafana

`logging-render` сохраняет YAML без установки. `logging-install` проверяет namespace, StorageClass, владельца release/версию, PodMonitor CRD, ресурсы и server dry-run, затем устанавливает только `lab-logging`; миграции/образы приложения не меняются. Неуспешные объекты остаются для диагностики. Два PodMonitor позволяют наблюдать процессы сбора отдельно от приложения.

`logging-query` принимает UUID либо сохранённый job locator, читает последние 30 минут, максимум 500 строк через API proxy. Достижение лимита — ошибка полноты, пустой результат не считается успехом. `logging-verify` требует `job_accepted`, `outbox_broker_ack`, `worker_received`, `worker_effect_committed`, `worker_offset_committed`, правильные service и общий trace ID, согласованный event ID publisher/worker. Для этого опыта нужен диагностический профиль tracing, поскольку при SDK disabled trace ID отсутствует. `logging-retry-verify` дополнительно требует два различимых `worker_retry_scheduled`; повторная доставка одной строки не считается новой попыткой. Эти команды не доказывают SQL-эффект или Kafka commit сами по себе: нужны прежние независимые SQL/offset проверки.

Loki добавлен в native datasource provisioning Grafana с UID `loki`; после `observability-install` нужен новый port-forward, если Pod заменился. Derived field TraceID ведёт в существующий Tempo. Работа ссылки и запросов подтверждается только опытом в Grafana. Пошаговый порядок — [процедура](../engineering/application-logging-lab.md).
