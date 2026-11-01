> Historical result from the private development history. Original identifiers are preserved; this is not a rerun of the public snapshot. Reviewed screenshots are included; raw captures remain private. See [provenance](../provenance.md). Local paths, where present, were redacted.

# Kafka: недоступность единственного брокера и восстановление

- Статус: `Passed` для описанного управляемого отказа и восстановления; причина задержки Operator не установлена.
- Выполнение: 25 октября 2026 года, время ниже в UTC; вывод команд предоставил владелец стенда.
- Ревизия начала сценария: `8fe69a129fec10fd20596e1ffded89177a4c28bc`, исходное рабочее дерево чистое. После прерванного restore добавлен вывод прогресса ожидания в CLI; приложения не пересобирались. Чистая ревизия CLI для всей последовательности восстановления отдельно не зафиксирована.
- Среда для выполнения: основной kind, Kubernetes 1.34, Strimzi 1.3.0/Kafka 4.2.2 с одной совмещённой broker/controller-нодой, существующий PostgreSQL и jobs-профиль приложения.
- Контракт: [Kafka и обработка заданий](../design/kafka-event-processing.md).
- Воспроизведение: [порядок проверки](../engineering/kafka-broker-outage-lab.md).

## Цель

Проверить приём заявки в PostgreSQL при фактическом отсутствии broker/controller Pod, накопление pending outbox и восстановление публикации/обработки той же заявки после возврата брокера. Отдельно прочитать marker, подтверждённый до остановки, и сравнить идентификаторы хранилища.

## Исходные условия

Успешные проверки CLI, чистая ревизия сценария, работающие Operator/broker/API/publisher/worker/PostgreSQL, Bound PVC, Stable consumer group и исходные offsets. Новый smoke marker публикуется до отказа; опыт ограничен его часовой retention. Прежние Passed не являются выполнением этого опыта.

## Способ создать отказ

Сохранить recovery metadata в исключённый из Git локальный файл до мутаций. Временно уменьшить Deployment Cluster Operator до нуля и дождаться удаления его Pod, затем удалить только единственный broker Pod с обычным grace. StrimziPodSet и Kafka CR не редактировать, PVC и offsets не удалять. До явного восстановления Operator не создаёт замену, что позволяет наблюдать фактическую недоступность единственного брокера. Это управляемая остановка, не SIGKILL или потеря узла.

## Ожидаемые признаки

Broker Pod отсутствует, storage Bound. API livez/readyz возвращают 200, enqueue возвращает 202, SQL сохраняет accepted и pending outbox без эффекта. Publisher фиксирует транспортную ошибку и сохраняет очередную попытку; worker не создаёт эффект и не подтверждает новую запись. После restore новый Ready broker получает прежние PVC/PV/node ID, smoke-read читает старый marker. Та же заявка доставляется и завершается с одним эффектом без ручного рестарта процессов приложения. Финальная доступность и очистка подтверждаются отдельно.

## Наблюдавшиеся признаки

Перед опытом владелец выполнил `go test -race ./cmd/labctl` (успех, 2.818 s) и `go vet ./cmd/labctl` (без ошибок). Использованы kubectl 1.35.9 и сервер 1.34.0. Попытка с kubectl 1.29 была отклонена проверкой совместимости до изменений.

Работающие образы:

| Процесс | Образ | Ревизия сборки |
|---|---|---|
| API | `kubernetes-application-lab-api:e157164760349963bf720248406ffa7460da5da08d14c8e6a4a9fd87f0dda598` | `011b2e8c144c` |
| Publisher | `kubernetes-application-lab-publisher:0fcdb84f54df4e30ccb9c3e50887a065cf13b750625be1c81e9bdcf510bf7c48` | `011b2e8c144c` |
| Worker | `kubernetes-application-lab-worker:8d1e8c624ebf85d1796525326f135d33b74e91f2bbe0a8c1c9997a87ef21dd67` | `de6f15ba5c4d` |

До отказа группа `lab-workers` Stable, offsets по partitions 0/1/2 равны 7/2/2, lag=0, pending=0. В 23:38:37 подтверждена запись marker `smoke-1792971513667895900` в `lab.smoke.v1`, partition 0, offset 1.

В 23:41:25 команда start подтвердила отсутствие broker Pod и ноль реплик Operator. PVC остался Bound, bootstrap EndpointSlice не содержал endpoints. API livez/readyz вернули 200. В 23:41:35 enqueue вернул 202 для задания `725fded8-e120-4177-a8d8-fc3804fafd21`, ключ `outbox-1792971695548585900`, событие `22ca5309-bc49-41c5-bbb9-25ee08feb38c`. Исходное состояние: accepted, outbox_rows=1, effects=0, published_at и Kafka coordinates отсутствуют. Затем pending=1, attempts росло, error_code=`kafka_publish_failed`.

Publisher зафиксировал десять ошибок публикации. Задержки повторов росли от 1 до 30 секунд; длительность самого запроса добавлялась к задержке. На попытке 11 в 23:50:16 получен `outbox_broker_ack` для partition 0, offset 7, затем `outbox_published`.

В 23:51:00 worker вновь получил partitions 0/1/2 и исходное событие. В 23:51:01.794 зафиксирован `worker_effect_committed`, duplicate=false; в 23:51:01.820 — `worker_offset_committed`, committed_offset=8. SQL показал succeeded, result с outcome=completed, outbox_rows=1, effects=1, pending=0. Всего шесть опубликованных outbox-записей; два failed-задания и восемь DLQ-квитанций относятся к прежним опытам.

В 23:52:22 `kafka-smoke-read` прочитал прежний marker по тем же topic/partition/offset. В 23:53:38 группа Stable с одним прежним worker, offsets 8/2/2, lag=0 на всех partitions. Publisher и worker вернулись в Ready на прежних Pod с нулём перезапусков. PostgreSQL также сохранил Pod, ноль перезапусков и прежний PV. Финальный `task smoke` прошёл: API revision=011b2e8c144c, endpoint 10.244.0.9.

После сохранения журналов выполнены `task kafka-smoke-cleanup` и `task worker-diagnostics-cleanup`: удалены три smoke Pod (включая прерванный probe) и два worker diagnostic Pod. Данные приложения, topics и PVC не удалялись.

## Диагностика

Первый restore вернул Operator к одной реплике, но ожидание брокера было прервано Ctrl+C. Последующий smoke-read тоже прерван: эти вызовы не считаются успешными проверками. Recovery snapshot сохранился. Operator `strimzi-cluster-operator-799955f45b-v7zgl` стал лидером в 23:44:39; информеры синхронизировались в 23:44:48. Далее журнал показывал `Reconciliation is in progress`, broker Pod ещё отсутствовал. Старые Ready в Kafka/StrimziPodSet не отражали фактическую доступность.

Повторный restore завершился успешно в 23:52:02 без ручного перезапуска Operator или создания broker Pod. По финальному снимку broker Pod был создан примерно в 23:49:26; endpoint стал ready в 23:50:22. Ack publisher в 23:50:16 получен раньше readiness endpoint: это разные сигналы. Причина задержки создания Pod не установлена. Гипотеза о повторной синхронизации информеров не считается доказанным объяснением. У старого Operator был один перезапуск ещё до опыта; он не приписывается созданному отказу.

Локальные журналы: `.local-kafka/broker-restored-publisher.log`, `.local-kafka/broker-restored-worker.log`; recovery metadata: `.local-kafka/broker-outage-restored-1792972322592989400.json`. Эти файлы исключены из Git; существенные результаты перенесены сюда из предоставленных выводов.

## Причина

Одна broker/controller-нода не обеспечивает доступность при остановке её процесса. Путь HTTP-приёма зависит от PostgreSQL, а не Kafka. Сохранённый outbox должен пережить отказ транспорта и позволить публикацию после его возврата. Приём 202 не означает доставку или завершение работы.

## Восстановление

Restore вернул Operator к исходной одной реплике и подтвердил замену broker при сохранённом хранилище:

| Объект | Сохранённый идентификатор |
|---|---|
| Operator Deployment UID | `8b908b4b-8a46-46c6-94cd-e223b412b433` |
| Kafka UID | `9040622e-dc7c-4aa5-b742-c039da34d9b3` |
| KafkaNodePool UID | `94dc6831-985e-442a-b351-8228add8dfcd` |
| StrimziPodSet UID | `9766eb1f-d928-46e3-b100-6f960f1716f9` |
| PVC `data-0-lab-kafka-dual-role-0` UID | `f2a16b76-7e9b-47fd-8afb-419f2bc9a8f6` |
| PV | `pvc-f2a16b76-7e9b-47fd-8afb-419f2bc9a8f6` |
| Node ID | `0` |

Broker UID изменился с `bc9dde3c-3095-4a2f-ace0-85d1ec693782` на `42504991-477b-47c4-aab5-83b4fdfa2b02`, новый Pod Ready 1/1. Snapshot архивирован после проверки; marker, исходное задание, offsets и очистка подтверждены отдельно.

## Поведение Kubernetes

Deployment удалил и восстановил Pod Operator при масштабировании 1 → 0 → 1. После возвращения Operator Strimzi создал замену broker Pod через StrimziPodSet controller. PVC оставался Bound; чтение исходного marker отдельно подтвердило сохранность выбранной записи.

## Поведение приложения

HTTP-приём сохранил заявку в PostgreSQL при отсутствующем брокере. Publisher повторял публикацию с ограниченной задержкой, worker восстановил сессию. На прежних Pod publisher/worker без перезапусков исходное задание завершилось с одним эффектом; SQL-эффект предшествовал source offset commit.

## Что результат подтверждает

Описанное локальное восстановление после длительной управляемой недоступности единственного broker/controller при сохранённом диске и живом Kubernetes/PostgreSQL.

## Что результат не подтверждает

HA, потерю узла/диска, внезапную остановку процесса, любой сетевой partition, exactly-once delivery, аварии между всеми подтверждениями SQL/Kafka, CronJob или весь Milestone 7. Остановка Operator также приостанавливает его reconciliation: это часть условий опыта, а не модель независимого отказа только data plane.

## Остаточный риск

Восстановление зависит от Kubernetes API, Operator и сохранённого диска. Причина наблюдавшейся задержки Operator не установлена; быстрый или ограниченный пятью минутами возврат брокера этим опытом не гарантируется. Первый restore и smoke-read были прерваны, затем восстановление завершено повторным вызовом. Проверки Go после добавления вывода прогресса отдельно не предоставлены. Сохранность доказана для выбранного marker и наблюдавшихся offsets, а один эффект не означает exactly-once delivery.
