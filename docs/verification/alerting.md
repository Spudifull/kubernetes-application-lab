> Historical result from the private development history. Original identifiers are preserved; this is not a rerun of the public snapshot. Reviewed screenshots are included; raw captures remain private. See [provenance](../provenance.md). Local paths, where present, were redacted.

# Доставка оповещения outbox и восстановление

**Статус: Passed в ограниченном объёме — первый цикл OutboxBacklogGrowing.** Выполнено владельцем 2026-11-01 на коде `05264fa14ce5`; receiver собран с этой revision без суффикса dirty. Совместная диагностика этого отказа через Loki/Tempo и итоговое здоровье также подтверждены. Остальные пять правил требуют собственных runtime-проверок.

Контракт: [alerting](../design/alerting.md). Процедура: [опыт publisher/outbox](../engineering/alerting-lab.md).

## Цель и исходные условия

Проверить настоящий Pending → Firing → активный alert в Alertmanager → firing receipt → восстановление publisher → resolved receipt того же fingerprint/startsAt и Prometheus inactive, со свежим outbox pending=0. Основной kind 1.34, прежние PostgreSQL/Kafka/monitoring/tracing, приложение с чистыми образами предыдущей проверенной ревизии. Установка alerting не требует миграций или новой сборки приложения.

## Проверки и установка

Успешно выполнены владельцем:

- `go test -count=1 -race ./cmd/alert-receiver ./cmd/labctl`: 1.481s / 1.192s;
- `go vet ./cmd/alert-receiver ./cmd/labctl`;
- `task alerting-helm-lint`: 0 failed;
- `task alerting-rules-test`: SUCCESS для шести fixtures; Docker fallback Prometheus 3.15.0-distroless, полученный digest `sha256:b2a413d5a03ea6a76782a508d1c7947440bba3b973931a25676e278431891b01`;
- `alert-receiver-build`, `alerting-render`, `observability-render`, `alerting-install`, `observability-install`.

Записанный image ref receiver: `kubernetes-application-lab-alert-receiver:5fcc8db165a9dad352be7356e5bef613cad6f6ef899f432be41072162273a8cb`. Release lab-alerting revision 1, monitoring revision 10, lab-observability revision 12. Receiver Pod `lab-alert-receiver-6b9c8bc4d4-ltzgq`. Полный Pod UID/imageID и длительный интервал ресурсов receiver в этих вложениях не зафиксированы; ref сборки не подменяет imageID среды выполнения.

Первоначальные alerts-status в 15:08:02Z, 15:08:18Z и 15:08:24Z завершились ошибкой «outbox alert rule missing, unhealthy or stale». Сохранённый API-ответ содержал только прежнее LabAPIMetricsUnavailable: новое правило отсутствовало в загруженном наборе, а не имело health=err. В 15:10:35Z status успешен: OutboxBacklogGrowing healthy/inactive, fresh pending=0, receiver пуст. Это подтверждает задержку появления правила и успешный повтор; точная стадия reconciliation/file reload отдельно не установлена. Первоначальные ошибки не исключены из истории результата.

## Нагрузка, отказ и восстановление

Наблюдатель стартовал до отказа в `15:10:52.2776478Z`, исходное правило inactive, fresh pending=0. Receiver started_at `15:07:33.792549256Z`, evicted=0. Затем применён существующий `publisher-unreachable-start`: bootstrap publisher указывает на закрытый loopback, Kafka/PVC и worker не останавливаются.

Новое задание `1aad010f-6a29-4fba-87b0-a1d5c9157f17` принято с HTTP 202 в `15:11:10.9608403Z`, created_at `15:11:10.949806Z`, trace ID `91ce141c66d01071aa604967c8c17ec8`. Во время отказа job оставался accepted, outbox содержал ожидающую строку. Kafka group была Stable, один member, lag=0: до публикации событие находится в PostgreSQL, поэтому Kafka lag не обнаруживает этот backlog.

После получения firing владелец выполнил `tracing-publisher-restore`: bootstrap согласован с Helm и сохранён диагностический tracing profile. В предоставленном выводе восстановление publisher завершилось revision 15. Outbox строка опубликована в `15:15:02.690272Z`, job succeeded в `15:15:03.707801Z`.

## Наблюдаемый цикл

| Признак | Время UTC / результат |
|---|---|
| Первый poll со свежим pending=1 | 15:11:27.3453145 |
| ActiveAt правила из Prometheus | 15:12:39.146839937 |
| Первый сохранённый Pending | 15:13:12.3458626 |
| StartsAt alert в Alertmanager/receiver | 15:13:39.146 |
| Receiver firing receipt | 15:14:09.391669669 |
| Первый poll с Firing и receipt | 15:14:12.3429541 |
| Первый poll со свежим pending=0 | 15:15:12.3400777 |
| EndsAt resolved alert | 15:15:39.146 |
| Receiver resolved receipt | 15:16:07.47609237 |
| Poll с inactive, pending=0, resolved | 15:16:10.85469 |
| alert_cycle_completed | 15:16:10.8553486 |

Fingerprint firing/resolved один: **`3ee7a538bf162672`**. Одинаковый startsAt `15:13:39.146Z`; resolved endsAt позже startsAt и раньше received_at. StartsAt webhook не подменяет ActiveAt правила: оба значения сохранены отдельно. Для firing в payload endsAt был нулевым временем; проверка завершения использует действительный endsAt resolved receipt.

Архив содержит baseline и 64 poll, всего 65 снимков. Во всех started_at receiver неизменен, evicted=0. Наблюдатель подтвердил последовательность и сохранил `alert-cycle-proof.json`; старые уведомления/ручной webhook POST не использовались. Свежий pending=0 появился до inactive/Resolved: оценка правил и доставка уведомления имеют собственные интервалы. Сам по себе ноль ещё не завершал наблюдение.

## Итоговые независимые проверки

В `15:18:06Z` alerts-status: OutboxBacklogGrowing healthy/inactive, active_fingerprints пуст, fresh pending=0, оба receipts сохранены. Все шесть новых правил и прежнее LabAPIMetricsUnavailable имеют health=ok/state=inactive. Это доказывает загрузку/успешное вычисление, но не срабатывание пяти остальных правил.

`job-status` в `15:18:07Z`: succeeded. SQL outbox-status: event ID `40b8345d-dd69-4bb4-8314-5ab4f4ddb467`, одна outbox row, **один SQL effect**, published_at установлен, attempts=8, pending=0. Attempts относится к publisher outbox и не означает восемь SQL-эффектов или worker retries. Topic lab.jobs.v1, partition 0, offset 14.

Worker group в `15:18:12Z`: Stable, один member, committed/end 15/15, 14/14, 17/17 — lag=0 по всем partition. Временные diagnostic Pod сохранены; их очистка отдельно не подтверждена. Дополнительные общие проверки после восстановления выполнены в 15:24Z и описаны ниже.

## Совместная диагностика и общее здоровье после восстановления

В 15:24:41Z logging-verify вернул 22 строки задания с одним trace ID `91ce141c66d01071aa604967c8c17ec8` и согласованным event ID. Старый publisher Pod `lab-publisher-74f7b66bbb-2rxz8` оставил семь outbox_publish_failed, новый `lab-publisher-5cd4699d79-zf8tx` — успешный claim/ack/published. Затем worker записал effect и offset commit. Строки из разных streams в выводе CLI не были глобально отсортированы; сопоставление выполняется по time/ID, а не позиции строки.

В 15:24:42Z application-trace-read успешно проверил цепочку из семи spans: HTTP → SQL enqueue → publisher attempt → Kafka publish → worker process → SQL complete/offset commit. Это число проверенных элементов цепочки, **не полное число spans**. В сохранённом ответе Tempo **41 span**: восемь publisher attempts/publish spans, семь schedule_retry и семь producer spans со статусом STATUS_CODE_ERROR, message=kafka_publish_failed; успешный producer и worker связаны с исходным HTTP-контекстом. Ошибки и успешное продолжение сохранены в одной трассировке.

На worker SQL complete заканчивается в `15:15:03.710236304Z`, offset commit начинается в `15:15:03.710319460Z` и заканчивается в `15:15:03.742777962Z`. Этот порядок проверен по interval/parent ID; независимый SQL-результат effect=1 приведён выше. По span нельзя утверждать exactly-once Kafka delivery.

prometheus-targets, application-metrics-verify и dependency-metrics-verify в 15:24:43–15:24:45Z показали по 23/23 UP targets (69 возвращённых target observations, все с пустым last_error). Прикладная health query=1, publisher published counter=1 в новом Pod, worker processed counter=2, pending=0, stats_up=1; возраст удачного SQL-снимка 10.7812955379s. Счётчики относятся к текущим процессам, не общему числу исторических заданий. Отдельный range-query роста publisher publish_errors старого Pod во время отказа в этой серии выводов не представлен; сами ошибки подтверждены журналами и spans. Dependency health=1, все 11 PostgreSQL collector samples=1, три lag=0, один group member. Это реальные samples, не подстановка отсутствующих рядов нулями.

Smoke прошёл: API revision `0b77cd84de5e`, EndpointSlice `10.244.0.25:8080`, клиентский Pod Succeeded и удалён. Эта проверка не добавляет нового job и не подменяет исходное задание.

- Журналы, проверенная цепочка и итоговые samples (private original artifact omitted; see provenance).
- Полный ответ Tempo для отказа и восстановления (private original artifact omitted; see provenance). Проверены имена атрибутов: идентификаторы, ограниченные HTTP/SQL/messaging metadata и error.type; payload/SQL-текст/credentials не копируются.

## Архив и границы

Проекция полных сохранённых poll и proof (private original artifact omitted; see provenance) содержит состояния правила, время оценки, fresh outbox query, соответствующие Alertmanager alerts/receiver receipts и SHA-256 четырёх исходных вложений. Полные API-ответы остаются в `.local-observability/20261101T151052.026350900Z-alerts-watch`; финальный status — `20261101T151806.187729900Z-alerts-status`. При проекции не включены Secret, исходные webhook bodies или Kafka payload.

Подтверждены приём HTTP-задания в outbox во время отказа publisher, свежие данные backlog, реальный Pending/Firing, доставка во внутренний receiver, Resolved того же alert, явное восстановление, SQL effect=1 и lag=0. Приложение продолжало основной путь независимо от receiver. Отключение receiver для доказательства этой независимости не выполнялось.

Совместная диагностика ошибки и восстановления через метрики, Loki и Tempo подтверждена в границах этих samples и одной трассировки. Просмотр этой трассировки в Grafana для данного опыта отдельно не предоставлен; чтение выполнено CLI через Tempo API. Этот опыт не доказывает runtime-срабатывание остальных пяти правил, exactly-once/долговечную доставку уведомлений, длительную устойчивость receiver, HA, enforcement NetworkPolicy, полный аудит секретов или завершение Milestone 8. Внешние каналы не настраивались.
