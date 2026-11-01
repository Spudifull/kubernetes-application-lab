> Historical result from the private development history. Original identifiers are preserved; this is not a rerun of the public snapshot. Reviewed screenshots are included; raw captures remain private. See [provenance](../provenance.md). Local paths, where present, were redacted.

# CronJob: назначенный запуск и повторный приём

- Статус: `Passed` для двух запусков по расписанию, двух повторов первого логического запуска и финальной очистки.
- Ревизия: `f5bf6d89a4729711916d2e36251eb123fecdd698`.
- Выполнение: 26 октября 2026 года, время ниже в UTC; команды выполнял владелец стенда, результаты предоставлены в выводах PowerShell.
- Среда: основной kind, Kubernetes 1.34, существующие PostgreSQL, Kafka, publisher и worker.
- Контракт: [Kafka и обработка заданий](../design/kafka-event-processing.md).
- Воспроизведение: [инструкция](../engineering/cronjob-lab.md).

## Цель

Связать назначенное время CronJob с воспроизводимым ключом транзакционного приёма. Проверить реальное расписание, повтор того же логического запуска и отсутствие второй outbox-записи/эффекта при повторе.

## Исходные условия

API-образ для CronJob: `kubernetes-application-lab-api:88df17930a5ed7d92a39fbfa5e84505407f8f21a0a684ab529364019ad97cd9b`, revision=`f5bf6d89a472`, build_date=`2026-10-26T03:17:42.412009400+03:00`. Сборка не помечена dirty. PostgreSQL 16.14-bookworm с миграциями 001/002/003; исходно published=6, pending=0, группа lab-workers Stable, offsets 8/2/2, lag=0. Проверки Go/Helm приведены ниже.

Работающий HTTP API остался на `011b2e8c144c`, publisher — на образе `0fcdb84f54df4e30ccb9c3e50887a065cf13b750625be1c81e9bdcf510bf7c48`, worker — на `8d1e8c624ebf85d1796525326f135d33b74e91f2bbe0a8c1c9997a87ef21dd67` (ревизия `de6f15ba5c4d`). Новый API-образ использован только одноразовыми Pod. Подробные параметры Kafka принадлежат [отчёту инфраструктуры](kafka-bootstrap.md).

## Способ создать нагрузку или отказ

Включить расписание, получить два автоматически созданных Job с разными назначенными моментами, приостановить CronJob. Дважды вручную повторить назначенный момент одного из них через ту же точку входа. Не пересоздавать БД, topics или consumer group.

## Ожидаемые признаки

Реальные Job имеют owner CronJob и scheduled-timestamp. Их Pod читают timestamp из Job через ограниченный Kubernetes API client, затем принимают задание и outbox одной SQL-транзакцией. Разные моменты дают разные ключи. Повторы того же момента возвращают тот же job_id и created=false. Publisher/worker доводят исходные задания до succeeded с одним эффектом, группа Stable, pending=0 и lag=0.

## Наблюдавшиеся признаки

Владелец предоставил успешные результаты `go test ./...`, `go test -race ./cmd/api ./cmd/labctl ./internal/scheduled` и `go vet ./...`. Race-проверки cmd/labctl и internal/scheduled завершились за 2.322 s и 2.196 s соответственно; cmd/api компилируется, собственных тестов пока не имеет. `task cron-helm-lint` успешно проверил disabled и enabled варианты chart, `helm lint helm/lab-postgres` также прошёл. Рекомендация icon от Helm является информационной.

В 00:18:11 установлен release lab-cron, revision=1, suspend=true, active=0, lastSchedule отсутствует. CronJob UID=`01f20b34-0c25-444d-a11c-adcb0805f0d2`. `db-deploy` перед установкой обновил NetworkPolicy PostgreSQL. Проверки `auth can-i` для ServiceAccount lab-cron вернули get jobs=yes, list jobs=no, get secrets=no.

| Назначенный момент | Kubernetes Job / UID | Ключ | Задание приложения |
|---|---|---|---|
| `2026-10-26T00:20:00Z` | `lab-cron-29882900` / `2c1ec530-d012-4d2a-8a81-96991eba203f` | `cron_37f1f714a77f48e109445c9d777be71462591d1d0c29da9a164a5842` | `b6b36576-05a2-465b-8184-3cd3cb6dd57a` |
| `2026-10-26T00:23:00Z` | `lab-cron-29882903` / `dd600995-1ef7-4b40-9108-68cc5abb037e` | `cron_e6dd81629cbd27c69438b05d31d4a03d7538ae8d48c5648405a314c4` | `68f72e50-cf52-460e-84ca-50a90009edcc` |

Первый resume выполнен в 00:20:08. Job создан в 00:20:09 для исходного момента 00:20:00, Complete в 00:20:13. В metadata сохранены owner CronJob и scheduled-timestamp; Pod `lab-cron-29882900-ldzkd` прочитал timestamp и вывел scheduled_job_enqueued, created=true в 00:20:10.423. Событие `935310c2-3be7-4603-91be-88ce000583e0` опубликовано в 00:20:11.014, partition=1, offset=2; задание succeeded в 00:20:12.036, outbox_rows=1, effects=1. Расписание приостановлено в 00:20:32 после одного Job: повторяющиеся строки watch отражали изменения одного объекта, а не два запуска.

В 00:21:44.904 и 00:21:50.401 отдельные ручные Job `lab-cron-manual-1792974103434050900` и `lab-cron-manual-1792974108903945100` повторили момент 00:20:00. Оба завершились Complete, вернули прежние ключ/job_id, created=false, status=succeeded. После повторов published осталось 7, pending=0, outbox_rows=1, effects=1; offsets остались 8/3/2, lag=0. Новый Job/Pod не создал вторую outbox-запись или эффект.

Второй resume выполнен в 00:22:40. Новый автоматический Job для 00:23:00 завершился Complete за 4 s; Pod `lab-cron-29882903-jtm27` в 00:23:01.411 вывел новый ключ/job_id, created=true. Событие `23f58e16-fb57-40e4-bfa9-2605af5df72c` опубликовано в 00:23:02.296, partition=0, offset=8; задание succeeded в 00:23:03.310, outbox_rows=1, effects=1. В 00:23:28 расписание снова приостановлено: suspend=true, active=0.

Итог: published=8, pending=0, succeeded=6 и failed=2 (два старых задания из прежних опытов). В 00:23:39 группа Stable, один прежний worker, offsets 9/3/2, lag=0 на всех partitions. Publisher/worker Ready 1/1, прежние Pod `lab-publisher-68bdd687f8-vntxb` и `lab-worker-69b8fd7494-4m76l`, restarts=0. PostgreSQL `lab-postgres-0` Ready 1/1, restarts=0, PVC Bound на прежнем `pvc-dd7b8153-83f3-4322-8561-145c9fbbb29a`. Финальный task smoke успешен: API revision=011b2e8c144c, endpoint=10.244.0.9.

В 00:24:38 task cron-cleanup вернул suspend=true через Helm (revision=6) и удалил второй автоматический и два ручных Job. task cron-status показал только suspended CronJob без Job/Pod, active=0. Первый автоматический Job уже отсутствовал к снимку 00:23:29; причина его удаления не установлена, прежние metadata и журнал сохранены в предоставленном выводе. task worker-diagnostics-cleanup удалил четыре диагностических Pod. Smoke Pod удалён самой smoke-командой. Данные SQL/Kafka и PVC не очищались.

Агент выполнил gofmt и git diff --check; Go/Helm-проверки, сборку и команды кластера выполнял владелец.

## Итоговая регрессия после CronJob

26 октября 2026 года владелец выполнил финальный прогон на чистой ревизии команд `9e95483e37b84c363a30f0b2b8f5e61e69292ca8`. Код после успешных Go/Helm-проверок не менялся; проверки не запускались повторно. Работающие образы остались указанными в исходных условиях: это проверка действующей комбинации ревизий, а не новая общая сборка всех процессов.

Выполнены task cron-status, kafka-smoke, job-enqueue, job-status, outbox-status, worker-group-status, smoke, db-status, publisher-status и worker-status. Исходно CronJob suspend=true, active=0, без Job/Pod; после этого resume не выполнялся. В 00:29:00 Kafka smoke записал и прочитал marker `smoke-1792974538847590300` в lab.smoke.v1, partition=0, offset=2. Probe-образ `kubernetes-application-lab-kafka-probe:c5da353fb59a1380bc2682ee5262df985d6459cb267c11800dfe43955b246c19`.

HTTP enqueue вернул 202 в 00:29:03.448: ключ `outbox-1792974543412953100`, job_id=`d7783d13-b1bf-4cd2-95a1-8bbe83e6179a`, event_id=`89193e05-8374-47aa-9a42-322dcde05455`. SQL created_at=00:29:03.439, published_at=00:29:03.900, attempts=1, partition=0, offset=9. В 00:29:04.912 задание стало succeeded, result={outcome:completed,duration_ms:1000}, outbox_rows=1, effects=1. Итог published=9, pending=0, succeeded=7, failed=2 (старые задания).

В 00:29:49 группа lab-workers Stable с одним прежним worker, committed offsets и log end offsets равны 10/3/2, lag=0. API smoke успешен, revision=011b2e8c144c, Service endpoint=10.244.0.9. Publisher/worker/PostgreSQL Ready 1/1, прежние Pod, restarts=0; PostgreSQL сохранил Bound PVC на `pvc-dd7b8153-83f3-4322-8561-145c9fbbb29a`. Новый rollout приложений не выполнялся.

После сохранения результатов task kafka-smoke-cleanup удалил `lab-kafka-smoke-1792974538847590300`, task worker-diagnostics-cleanup — `lab-worker-diagnostic-1792974588395369100`; API smoke удалил свой Pod. SQL, topics, offsets и PVC сохранены. Port-forward можно закрыть Ctrl+C без изменения состояния кластера.

Штатная цепочка API → PostgreSQL/outbox → Kafka → worker после опытов подтверждена. В совокупности с профильными отчётами bootstrap/outbox/worker/redelivery/retry-DLQ/broker-outage это завершает базовый объём Milestone 7 в их точных границах. Финальный прогон не повторяет все отказы на одном новом образе, не доказывает HA, KEDA, exactly-once delivery, все окна аварии SQL/Kafka или срабатывание Forbid/deadline. Нулевой lag остаётся снимком, одна broker/controller-нода — точкой недоступности. Задержка Operator и исчезновение первого CronJob Job остаются неустановленными наблюдениями профильных отчётов.

## Диагностика

Проверены предоставленные metadata первого Job, журналы автоматических и ручных запусков, SQL и consumer group snapshots. UID Job сохранены, UID их Pod отдельно не предоставлены. Для второго запуска назначенное время подтверждено журналом одноразового процесса, UID Job — selector контроллера в снимке status; отдельный полный YAML второго Job не предоставлен. Job Complete сопоставлен с SQL succeeded и эффектом, а не использован как доказательство выполнения worker. API locator не применялся к заданиям CronJob.

## Причина

Kubernetes допускает пропуски и дубли запусков. Forbid ограничивает пересечение управляемых Job одного CronJob, но не заменяет SQL-идемпотентность. Ключ от namespace, имени CronJob и назначенного момента переживает замену Pod/Job и неопределённый результат предыдущего commit.

## Восстановление

Оба периода расписания завершены явным suspend; повтор исходного момента проверен при приостановленном расписании. Ошибок приёма или отказов зависимостей в этом опыте не создавалось. Финальный cleanup удалил оставшиеся lab-cron Job/Pod и диагностические Pod; CronJob сохранён suspended, SQL/Kafka и PVC сохранены.

## Поведение Kubernetes

batch/v1 CronJob создаёт ограниченные по времени Job, backoffLimit=2, restartPolicy=Never, concurrencyPolicy=Forbid, startingDeadlineSeconds=30, activeDeadlineSeconds=90. TTL/history limits удаляют завершённые Job. ServiceAccount имеет get jobs только в namespace; другие права не добавлены.

## Поведение приложения

Одноразовый режим не поднимает HTTP-сервер, не публикует в Kafka и не исполняет эффект. Он читает metadata, вызывает прежний Store.Enqueue и завершает процесс. Ошибка metadata не заменяется текущим временем. Повтор обрабатывает существующая транзакционная проверка ключа и содержимого.

## Что результат подтверждает

Два разных назначенных момента реального CronJob создали разные задания. Два ручных повтора первого момента вернули прежнее задание без новых outbox-записей и эффектов. Оба исходных задания завершились через прежнюю цепочку publisher/worker, lag вернулся к нулю, финальная доступность и очистка подтверждены.

## Что результат не подтверждает

Exactly-once запуск Kubernetes, отсутствие пропусков расписания, запрет ручных параллельных Job политикой Forbid, HA, восстановление после всех вариантов отказов SQL/API, фактический пропуск длительного Job по Forbid или срабатывание deadline/SIGTERM. Эти расширения ещё не выполнены и не входят в завершённый базовый объём Milestone 7.

## Остаточный риск

Прочитать timestamp можно только при доступном API и поддержке аннотации Kubernetes 1.32+. Role разрешает чтение любого Job своего namespace. Namespace/имя CronJob входят в логическую идентичность: пересоздание того же имени не сбрасывает SQL-ключи. Смена имени, назначенного момента или удаление SQL-истории меняет границу дедупликации. Причина исчезновения первого Job не установлена; TTL/history limits могут удалить журналы до их сохранения. Основной kind не доказывает применение NetworkPolicy. Проверка повтора была последовательной, не опытом одновременного приёма дубликатов.

## Расширение: Forbid, deadline и отказ зависимости — Passed

Новые сценарии не входят в Passed выше. Код профилей зафиксирован в 656d989; нужен новый API-образ с задержкой до SQL. Воспроизведение находится в [инструкции](../engineering/cronjob-lab.md#расширение-forbid-deadline-и-отказ-postgresql-для-cronjob).

Цель — отделить поведение scheduler/Job controller от поведения одноразового приложения. Профиль forbid задерживает приём на 75 s до SQL при Job deadline=120 s; deadline задерживает столько же, но задаёт Job deadline=15 s/backoffLimit=0; db-failure меняет только порт PostgreSQL для CronJob Pod на 1. Normal возвращает hold=0, порт из ConfigMap, deadline=90 s/backoffLimit=2. Общий PostgreSQL не останавливается.

Для Forbid нужны активный Job на следующей границе расписания, сигнал контроллера о пропуске и отсутствие нового Job для этого момента. Затем suspend и завершение уже принятого запуска с одним SQL-заданием. Для deadline нужны reason=DeadlineExceeded, ключ из scheduled_job_prepared, отсутствие SQL-задания и, отдельно, журнал отмены до SQL для утверждения об обработке SIGTERM. Для db-failure нужны ошибки подключения и конечный Failed, отсутствие задания, сохранённый scheduled timestamp; после normal повтор того же момента должен создать одно задание, следующий повтор — вернуть его без нового эффекта.

Расширение имеет Passed для Forbid, Job deadline, отказа БД и повторного приёма после восстановления; финальные доступность и очистка подтверждены ниже. 27 октября владелец подтвердил go test ./..., race cmd/labctl и cron-helm-lint со всеми профилями. Отдельным повтором подтверждены go test -race ./internal/scheduled (cached) и go vet ./... без ошибок. Новые unit-тесты проверяют границы задержки/отмену и запрет смены профиля при активном Job. Обработка SIGTERM одноразовым процессом не подтверждена журналом runtime-опыта deadline и не входит в Passed этого расширения.

Риски: контроллеры согласуют состояние асинхронно, Job может получить grace после deadline, допустим запоздалый запуск после завершения активного Job в пределах startingDeadlineSeconds. Нельзя доказывать пропуск только числом строк watch. Это управляемые лабораторные задержка и неверный порт, не все отказы Kubernetes API/PostgreSQL и не неопределённый результат SQL commit.

### Forbid — Passed

27 октября 2026 года, API revision=a4f78418becc (без dirty в выводе сборки), build_date=2026-10-27T09:52:55.419863000+03:00, образ `kubernetes-application-lab-api:b8c991c32821b4473e54b3aac0d8801617f123b398fe3e4a5ecb8af3d8ce276d`. CronJob release revisions 7 (normal deploy), 8 (forbid profile), 9 (resume), 10 (suspend). Далее время UTC.

Job `lab-cron-29884734`, UID def0dfa5-bc88-410f-b8b8-449c259361d1, назначен на 06:54:00. Pod lab-cron-29884734-srkt2 вывел scheduled_job_prepared в 06:54:01.671, hold_duration=75 s, затем enqueue в 06:55:16.777. Job Complete за 79 s. Контроллер зафиксировал JobAlreadyActive, затем SawCompletedJob первого и SuccessfulCreate следующего Job: overlap не разрешён, но отложенный запуск следующего момента допущен после завершения предыдущего.

Job `lab-cron-29884735`, UID 81e0f107-0158-43de-a35a-925430129bcf, назначен на 06:55:00; Pod lab-cron-29884735-h57j5 вывел prepared в 06:55:20.275 и enqueue в 06:56:35.357. Job Complete за 78 s. Второй запуск начат примерно на 20 секунд позже назначенного момента, в пределах startingDeadlineSeconds=30. Поэтому первоначальное ожидание только одного Job не используется как критерий Forbid: доказанное свойство — запрет одновременных активных Job, а не безусловная потеря следующего логического запуска.

Первое задание `234ea214-4e30-43ac-bb87-adfc087467e2`, ключ `cron_daeb06c2fb30b2e839acd45f2a94a8c51c1141530e66b51159c35ba1`, событие 7a3f49c8-d41a-434c-ba9d-0354349b09e8: succeeded в 06:55:18.322, partition=1/offset=3, outbox_rows=1, effects=1. Второе `44d73ae8-2bbb-4883-86bb-e793bc1471ba`, ключ `cron_1778e9bac501141f28eba4c90382189a7e838d72163a5f023ff03ad9`, событие 0366d15f-23db-4b4c-ac56-9170f0ad2877: succeeded в 06:56:36.440, partition=1/offset=4, outbox_rows=1, effects=1. Оба published с attempts=1.

После suspend CronJob SUSPEND=True, ACTIVE=0; оба Job сохранены Complete, оба Pod Completed, restarts=0. Published=11, pending=0, succeeded=9, failed=2 (старые). В 06:56:44 группа Stable, offsets 10/5/2, lag=0; диагностический Pod lab-worker-diagnostic-1793084202878342300 оставлен до общей очистки. Normal profile и cleanup ещё не выполнены: профиль меняется следующим опытом.

### Deadline — Passed в границе Job controller

27 октября 2026 года, тот же образ b8c991c32821b4473e54b3aac0d8801617f123b398fe3e4a5ecb8af3d8ce276d / revision=a4f78418becc. Release revisions 11 (deadline), 12 (resume), 13 (suspend). Job `lab-cron-29884764`, UID `1c7e6d61-4d0b-4cd3-ba1a-088d411f241a`, owner CronJob UID=01f20b34-0c25-444d-a11c-adcb0805f0d2. Назначен и создан в 07:24:00 UTC; activeDeadlineSeconds=15, backoffLimit=0, LAB_CRON_HOLD_SECONDS=75, terminationGracePeriodSeconds=10.

В 07:24:15 появилась FailureTarget=True, reason=DeadlineExceeded, message=Job was active longer than specified deadline. В 07:24:16 зафиксирована Failed=True с той же причиной, failed=1, ready=0, terminating=0. Чтение logs -f начато после suspend в 07:24:20 и завершилось timed out waiting for the condition; в итоговом списке deadline Pod уже отсутствует. Это не доказательство отсутствия запуска контейнера и не журнал обработки SIGTERM приложением. Scheduled_job_prepared/canceled_before_sql не получены, поэтому утверждение об обработке сигнала до SQL остаётся непроверенным.

Для точного назначенного момента `2026-10-27T07:24:00Z` task cron-run-status показал jobs=0, outbox_rows=0, effects=0. CronJob SUSPEND=True, ACTIVE=0; Failed Job сохранён для доказательства, две прежние успешные Job также сохранены. Доказаны срабатывание Job deadline и отсутствие SQL-задания этого логического запуска; общая очистка и normal profile ещё не выполнены.

### Отказ зависимости — наблюдался, восстановление ещё требуется

27 октября 2026 года, прежний образ b8c991c32821b4473e54b3aac0d8801617f123b398fe3e4a5ecb8af3d8ce276d / revision=a4f78418becc. Release revisions 14 (db-failure), 15 (resume), 16 (suspend). Job `lab-cron-29884767`, UID `993ad299-197e-4678-ae41-6e36abe07520`, назначен на `2026-10-27T07:27:00Z`, создан в 07:27:05 UTC. Template подтверждает LAB_DB_PORT=1, hold=0, activeDeadlineSeconds=90, backoffLimit=2; другие параметры PostgreSQL не менялись.

Три Pod — lab-cron-29884767-gnjd9, lab-cron-29884767-v9t7z, lab-cron-29884767-6kfvz — вывели prepared в 07:27:07.017, 07:27:18.283 и 07:27:39.318. Во всех случаях ключ `cron_92fe94de0bd9fe364c794b244839285cd569f7c5a9e8ce93951a8998`, назначенный момент неизменен; через примерно 0.5 s каждый процесс завершился с error=scheduled jobs schema unavailable. Одного общего текста ошибки недостаточно для определения причины; неверный порт подтверждён Job template, а доступность общей БД — отдельным db-status.

В 07:27:42 Job получил FailureTarget/Failed=True, reason=BackoffLimitExceeded, failed=3. Два снимка cron-run-status для исходного момента показали jobs/outbox_rows/effects=0/0/0. PostgreSQL lab-postgres-0 Ready 1/1, restarts=0, прежний адрес 10.244.0.7 и Bound PVC на pvc-dd7b8153-83f3-4322-8561-145c9fbbb29a. Job и журналы сохранены; normal profile, повтор исходного момента и последующая очистка пока не выполнены.

### Восстановление после отказа БД — выполнено

В 07:41:06 UTC normal profile применён через Helm, revision=17, CronJob остаётся suspended. Ручной Job lab-cron-manual-1793086868026604500 (UID c3bb403b-caad-4b5f-a13c-d8a7dfbec466) повторил исходный момент 2026-10-27T07:27:00Z. В 07:41:10.028 enqueue вернул created=true, job_id=b71a1bcb-f2ee-4c58-9ddb-3dbc8c582d83 с прежним ключом cron_92fe94de0bd9fe364c794b244839285cd569f7c5a9e8ce93951a8998. Событие 7c028fe5-1200-40ce-9384-09c5c9ffa5d3 опубликовано с attempts=1 в 07:41:10.682, partition=0, offset=10; succeeded в 07:41:11.710, один outbox и один эффект.

Второй ручной Job lab-cron-manual-1793086874255718900 (UID 542cc4c0-31a5-4f7d-9f75-7f375c187d95) в 07:41:16.216 вернул тот же ключ/job_id, created=false, status=succeeded. Оба Job Complete за 5 s. Точная SQL-проверка исходного момента показала jobs/outbox_rows/effects=1/1/1, общий pending=0, published=12, succeeded=10, failed=2 (старые задания).

В 07:41:26 группа Stable с одним прежним worker; committed/log end offsets 11/5/2, lag=0. CronJob SUSPEND=True, ACTIVE=0. Сохранились Failed Job deadline/DB и успешные Job; прежний первый Forbid Job отсутствует в этом снимке. Диагностический Pod lab-worker-diagnostic-1793086884797419800 сохранён до очистки. Финальный smoke/readiness/cleanup после этих расширений пока не предоставлены.

### Финальные доступность и очистка расширения

После восстановления владелец выполнил task smoke, db-status, publisher-status и worker-status: API smoke успешен (revision=011b2e8c144c, endpoint=10.244.0.9), PostgreSQL/publisher/worker Ready 1/1 на прежних Pod и образах, restarts=0. PostgreSQL PVC сохранился Bound на прежнем PV. В 07:43:33 UTC task cron-cleanup через Helm revision=18 сохранил normal/suspended CronJob и удалил пять оставшихся Job: второй Forbid, deadline, DB-failure и два ручных повтора восстановления. Cron-status показал только SUSPEND=True, ACTIVE=0 без Job/Pod. Удалены также два worker-diagnostic Pod с идентификаторами 1793084202878342300 и 1793086884797419800; API smoke удалил свой Pod. SQL, topics, offsets и PVC не удалялись. Расширение завершено в перечисленных границах; KEDA проверяется отдельно.
