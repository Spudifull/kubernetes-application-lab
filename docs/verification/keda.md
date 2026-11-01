> Historical result from the private development history. Original identifiers are preserved; this is not a rerun of the public snapshot. Reviewed screenshots are included; raw captures remain private. See [provenance](../provenance.md). Local paths, where present, were redacted.

# KEDA: рост и уменьшение числа worker по Kafka lag

- Статус: `Passed` для масштабирования 1–3 по lag, партии из 30 заданий, уменьшения до одной реплики и возврата управления Helm.
- Ревизия кода и исходного preflight: `656d989`; уточнение RBAC values зафиксировано в `a4f78418beccfc8a396cf5e14821c248f90ed930`. Во время сбора доказательств менялась документация, не исполняемый код.
- Среда: основной kind/Kubernetes 1.34, Kafka 4.2.2, PostgreSQL, действующие API/publisher/worker.
- Контракт: [Kafka и обработка заданий](../design/kafka-event-processing.md).
- Воспроизведение: [инструкция](../engineering/keda-lab.md).

## Цель

Проверить связь накопленного lag с числом consumer-реплик и последующим возвратом к одной реплике без потери заданий или повторных SQL-эффектов.

## Исходные условия

Go/Helm-проверки и ревизии приведены ниже. Перед установкой не было другого External Metrics provider. CronJob normal/suspended/inactive после завершённых сценариев, worker без fault settings, исходно offsets=11/5/2, pending=0, lag=0. KEDA chart/app 2.21.0, отдельный KafkaUser lab-keda с Describe, ScaledObject Ready и числовая метрика HPA подтверждены выполнением. Работающий worker сохраняет образ 8d1e8c624ebf85d1796525326f135d33b74e91f2bbe0a8c1c9997a87ef21dd67 (de6f15ba5c4d); API/publisher используют прежние образы, не собираются заново для этого опыта.

## Способ создать нагрузку или отказ

Включить opt-in scaling, создать 30 заданий по 8 секунд через прежний HTTP API, наблюдать HPA/replicas/consumer group и PostgreSQL connections. После обработки дождаться уменьшения числа реплик и вернуть фиксированное управление Helm. Отказ не создаётся.

## Ожидаемые признаки

Положительный lag и метрика HPA предшествуют увеличению Ready replicas от 1 до 2–3; группа распределяет три partitions. Все задания succeeded с одним outbox/эффектом. Lag=0 и уменьшение до одной реплики после stabilization. Disable удаляет ScaledObject/HPA, фиксированный worker Ready 1/1, smoke успешен, данные сохранены.

## Наблюдавшиеся признаки

27 октября 2026 года владелец предоставил успешный go test ./... (cmd/labctl=2.003 s, internal/scheduled=1.713 s), race cmd/labctl=2.543 s, успешные cron-helm-lint (включая профили), worker-helm-lint и keda-worker-helm-lint (enabled/paused). Отдельным повтором подтверждены go test -race ./internal/scheduled (cached) и go vet ./... (без ошибок); смешанная строка предыдущего вывода больше не является пробелом доказательства.

Исходный preflight: узел Ready, Kubernetes 1.34.0, 12 CPU, allocatable memory=7794856Ki. Memory/Disk/PID pressure=false. Requests=2100m CPU/2658Mi памяти, limits=5100m/5126Mi. По reservations помещаются KEDA и две дополнительные worker-реплики; фактическое пиковое потребление памяти не измерено. API/publisher/worker Ready 1/1 на прежних образах; CronJob suspended/inactive. Другой External Metrics APIService в снимке отсутствует.

task keda-render успешно сформировал официальный chart 2.21.0 без изменения кластера. При review обнаружены wildcard custom scale rules и отсутствие ограничения Secret по namesAllowList при restrict.secret=true. Custom targets отключены в values; игнорируемый allowlist удалён, граница namespace-only для Secret описана явно. Повторный task keda-render успешен; проверка сформированного ClusterRole подтвердила отсутствие wildcard-прав custom scale targets после уточнения values. На этапе render выполнение ещё не было подтверждено; последующие разделы фиксируют завершённую установку и опыт.

## Диагностика

Первая попытка task keda-install после очистки CronJob остановлена до установки: client kubectl=1.29, server=1.34, unsupported skew. В новом PowerShell не был сохранён предыдущий приоритет локального kubectl 1.35.9 в PATH. Затем task keda-identity-create успешно создал KafkaUser lab-keda с Ready и task keda-secret-create создал lab-keda-kafka без печати credentials. Enable остановился на чтении scaledobjects до изменения worker release; status подтвердил отсутствие CRD. Ошибки resource type не считаются ошибкой Kafka identity или выполненной установкой KEDA. Продолжение выполнено после исправления PATH и повтора install/enable/status; созданные identity/Secret сохранены.

После явного выбора kubectl 1.35.9 task keda-install завершился успешно: release lab-keda revision=1, 27 октября 2026 года, 07:45:49 UTC, chart/app 2.21.0. Три Deployment Ready 1/1: operator, metrics-apiserver и webhooks на ghcr.io/kedacore/*:2.21.0. APIService v1beta1.external.metrics.k8s.io Available=True. Operator Pod keda-operator-67f8dcfd94-rd9cq имеет один предшествующий перезапуск, причина в первом снимке ещё не получена (установлена ниже); остальные Pod без перезапусков. Предупреждения CRD unrecognized format int32/int64 не помешали установке.

В 07:47:19 UTC включён worker scaling, release lab-worker revision=19, chart=lab-worker-0.4.0. ScaledObject UID=a1768d65-4fd6-4b12-8aee-fbb1fa07f031, TriggerAuthentication UID=9b8de98f-6023-44cd-b6e9-dbef18fec64c, HPA UID=72a69855-8e07-480a-a3d1-8518f02a2a74. ScaledObject Ready=True, Active=False (ScalerNotActive), Paused=False, Fallback=False, originalReplicaCount=1. HPA создан, но первый снимок содержит currentMetrics=null, desiredReplicas=0 и ещё не доказывает успешного получения числовой метрики. Предупреждение о pollingInterval отражает режим minReplicaCount=1: интервалы масштабирования задаёт HPA; pollingInterval продолжает обновлять conditions/events.

Worker сохранил образ 8d1e8c624ebf85d1796525326f135d33b74e91f2bbe0a8c1c9997a87ef21dd67, но Deployment обновил Pod на lab-worker-85fb54b467-kjk6z (10.244.0.102), Ready 1/1, restarts=0. Смена Pod при включении режима не считается аварией приложения. В этом исходном снимке партия ещё не запускалась; числовая метрика и причина перезапуска проверены следующим шагом.

Следующий снимок подтвердил HPA AbleToScale=True/ReadyForNewScale и ScalingActive=True/ValidMetricFound с 07:47:36 UTC. currentMetrics external.averageValue=0, target averageValue=3, currentReplicas=1, desiredReplicas=1. ScalingLimited=True/TooFewReplicas отражает нижнюю границу minReplicas=1 при пустой очереди, а не ошибку получения метрики.

Previous state operator: startedAt=07:46:15, finishedAt=07:46:16, reason=Completed, exitCode=0. Журнал cert-rotation показывает refreshing CA/server certs, server certs refreshed, затем Secrets have been updated; exiting so pod can be restarted (RestartOnSecretRefresh). Перезапуск связан с явным завершением процесса после обновления сертификатов; признаков OOMKilled в этом состоянии нет. Версия operator 2.21.0, upstream commit 626ded5d783108bedf647e20bbc83f8e458e02e4. Причина перезапуска установлена, числовая метрика подтверждена: исходная готовность перед нагрузкой обеспечена.

Записать SHA/чистоту, chart/image versions, APIService/ScaledObject/HPA conditions, lag/current metric/desired/current/ready replicas во времени, group assignments и offsets, IDs партии, SQL outbox/effects, pg_stat_activity во время нагрузки, max_connections, final readiness/cleanup и передачу управления Helm. Источники событий и команды указаны в инструкции.

## Наблюдение нагрузки и масштабирования

27 октября 2026 года наблюдатель предоставил 112 JSON-событий Deployment/HPA за интервал примерно 07:53:02–07:57:37 UTC. Deployment UID остался 0816eccc-609b-41c7-99ee-b7294c24ca68, образ worker — прежний 8d1e8c624ebf85d1796525326f135d33b74e91f2bbe0a8c1c9997a87ef21dd67. Новых ручных изменений replicas в этом интервале не заявлено.

| Первый наблюдённый снимок UTC | Deployment desired/current/ready | HPA current/desired/average metric |
|---|---|---|
| 07:53:02 | 1/1/1 | 1/1/0 |
| 07:53:22 | 2/2/1 | 1/2/5, lastScale=07:53:21 |
| 07:53:27 | 2/2/2 | — |
| 07:53:37 | 3/3/2 | 2/3/4500m, lastScale=07:53:36 |
| 07:53:42 | 3/3/3 | — |
| 07:55:37 | 3/3/3 | 3/3/0 |
| 07:55:52 | 2/2/2 | 3/2/0, lastScale=07:55:51 |
| 07:56:22 | 1/1/1 | 2/1/0, lastScale=07:56:21 |
| 07:56:37 | 1/1/1 | 1/1/0 |

Между 07:53:47 и 07:53:57 наблюдалось краткое снижение readyReplicas с 3 до 2 и возврат к 3; непрерывная готовность всех трёх реплик не заявляется. HPA averageValue — метрика для вычисления масштаба, не подмена прямого снимка суммы committed lag.

Партия run_id=`keda-1793087596692160300`: 30 enqueue за 07:53:16.747–07:53:16.973. Получены 30 уникальных job_id со succeeded, outcome=completed, duration_ms=8000; worker_batch_completed jobs=30 в 07:55:25.977. Время завершения обработки последнего задания по SQL полю HTTP-ответа — 07:55:25.734. Первый job_id=a7f7235c-2d16-45f2-bbc7-aab7d2c5022e, последний=ef5cc058-d50c-464d-8a7f-b208f08b907f. Полный набор сохраняется в .local-kafka/worker-batch.json и предоставленном выводе, не создаётся отдельный сводный отчёт.

В 07:53:40 группа lab-workers имела members=3, state=PreparingRebalance: worker lab-worker-85fb54b467-qt54l (10.244.0.104) показывал partitions [1,2], qhbs9 (10.244.0.103) — [0], kjk6z (10.244.0.102) — assignment_pending=true. Это переходный снимок, не окончательное стабильное распределение по одной partition на реплику. Committed/log-end offsets 11/20, 5/13, 2/8, lag соответственно 9/8/6 (сумма 23). Наблюдение подтверждает вступление трёх consumers и накопленную очередь; финальный Stable/lag=0 ещё требуется.

SQL-снимок во время обработки: все 30 IDs имеют outbox_rows=1, эффекты ещё 0, два задания processing, остальные published. В pg_stat_activity по одному idle-соединению от API 10.244.0.9, publisher 10.244.0.15 и каждого worker 10.244.0.102/.103/.104, плюс один active psql (всего 6), max_connections=100. Это измерение во время нагрузки, не доказательство отсутствия давления при любой нагрузке. Этот промежуточный снимок не подтверждает итоговые эффекты; финальные SQL и handoff приведены ниже.

## Причина

Kafka scaler измеряет committed consumer lag, HPA меняет масштаб Deployment. Consumer group распределяет partitions, приложение сохраняет эффект и commit. Предел параллелизма ограничен тремя partitions; рост числа реплик увеличивает потенциальное число SQL-соединений.

## Итоговые SQL, offsets и возврат Helm

После партии task keda-check показал все 30 сохранённых job_id как succeeded, outbox_rows=1, effects=1. Повторная проверка по точным IDs не заменена общим LIMIT 30. PostgreSQL snapshot после уменьшения реплик: по одному idle-соединению API 10.244.0.9, publisher 10.244.0.15 и оставшегося worker 10.244.0.102, плюс active psql, всего 4 при max_connections=100. Во время нагрузки наблюдалось 6 с тремя worker, как описано выше.

В 08:02:45 UTC группа lab-workers Stable, members=1, все partitions [0,1,2] у lab-worker-85fb54b467-kjk6z, assignment_pending=false. Committed и log end offsets равны 23/15/10, lag=0 на каждой partition. От исходных 11/5/2 прирост 12/10/8, в сумме 30 записей партии.

В 08:03:12 task keda-disable через Helm revision=20 поставил paused-replicas=1 и подтвердил одну реплику. Затем удалён ScaledObject lab-worker и завершено ожидание удаления его HPA. В 08:03:14 Helm revision=21 вернул autoscaling.enabled=false и fixed replicas=1. Команда завершилась успешно, force ownership не применялся. Task keda-status вернул items=[] для ScaledObject/TriggerAuthentication/HPA; worker Ready 1/1 на прежнем Pod lab-worker-85fb54b467-kjk6z, restarts=0, образ прежний. Передача управления не потребовала нового Pod.

Финальный outbox-status: published=42, pending=0, succeeded=40, failed=2 (старые задания). Все 30 новых строк имеют один outbox и один эффект; attempts=1. PostgreSQL Ready 1/1, restarts=0, прежний Bound PVC на pvc-dd7b8153-83f3-4322-8561-145c9fbbb29a; publisher Ready 1/1, restarts=0 на прежнем Pod/образе. API smoke успешен, revision=011b2e8c144c, endpoint=10.244.0.9; smoke Pod удалён. Удалены два worker-diagnostic Pod: 1793087618655670800 и 1793088163700924700.

Официальные KEDA operator/metrics-apiserver/webhooks остаются Ready 1/1; у operator по-прежнему один объяснённый перезапуск при обновлении сертификатов, новых в финальном снимке нет. APIService Available=True, но scaler/HPA приложения уже отсутствуют. KafkaUser, Secret, CRD и инфраструктура KEDA оставлены для повторного опыта; SQL/Kafka данные и PVC не удалялись. CronJob оставался normal/suspended после его очистки, resume во время KEDA-опыта не выполнялся.

## Восстановление

Штатный keda-disable выполнен успешно, как описано выше. Воспроизведение ошибки метрик и частичного disable не выполнялось: при таких отказах сохранить conditions/журналы и осмотреть оставшиеся объекты до повтора. Не удалять БД/topics/offsets/Secret и не выполнять fixed worker-команды до снятия ScaledObject. KEDA operator остаётся установленным.

## Поведение Kubernetes

ScaledObject передаёт масштабирование HPA, replicas исключено из Deployment при opt-in scaling. Пауза и удаление HPA предшествуют возвращению replicas=1 в Helm. Встроенные liveness/readiness worker не меняются.

## Поведение приложения

Прежние API/outbox/publisher/worker обработали партию без новой бизнес-логики. Во время масштабирования наблюдался rebalance, все 30 заданий завершились с одним эффектом. Повторная доставка допустима по контракту, но наличие дубликата именно в этом опыте отдельно не заявляется.

## Что результат подтверждает

Наблюдённую цепочку числовая метрика/положительный lag → рост worker 1→2→3 → обработка 30 заданий с одним outbox/эффектом → lag=0 → уменьшение 3→2→1 и штатный возврат фиксированного управления Helm. Финальная доступность и очистка диагностики подтверждены на указанной комбинации образов.

## Что результат не подтверждает

Scale-to-zero, HA, срабатывание при отказе Kafka/метрик, максимальную пропускную способность, отсутствие SQL pressure при любой нагрузке или exactly-once delivery. Эти свойства не следуют из установки KEDA.

## Остаточный риск

Протокол/ACL/TLS и штатный handoff проверены только на указанной конфигурации. Кластерные CRD/RBAC/APIService и namespaced Secret access KEDA остаются после опыта. Пиковая CPU/память и предельная нагрузка на PostgreSQL не измерялись, снимки соединений не являются общей гарантией запаса. Во время rebalance готовность кратко снижалась с трёх до двух реплик. Новые версии, scale-to-zero и ошибки метрик требуют отдельных сценариев. Необъяснённые наблюдения прежних отчётов не снимаются этим опытом.
