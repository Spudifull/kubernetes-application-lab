> Historical result from the private development history. Original identifiers are preserved; this is not a rerun of the public snapshot. Reviewed screenshots are included; raw captures remain private. See [provenance](../provenance.md). Local paths, where present, were redacted.

# Kafka worker: конечные повторы и DLQ

- Статус: `Passed` в границах описанных локальных сценариев, включая финальную регрессию и очистку.
- Выполнение: 25–26 октября 2026 года по Europe/Minsk; времена событий ниже приведены в UTC.
- Ревизия: `de6f15ba5c4db87dac014c60c2422286353dddd5`.
- Worker: `kubernetes-application-lab-worker:8d1e8c624ebf85d1796525326f135d33b74e91f2bbe0a8c1c9997a87ef21dd67`, build date `2026-10-25T22:36:26.063044100+03:00`.
- Среда: основной kind, Windows PowerShell, Kubernetes 1.34.0, kubectl 1.35.9, Helm 4.3.1, PostgreSQL 16.14, Kafka 4.2.2/Strimzi 1.3.0.
- Контракт: [Kafka и обработка заданий](../design/kafka-event-processing.md).
- Воспроизведение: [порядок проверки retry/DLQ](../engineering/kafka-retry-dlq-lab.md).

## Цель

Проверить сохранение контролируемых ошибок и срока следующего повтора в PostgreSQL, успех третьей попытки после двух ошибок, конечный отказ после трёх ошибок, доставку в DLQ до source commit и отсутствие commit при недоступном DLQ producer. Для некорректных событий проверить DLQ без строки задания и продолжение обработки источника.

## Исходные условия

Требуются новые Go-проверки, lint/render worker chart, реальный изолированный SQL integration-тест, миграция 003, отдельный DLQ inspector с Read/Describe только на DLQ, новые worker и migration-образы из чистой ревизии. Данные прежнего аварийного опыта сохраняются, source offsets не сбрасываются. Исправление диагностики на afe6fc076478 и старый аварийный Passed не доказывают новое поведение.

## Способ создать нагрузку или отказ

На новой опубликованной заявке при остановленном worker задать две либо три управляемые ошибки диагностическим Pod. Для отказа DLQ изменить только bootstrap отдельного producer на закрытый loopback порт. Отдельным Pod с publisher identity отправить пять фиксированных некорректных записей. Инспектор с отдельным удостоверением читает одну DLQ-запись по подтверждённым координатам.

## Ожидаемые признаки

- Две ошибки → succeeded, один эффект, failure_count=2, без DLQ.
- Три ошибки → DLQ broker ack → SQL failed с квитанцией → source commit, effects=0.
- Недоступный DLQ → pending квитанция, нет terminal state и source commit; после восстановления тот же source отправлен без новых рабочих попыток.
- Некорректный source → квитанция без job_id, DLQ содержит безопасные метаданные и хеши, source commit идёт после квитанции.
- Обычная финальная заявка успешно обрабатывается; worker/API/publisher/PostgreSQL готовы, диагностика очищена, данные сохранены.

## Наблюдавшиеся признаки

Владелец выполнил go test ./..., go test -race ./..., go vet ./... и lint/render worker chart, успешные выводы предоставлены во вложении `d96c60d1-bcbe-4a60-b36c-f8415c565f28`. SQL-тесты с race дали PASS: TestPostgresWorker 0.41s, TestPostgresWorkerReliability 0.39s, пакет 3.464s. БД `lab_jobs_test_b63e406a2d8e40e38fe7ebd57afec9c2` удалена, SELECT подтвердил `(0 rows)`.

Образ миграции `kubernetes-application-lab-api:af56d8bc4a234a4ad181533bd1c250b6b9ed420875dd237bff8ef60e3824b66f` собран на той же ревизии. Job `lab-db-migration-20261025193619` применил 003_worker_reliability; старые заявки сохранены. Новый worker стал 1/1 Ready. Первая попытка Kafka bootstrap остановилась на client 1.29/server 1.34; Secret инспектора отсутствовал. После выбора локального kubectl 1.35.9 bootstrap завершён, Secret создан, повтор подтвердил совпадение. Эти исходные ошибки не относятся к обработке заданий.

### Две ошибки и успех третьей попытки — выполнено

Задание `663d085e-c1e6-4e78-bd89-5a7c9b8e8b7e`, ключ `outbox-1792957296309130600`, event `376dc276-bba5-4b8d-8e84-1295291147da`, source `lab.jobs.v1`, partition 0, offset 0. Перед запуском effects=0, одна outbox-строка, published_at=`2026-10-25T19:41:37.538111Z`; настройка двух ошибок подтверждена отдельным диагностическим Pod.

Worker Pod `lab-worker-69b8fd7494-fjvkm`, Helm revision 12. Все времена UTC 25 октября 2026 года:

| Событие | Время |
|---|---|
| первая рабочая попытка | 19:41:59.103509821 |
| worker_retry_scheduled, failure_number=1 | 19:42:00.109777990 |
| вторая рабочая попытка | 19:42:10.212326232 |
| worker_retry_scheduled, failure_number=2 | 19:42:11.216905452 |
| третья рабочая попытка | 19:42:21.340552930 |
| worker_effect_committed, duplicate=false | 19:42:22.352582143 |
| worker_offset_committed, committed_offset=1 | 19:42:22.365027555 |

Все три чтения имеют тот же source offset и event ID. Между попытками пересозданы consumer-сессии; длительность всего повтора больше минимального SQL backoff. Первый HTTP/SQL-снимок был промежуточным: processing, failure_count=1, effects=0. Повторный снимок подтвердил succeeded, failures_before_success=2, failure_count=2, completed_at=`2026-10-25T19:42:22.347707Z`, result outcome=completed/duration_ms=1000, один эффект, одна outbox-строка, исходные publisher partition/offset. Таблица DLQ пуста. Итоговая группа Stable с одним member и [0,1,2], partition 0 committed=1/end=1/lag=0. Старый аварийный job сохранил свой результат и эффект.

В промежуточном снимке `2026-10-25T19:42:14Z` диагностика поймала PreparingRebalance с assignment=null/assignment_pending=true и успешно продолжила чтение offsets всех трёх partitions. Это runtime-наблюдение ранее непроверенной переходной ветви исправления диагностики. Доказательства: исходный снимок `a62ed9af-742e-4080-a539-467fafc26a57`, завершение `b7fc7073-7b92-4d87-b62c-8e8fde9881ad`.

### Три ошибки и DLQ — выполнено

Задание `449b2af7-b1df-4af0-b386-c9c89e4c2d35`, ключ `outbox-1792957494370887600`, event `5c71c37a-96ee-4ccb-ae8c-1863ad77bbad`, source `lab.jobs.v1`, partition 1, offset 0. До запуска: published, effects=0, outbox_rows=1. Настройка трёх ошибок подтверждена отдельным Pod. Worker `lab-worker-69b8fd7494-8txjl`, Helm revision 14, та же ревизия и образ.

| Событие | Время UTC |
|---|---|
| failure_number=1 | 19:45:25.467921147 |
| failure_number=2 | 19:45:36.576325597 |
| failure_number=3 | 19:45:47.686697462 |
| worker_dlq_broker_ack, DLQ partition 1/offset 0 | 19:45:57.872495118 |
| worker_dlq_recorded | 19:45:57.878962407 |
| worker_offset_committed, source committed_offset=1 | 19:45:57.893391288 |

После третьей ошибки исходная запись прочитана ещё раз для ветви DLQ, без четвёртого worker_work_started. SQL: failed, failure_count=3, effects=0, completed_at=`2026-10-25T19:45:57.876053Z`, error_code=retry_exhausted. Квитанция source 1/0 имеет published_at=`19:45:57.877023Z`, DLQ 1/0. GET вернул тот же конечный статус. Инспектор в 19:46:05.758976319 прочитал запись `lab.jobs.dlq.v1`, partition 1, offset 0 и сверил envelope с SQL-квитанцией: failures=3, reason=retry_exhausted, правильные job/event/source IDs, value_bytes=228 и SHA-256. Исходное body в envelope не копируется.

В 19:46:54 группа Stable с одним member и [0,1,2], committed/end offsets всех трёх partitions равны 1/1, lag=0. Ранние снимки processing/failure_count=2 и PreparingRebalance были сделаны до завершения и не приняты за конечный результат. Доказательства: `686df645-e16b-4d36-a381-bcd81da20e67` и `a92d86e0-7df2-4d59-9880-c092cee816fe`.

### Недоступный DLQ producer и восстановление — выполнено

Задание `209ca734-ba94-4047-a2af-30714abdc5b5`, ключ `outbox-1792957677910304200`, event `dab33ea5-8db4-4ebb-82d6-55379efc2d23`, source `lab.jobs.v1`, partition 1, offset 1. До запуска published, effects=0. Helm revision 16 запустила Pod `lab-worker-6f44ffd566-txlgr` с закрытым loopback адресом только для DLQ producer. Три ошибки сохранены в 19:48:27.008963605, 19:48:33.115238144 и 19:48:39.234910386 UTC. Первый worker_dlq_publish_failed получен в 19:48:47.359475973; последующие ошибки в 19:48:55, 19:49:03 и 19:49:11 не добавили рабочих попыток.

SQL до восстановления: processing, failure_count=3, effects=0, completed_at отсутствует; pending квитанция source 1/1 без published_at и DLQ-координат. Групповой снимок 19:49:27: partition 1 committed=1/end=2/lag=1. Следовательно запись offset 1 оставалась неподтверждённой при отказе публикации. Остальные partitions имели lag=0.

Helm revisions 17 → 18 выполнили stop и обычное развёртывание. Pod `lab-worker-69b8fd7494-4m76l` на прежнем образе получил ту же запись в 19:50:58.178990423; новых worker_work_started для неё в журнале восстановленного процесса нет. DLQ ack 19:50:58.258274744, worker_dlq_recorded 19:50:58.262734197, source commit=2 в 19:50:58.265411920. SQL: failed/retry_exhausted, failure_count=3, effects=0, completed_at=`2026-10-25T19:50:58.260716Z`, квитанция published_at=`19:50:58.261286Z`, DLQ partition 1/offset 1. Инспектор в 19:52:25.647919185 прочитал именно эту DLQ-запись, сверив исходные job/event/source IDs и failures=3.

Финальный снимок 19:52:34: группа Stable, один member с [0,1,2], committed/end offsets 1/2/1, lag=0 во всех partitions. Это восстановление отдельного DLQ producer после неверного адреса, не опыт отказа самого брокера. Доказательства: исходное состояние `7583f635-005d-41df-8ab1-28f75238a825`, восстановление `4c488772-f147-40c4-b813-b460ca5432d4`.

### Некорректный JSON без задания — выполнено

Режим invalid-json опубликовал одну запись размером 1 байт в source partition 0/offset 1 в `2026-10-25T23:09:34.030577259Z` (26 октября 02:09:34 Europe/Minsk). Прежний восстановленный Pod `lab-worker-69b8fd7494-4m76l` на de6f15ba5c4d записал worker_event_rejected в 23:09:34.031800953, DLQ ack для partition 2/offset 0 в 23:09:34.053274328, worker_dlq_recorded в 23:09:34.056496163 и source commit=2 в 23:09:34.059831500 UTC.

SQL-квитанция source 0/1 имеет reason=invalid_event, job_id отсутствует, published_at=`2026-10-25T23:09:34.054478Z`. Инспектор в 23:10:03.604029083 прочитал DLQ partition 2/offset 0 с теми же source coordinates, failures=0, value_bytes=1 и SHA-256; job_id/event_id и исходное body в envelope отсутствуют. Группа в 23:10:10 Stable, один member, committed/end offsets 2/2/1 и lag=0 во всех partitions.

Outbox/HTTP-задания не создавались: SQL по-прежнему показывает четыре задания, два failed и два succeeded, прежние результаты и эффекты сохранены. Доказательство этой ветви — вложение `13f198d7-5b4c-4d19-8230-874878669468`. Второе вложение того же сообщения `658e170f-cd28-47cf-b54a-2157c69ef503` повторяет прежний опыт исчерпания ошибок и не считается новым опытом.

### Неизвестная версия события — выполнено

Режим unknown-version опубликовал 206 байт в source partition 0/offset 2 в `2026-10-25T23:13:24.899973618Z`. SQL-квитанция без job_id имеет reason=invalid_event, published_at=`23:13:24.937453Z`, DLQ partition 1/offset 2. Инспектор в 23:13:36.100510273 прочитал именно эту запись и сверил source 0/2, value_bytes=206, failures=0 и SHA-256. В 23:13:48 группа Stable, один member с [0,1,2], source partition 0 committed=end=3, lag=0 во всех partitions. Счётчики прежних управляемых ошибок и эффекты сохранены. В этом первом вложении журнала worker ещё нет; последующий полный журнал, описанный ниже, подтвердил порядок внутренних событий этой ветви. Доказательство — `a55645fa-4744-4d77-a08e-76bdbada4fe9`.

### Остальные некорректные события — выполнено

Вложение `e74a380c-b651-48a4-aff9-b0c5ac3bb641` подтвердило следующие публикации и прямое чтение соответствующих DLQ-записей (время UTC 25 октября, локально уже 26 октября):

| Режим | Публикация UTC | Байты | Source partition/offset | DLQ partition/offset | Reason | Committed source offset |
|---|---|---|---|---|---|---|
| unknown-type | 23:15:16.794555726 | 207 | 0/3 | 2/1 | invalid_event | 4 |
| missing-job | 23:15:58.409954405 | 206 | 0/4 | 1/3 | inconsistent_event | 5 |
| oversized | 23:16:22.992573379 | 4097 | 0/5 | 1/4 | invalid_event | 6 |
| повторный oversized | 23:17:00.388059449 | 4097 | 0/6 | 2/2 | invalid_event | 7 |

Во всех четырёх квитанциях job_id отсутствует, инспектор сверил именно исходные координаты, размеры, reason и хеши. Повторный oversized был новой намеренной публикацией с новым offset, а не повторной доставкой прежней записи; одинаковый body hash не означает один Kafka record.

Полный журнал worker из того же вложения также закрыл ранее отсутствовавший журнал unknown-version. Для source 0/2 ack → recorded → commit идут в 23:13:24.935737356 → 23:13:24.939840774 → 23:13:24.943876882. Для source 0/3: 23:15:16.827787228 → 23:15:16.885819397 → 23:15:16.889770605; для 0/4: 23:15:58.440921543 → 23:15:58.444217045 → 23:15:58.447043866; для 0/5: 23:16:22.999848413 → 23:16:23.003455518 → 23:16:23.006674542; для 0/6: 23:17:00.410467672 → 23:17:00.413797478 → 23:17:00.416247680 UTC. Для missing-job в журнале есть исходные диагностические UUID, но SQL-квитанция не приписывает событие существующему заданию.

Итог 23:17:18: Stable группа с одним member, committed/end offsets 7/2/1, lag=0 везде. Outbox-status после всех некорректных событий сохранил четыре задания (два failed, два succeeded), pending=0, прежние эффекты и completed_at. Новых заданий/эффектов из некорректных записей не появилось.

### Финальная регрессия и очистка — выполнено

После всех некорректных событий новая обычная заявка `151bfebf-49c0-422d-afd0-2116b896e1fb`, ключ `outbox-1792970346443347000`, event `158487b3-cebb-41e2-b74c-2e7e549ba15e` принята в `2026-10-25T23:19:06.4884701Z`. Source partition 2/offset 1, опубликована в 23:19:06.725182. GET и SQL подтвердили succeeded, completed_at=`23:19:07.80108Z`, result outcome=completed/duration_ms=1000, effects=1 и outbox_rows=1. Итого пять заданий: три succeeded с одним эффектом каждое, два failed без эффекта; pending outbox=0. Счётчики предыдущих ошибок остались 2/3/3. DLQ содержит восемь подтверждённых квитанций: две для исчерпания ошибок и шесть для некорректных записей с учётом второго oversized.

Снимок `23:21:14Z`: worker `lab-worker-69b8fd7494-4m76l` Ready 1/1 без перезапусков на прежнем образе de6f15ba5c4d, группа Stable, один member с [0,1,2], committed/end offsets 7/2/2, lag=0 во всех partitions. Publisher Ready 1/1 без перезапусков на прежнем образе `0fcdb84f54df4e30ccb9c3e50887a065cf13b750625be1c81e9bdcf510bf7c48`; PostgreSQL 1/1 без перезапусков, PVC Bound и прежний PV `pvc-dd7b8153-83f3-4322-8561-145c9fbbb29a` сохранён.

`task smoke` завершил внутрикластерный клиент Succeeded, вернул API revision `011b2e8c144c` через Service с EndpointSlice `10.244.0.9`, затем удалил smoke Pod. API/publisher не обновлялись на новый migration-образ; это ожидаемая совместимость прежнего формата событий и добавочной миграции, а не прогон всех процессов на одном SHA.

`task worker-diagnostics-cleanup` удалил все 30 перечисленных диагностических Pod. Jobs/outbox/retry state, DLQ-квитанции и сообщения, topics, offsets, Secret и PVC не очищались. Отдельная финальная проверка Kafka PV не предоставлена; сохранность Kafka при отказе хранилища этим опытом не доказывается. Доказательство финального состояния — `c38b511e-d13a-47cc-bffa-3b1d90b09ead`. Команды выполнял владелец, агент анализировал предоставленный вывод и сохранял существенные значения в этом отчёте.

## Диагностика

Сохранить SHA, build revisions и образы API-migration/worker отдельно от работающих API/publisher. Для каждого опыта фиксировать job/event ID, source topic/partition/offset, group offsets, failure_count/next_retry_at, SQL-эффекты, DLQ receipt и прочитанную запись, журналы и Pod UID до удаления. При PreparingRebalance назначения могут быть неизвестны; lag=null не считать нулём. Историю каждого отказа сохранять.

## Причина

SQL и Kafka имеют независимые подтверждения. При штатном успехе source commit следует за SQL-эффектом. При отказе source commit следует за DLQ ack и SQL-квитанцией; до этих действий запись должна повторяться. Число рабочих ошибок хранится независимо от перезапуска процесса.

## Восстановление

Обычные worker-stop/worker-deploy сняли лабораторный DLQ bootstrap, не сбросив failures или source offsets. Финальная обычная заявка, smoke через Service, доступность зависимостей и удаление диагностических Pod подтвердили возврат стенда. Изолированная SQL-тестовая БД удалена, её отсутствие проверено.

## Поведение Kubernetes

Kubernetes управляет Pod, репликами и probes; очередь повторов, DLQ и порядок commit принадлежат приложению. Readiness сама по себе не блокирует consumer. Отказ внешней зависимости не меняет liveness.

## Поведение приложения

Worker выполняет одну ограниченную операцию за раз. Контролируемые ошибки фиксируются после завершения работы; отмены и транспортные/SQL-ошибки не расходуют счётчик контролируемых ошибок. При ошибке текущая consumer-сессия закрывается, последующие source offsets не подтверждаются. Задержка повтора хранится по часам PostgreSQL, без имитации отложенного Kafka topic.

## Что результат подтверждает

Описанные конечные повторы и DLQ на локальном стенде с реальными Kafka/PostgreSQL: успех после двух ошибок, конечный отказ после трёх, запрет source commit при недоступном DLQ producer, восстановление той же записи без новых рабочих попыток, пять видов некорректных событий без задания, финальную обычную обработку и очистку диагностики. В ходе опыта также наблюдалась успешная диагностика offsets при assignment_pending=true.

## Что результат не подтверждает

Exactly-once delivery/DLQ, потерю диска, настоящий отказ broker, все варианты forced rebalance, обработку произвольной прикладной работы, CronJob или полный Milestone 7.

## Остаточный риск

Падение после DLQ ack до SQL commit может повторить запись DLQ; стабильная source identity позволяет отличить дубликаты. Эта аварийная точка в кластере не создавалась. Отложенный retry и недоступная DLQ задерживают обработку текущей consumer-сессии целиком, а не только partition. Локальный topic с тремя partitions имеет retention; ручное пересоздание topic может нарушить смысл координат SQL-квитанций. Очистка этих квитанций по retention ещё не реализована. Отдельное сохранение счётчика управляемых ошибок при перезапуске процесса посреди рабочих повторов проверено SQL-тестом, но не отдельным runtime-опытом; runtime-восстановление после исчерпания ошибок наблюдалось в опыте недоступного DLQ producer.
