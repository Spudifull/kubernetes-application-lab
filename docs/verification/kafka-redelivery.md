> Historical result from the private development history. Original identifiers are preserved; this is not a rerun of the public snapshot. Reviewed screenshots are included; raw captures remain private. See [provenance](../provenance.md). Local paths, where present, were redacted.

# Kafka worker: аварийная повторная доставка

- Статус: `Passed` в границах одного локального аварийного опыта; восстановление, финальная проверка доступности и очистка подтверждены владельцем 25 октября 2026 года.
- Ревизия: `011b2e8c144cd36ab379457f14c2fcb59301d33e`; worker build date `2026-10-25T21:33:09.840053000+03:00`. Build revision не содержит суффикса `-dirty`; отдельный вывод status перед сборкой не предоставлен.
- Worker: `kubernetes-application-lab-worker:669cf5026ba3768b8eab2143a338b27410d610bdfbe69fabd85ddf2875c79ae7`.
- Среда: новый основной kind на другом компьютере, Windows PowerShell, Kubernetes 1.34.0, kubectl 1.35.9, Helm 4.3.1, Kafka 4.2.2/Strimzi 1.3.0, PostgreSQL 16.14. Времена ниже — UTC.
- Проверяемый контракт: [Kafka и обработка заданий](../design/kafka-event-processing.md).
- Воспроизведение: [порядок проверки аварии](../engineering/kafka-redelivery-lab.md).

## Цель

Проверить повторную доставку исходной Kafka-записи после аварии процесса между сохранением SQL-эффекта и подтверждением offset, без повторного SQL-эффекта.

## Исходные условия

На новом компьютере созданы кластер и ресурсы приложения; старые данные не переносились. API собран из `011b2e8c144c`, образ `kubernetes-application-lab-api:e157164760349963bf720248406ffa7460da5da08d14c8e6a4a9fd87f0dda598`; publisher — `kubernetes-application-lab-publisher:0fcdb84f54df4e30ccb9c3e50887a065cf13b750625be1c81e9bdcf510bf7c48` той же build revision. Jobs-профиль API установлен после успешного Job миграции `lab-db-migration-20261025181642`. PostgreSQL PVC Bound, PV `pvc-dd7b8153-83f3-4322-8561-145c9fbbb29a`. Kafka PVC Bound, PV `pvc-f2a16b76-7e9b-47fd-8afb-419f2bc9a8f6`. Kafka smoke подтвердил запись и чтение partition 0/offset 0 отдельного `lab.smoke.v1`.

Владелец предоставил успешные `go vet ./...`, lint/render обычного и аварийного chart. Полных выводов `go test ./...` и `go test -race ./...` для этой ревизии в предоставленных материалах нет. Изолированный `task worker-sql-integration-test` выполнил `TestPostgresWorker` с race: PASS за 0.30s, пакет за 2.528s. БД `lab_jobs_test_40ac05e56fc54cc4a6147b1fc0f2c2b8` после опыта отсутствует: финальный SELECT из pg_database вернул `(0 rows)`.

После остановки worker создано задание `c8969b23-4b77-4991-a8f8-0ef29a8d09df`, ключ `outbox-1792953647393171900`, event `7f5fab34-0538-4737-90e9-a2dff3dd0c1a`. Оно опубликовано в `lab.jobs.v1`, partition 2, offset 0: published_at `18:40:47.550978`, attempts=1, одна outbox-строка, effects=0. Группа Empty, members=0, committed offsets отсутствуют, log end 0/0/1.

## Способ создать нагрузку или отказ

`worker-deploy-crash` выбирает job ID из locator и включает аварийное завершение с кодом 86 в точке вызова CommitRecords, после успешного SQL-результата. Для target повторная доставка тоже завершается до commit, пока обычное развёртывание не снимет отказ. Штатного закрытия Kafka-клиента и выхода из группы при аварии нет.

## Ожидаемые признаки

- SQL: succeeded, один эффект, result и completed_at сохранены до восстановления.
- Kafka: исходный offset ещё не подтверждён, аварийный журнал содержит его точные координаты.
- Kubernetes: перезапуск контейнера после exit 86; возможен CrashLoopBackOff.
- После снятия отказа: та же запись распознана как дубликат и подтверждена, SQL-результат не изменился.

## Наблюдавшиеся признаки

Аварийный Pod `lab-worker-7db9677685-kwffn`, UID `d3cbf6b7-13ba-4154-b981-ea8dc5696172`, на указанном worker image сохранил:

| Событие | Время UTC |
|---|---|
| worker_received, partition 2/offset 0 | 18:42:02.635837967 |
| worker_work_started | 18:42:02.655070616 |
| worker_effect_committed, duplicate=false | 18:42:03.661619516 |
| worker_crash_before_offset_commit, exit_code=86 | 18:42:03.661763164 |

Pod lastState подтверждает exitCode=86, завершение `18:42:03`, restartCount=1 в сохранённом снимке. HTTP и SQL до восстановления: succeeded, effects=1, completed_at=`2026-10-25T18:42:03.65966Z`, result=`{"outcome":"completed","duration_ms":1000}`.

После остановки Helm revision 4 независимый снимок группы в `18:45:51` показал committed_offset=-1 и log_end_offset=1 для partition 2. Группа ещё Stable с одним прежним member; это снимок координатора после аварийных перезапусков, не доказательство живого Pod или группы Empty. Отсутствующий offset не трактуется как нулевой lag.

Helm revision 5 вернула обычный worker Pod `lab-worker-7759d7df7-7f7l2`, 1/1 Ready, restarts=0, на том же образе. Его журнал:

| Событие | Время UTC |
|---|---|
| worker_received, тот же event, partition 2/offset 0 | 18:47:00.319987122 |
| worker_duplicate_ignored | 18:47:00.335323946 |
| worker_offset_committed, committed_offset=1 | 18:47:00.36563849 |

HTTP и SQL после восстановления сохранили тот же completed_at, result, effects=1, outbox_rows=1 и исходные publisher partition/offset, attempts=1. В `18:47:35` группа Stable с одним member, назначение [0,1,2]. Для partition 2 committed_offset=1, log_end_offset=1, lag=0. Пустые partitions 0/1 сохранили committed_offset=-1, log_end_offset=0 и lag=null; утверждение о нулевом измеренном lag всех partitions не делается.

## Диагностика

Первый worker-group-status во время аварийных перезапусков получил PreparingRebalance с двумя members и завершился `invalid group assignment`; ожидание Succeeded диагностического Pod затем истекло. Код диагностики безусловно разбирает MemberAssignment и не обрабатывает отсутствие назначения в переходном состоянии. Этот отказ не принят за снимок offsets. После остановки перезапусков новый диагностический Pod успешно получил offsets до восстановления; исходный отказ сохранён.

Доказательства предоставлены владельцем в этой беседе: подготовка — текст от 18:40, авария — вложение `f5e7ae11-5dca-437c-89c0-62d05a551176`, восстановление — `68895538-5e24-4a51-b6bd-c40e53404b9f`, SQL-тест — `68bd9088-3a92-427a-bcd2-977163e5963f`. Локальные файлы `.local-kafka/redelivery-*` не входят в Git; существенные значения перенесены сюда. Команды кластера и тесты выполнял владелец, агент анализировал вывод.

## Причина

Запланированный отказ расположен между двумя независимыми подтверждениями: PostgreSQL уже сохранил эффект, Kafka ещё не получила commit. Это ожидаемое окно повторной доставки, а не распределённая транзакция.

## Восстановление

worker-stop и worker-deploy сняли отказ через Helm; одна реплика Ready, повторная доставка завершилась commit. Финальный вывод владельца во вложении `a437d5e4-4114-44bd-a094-ae99cb0f9ada` подтверждает publisher 1/1 без перезапусков, PostgreSQL 1/1 без перезапусков, PVC Bound с прежним PV `pvc-dd7b8153-83f3-4322-8561-145c9fbbb29a`. `task smoke` завершил внутрикластерный клиент Succeeded, вернул API revision `011b2e8c144c` через DNS Service и EndpointSlice `10.244.0.9`, затем удалил smoke Pod.

`task worker-diagnostics-cleanup` удалил пять диагностических Pod, включая неуспешный `lab-worker-diagnostic-1792953738418417900`. `task kafka-smoke-cleanup` удалил `lab-kafka-smoke-1792953060658840100`. SELECT из pg_database подтвердил отсутствие тестовой БД. Jobs/outbox, offsets, topics, Secret и PVC этими командами не удаляются. Отдельная повторная проверка Kafka PV после финальной очистки не предоставлена; этот результат не доказывает свойства хранения при отказе диска.

## Поведение Kubernetes

Kubernetes перезапустил контейнер после exit 86 и создал новый Pod при восстановлении через Helm. Он не подтверждал сообщения и не обеспечивал дедупликацию SQL-эффекта.

## Поведение приложения

Восстановленный процесс прочитал исходную запись, распознал сохранённый эффект и подтвердил offset. SQL-результат и время завершения не изменились; второй SQL-эффект не возник.

## Что результат подтверждает

Один локальный опыт аварийной повторной доставки той же записи с идемпотентным SQL-эффектом в выбранной точке отказа, восстановление worker, доступность API/publisher/PostgreSQL и завершение диагностической очистки.

## Что результат не подтверждает

Exactly-once delivery, конечные retry/DLQ, реальный отказ брокера, потерю диска, аварию в каждой возможной точке SQL/Kafka или весь Milestone 7.

## Остаточный риск

После этого опыта исправлена диагностика в ревизии `afe6fc07647873ea1c9455ac28438d33fbb3ffa4`: отсутствующее назначение во время PreparingRebalance/CompletingRebalance отражается как неизвестное, а offsets читаются дальше. Владелец предоставил успешные `go test ./...`, `go test -race ./...`, `go vet ./...` перед commit; cmd/worker прошёл за 1.687s и 2.013s соответственно, часть других пакетов использовала cache. Регрессионный тест покрывает переходные состояния, Stable и повреждённые данные. Эти проверки относятся к последующему исправлению и не являются отдельным запуском тестов исторической ревизии аварийного опыта.

Вложение `ddb4509f-ea97-497d-ba09-f45e52403d55` подтвердило сборку worker с build revision `afe6fc076478`, build date `2026-10-25T22:11:15.645386100+03:00`, образом `kubernetes-application-lab-worker:be7d9e2c768a8550f59856c33b5b2bf782aac26f7d4207f8b346d2c4949d3b4c`. Helm revisions 6 → 7 → 8 выполнили переход 1 → 2 → 1. В снимке 19:12:48 UTC Pod `lab-worker-7d76f6d487-rfvtz` получил [0,1], `lab-worker-7d76f6d487-zl7vw` — [2]; повтор 19:14:39 сохранил назначения. Финальный снимок 19:15:09 показал одного member с [0,1,2]. Все четыре диагностических запуска успешны, состояние во всех снимках Stable, assignment_pending=false. Offset partition 2 остался 1, log end 1, lag 0; пустые partitions 0/1 сохранили offset=-1, lag=null. Переходное состояние в runtime не попало в снимки: ветвь assignment_pending=true подтверждена unit-тестом, но пока не наблюдалась в кластере. Очистка четырёх новых диагностических Pod ещё не подтверждена.

Владелец сообщил об очистке четырёх новых диагностических Pod после проверки afe6fc076478; отдельный вывод удаления не предоставлен.

В старом образе диагностика назначения неустойчива во время rebalance; в новом образе успешны стабильные снимки, переходная ветвь пока проверена только unit-тестом. Retention ограничивает окно восстановления. Этот опыт не проверяет крайние точки SQL commit, потерю тома или нагрузку на зависимости. Passed относится к описанному аварийному runtime-сценарию; последующие Go-проверки исправления указаны отдельно.
