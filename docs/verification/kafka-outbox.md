> Historical result from the private development history. Original identifiers are preserved; this is not a rerun of the public snapshot. Reviewed screenshots are included; raw captures remain private. See [provenance](../provenance.md). Local paths, where present, were redacted.

# Лабораторный сценарий: HTTP и transactional outbox

- Статус: `Passed` для второй итерации: HTTP/outbox, публикация, восстановление publisher и SQL integration-тест; не для полного Milestone 7.
- Ревизия API/publisher и первоначального сценария: `4345b1078c40df1eadfc298f419b919b5ceebdda`, перед сборкой дерево чистое. Исправленный сценарий и SQL integration-тест выполнены на чистой ревизии `e84d7a25fe15f9c669dfb4cb4b27b60cada3b05e`; образы процессов оставлены прежними.
- Среда: основной одноузловой kind Kubernetes `1.34.0`, kubectl `1.35.9`, PostgreSQL `16.14`, Strimzi `1.3.0`, Kafka `4.2.2`.
- Проверяемый контракт: [Kafka и обработка заданий](../design/kafka-event-processing.md).

## Цель

Проверить атомарное создание заявки/outbox, конкурентную идемпотентность HTTP, подтверждение публикации и сохранение ожидающего события при недоступном для publisher адресе Kafka. Worker отсутствует: завершение задания не ожидается.

## Исходные условия

Владелец проверяет Go-код и charts, создаёт commit и собирает оба образа из чистого дерева. Сохраняются Git SHA, build revision API/publisher, content-identity references, Pod UID/imageID, версии, исходные job/outbox counts, Kafka Ready и прежние PVC/PV PostgreSQL/Kafka. Миграция `002` применяется отдельным Job; повтор не создаёт вторую схему. Старый `001` не изменён. Исполняемый порядок — [инструкция](../engineering/kafka-lab.md#итерация-2--http-и-transactional-outbox).

## Способ создать нагрузку или отказ

Восемь HTTP-запросов одновременно начинают работу с одним новым ключом; затем отправляется другой payload с тем же ключом. Пока publisher отсутствует либо остановлен, сохранить строку `accepted` и ровно одно неопубликованное событие. После запуска publisher сопоставить `outbox_broker_ack`, `outbox_published`, PostgreSQL partition/offset и статус задания.

Во втором опыте старый publisher сначала полностью останавливается. Новому процессу задаётся закрытый loopback-адрес вместо Kafka bootstrap. Подать новую заявку, наблюдать неудачную публикацию, рост attempts, отсутствие published_at и сохранение строки задания. Затем вернуть каноническую конфигурацию и дождаться публикации того же event ID.

Отдельный integration-тест на изолированной PostgreSQL проверяет параллельный enqueue, параллельный claim, истечение lease, отказ старого токена, отсутствие регрессии статуса и полный откат заявки при ошибке INSERT outbox. Тест не входит в обычный `go test ./...` и не должен использовать основную БД.

## Ожидаемые признаки

- HTTP: восемь `202` с одним job ID, затем `409`; один job и один outbox event.
- До publisher: `accepted`, `published_at IS NULL`, отсутствие эффекта.
- После broker ack: тот же ID, `published`, заполнены published_at/partition/offset; `effects=0` до реализации worker.
- При недоступном адресе: API продолжает принимать заявки; publisher неготов, но жив; событие не теряется, ошибка не приводит к опубликованной отметке.
- После восстановления: прежний event ID опубликован, повторная отправка заявки с тем же содержимым не создаёт новую строку.
- PostgreSQL/Kafka PVC сохраняются; API без профиля jobs не получает новую зависимость.

## Наблюдавшиеся признаки

Владелец выполнил Go-тесты, race, vet, lint обоих прикладных charts, lint PostgreSQL и формирование jobs-манифестов. API собран как `kubernetes-application-lab-api:b7fd022772ddb499852f4f02f03751645887992396e515fbe900ef5cf13ba4b0`, publisher — `kubernetes-application-lab-publisher:e511ef75f369e3e32ebbcf78f029f89876f56f2d6f2d083488db49f3ff0e8a4f`. Оба имеют build revision `4345b1078c40`, без `-dirty`. Миграционные Job `lab-db-migration-20261025101107` и `lab-db-migration-20261025101235` завершились успешно; SQL-снимок содержит `001_lab_records` и `002_jobs_outbox`. Повтор создания Kafka Secret подтвердил совпадение без изменения удостоверения.

25 октября 2026 года конкурентная HTTP-проверка дала восемь `202` с job ID `1881ce49-f20b-4b45-b0aa-30ecca60ee3f` и ключом `outbox-1792923298685626300`, затем `409` для иного содержимого. До запуска publisher была одна строка `accepted`, один outbox event `50e57e5b-2c47-49d6-b712-07ab34b85fd8`, attempts `0`, pending `1`. Publisher `lab-publisher-6f844ff956-bkklk` получил broker ack в `10:15:32.857967 UTC` для partition `0`, offset `0`; PostgreSQL published_at — `10:15:32.858897 UTC`, затем событие `outbox_published`. Итог: `published`, pending `0`, effects `0`.

В опыте закрытого bootstrap API в `10:52:44.952825 UTC` вернул `202` для job `8bb9d0f1-3206-416e-85c5-3a4c16034772`, ключ `outbox-1792925564942539400`, event `bfa130ac-485b-467a-8e1b-7326f03ba6b0`. Publisher `lab-publisher-7698c64c69-k2c77` был `Running`, `0/1 Ready`, restarts `0`; заявка сохраняла `accepted`, published_at оставался пустым. Последующий SQL-снимок показал attempts `2` и `kafka_publish_failed`.

**Исходный отказ восстановления сохранён:** `task publisher-unreachable-restore` завершился ошибкой Helm — конфликт с менеджером `kubectl-set` на поле `LAB_KAFKA_BOOTSTRAP`. Сам publisher не восстановился от этой команды; это дефект сценария, не потеря задания. Владелец вручную вернул канонический bootstrap через `kubectl set env`, затем повторил задачу. Helm release стал `deployed`, revision `3`; новый Pod `lab-publisher-6f844ff956-s4p5d` — `1/1 Ready`, restarts `0`.

Сразу после восстановления SQL ещё показывал `accepted`, attempts `6` и next_retry_at `10:55:46.030602 UTC`. Это не новый отказ: следующий claim произошёл в `10:55:46.751615 UTC`. На попытке `7` тот же event получил broker ack в `10:55:46.772252 UTC`, partition `0`, offset `1`, и PostgreSQL published_at `10:55:46.772991 UTC`; `outbox_published` зарегистрирован в `10:55:46.775149 UTC`.

Итоговый SQL-снимок: published `2`, pending `0`; у обоих заданий `outbox_rows=1`, effects `0`, claim и error_code очищены. Kafka Ready, прежний broker UID `99405621-957e-4d82-892b-80f0cffdc2ea`, Kafka PV `pvc-952158d8-ec04-4ad6-a748-8212b33cab2d` Bound. PostgreSQL `1/1`, restarts `0`, прежний PV `pvc-08e126ef-91ad-406a-86d1-db94b3f2acbe` Bound. Smoke через Service прошёл на API `4345b1078c40`, временный Pod удалён. У Strimzi Operator наблюдался один restart; причина в этом опыте не устанавливалась. Разрыв `kubectl logs` не означал остановку publisher: последующая проверка того же Pod показала готовность и ноль рестартов.

### Повтор исправленного сценария

После успешных `go test -count=1 -race ./cmd/labctl` и `go vet ./cmd/labctl` владелец повторил опыт на ревизии `e84d7a25fe15` без ручного возврата адреса. API вернул `202` для job `645bd1f5-0170-4f8c-9510-ca58515f53f3`, key `outbox-1792926678620608000`, event `f4db6a35-5cee-4db6-a3b1-fffd6378504c`. Publisher `lab-publisher-7698c64c69-hh2n4` зарегистрировал три `outbox_publish_failed`: попытки занимали примерно по 20 секунд, задержки — 1, 2 и 4 секунды. Задание сохраняло `accepted`, published_at оставался пустым.

Задача `publisher-unreachable-restore` самостоятельно вернула адрес и выполнила Helm upgrade, revision `4`, без конфликта поля. Новый Pod `lab-publisher-6f844ff956-skkkf` стал `1/1 Ready`, restarts `0`, на прежнем образе. На попытке `4` прежний event получил broker ack в `11:12:25.961583 UTC`, partition `0`, offset `2`; PostgreSQL published_at — `11:12:25.962292 UTC`, `outbox_published` — `11:12:25.964112 UTC`. Повтор задачи восстановления также успешен: Helm revision `5`, тот же Pod, restarts `0`. Итог: published `3`, pending `0`; у всех job по одной строке outbox, effects `0`, claim/error_code очищены. Smoke прошёл через Service на API `4345b1078c40`, временный Pod удалён.

### SQL integration-тест и очистка

На чистой ревизии `e84d7a25fe15f9c669dfb4cb4b27b60cada3b05e` владелец создал отдельную БД `lab_jobs_test_a62cc1792f5d48dcb83efa21829a8ef0`. Задача `jobs-sql-integration-test` выполнила `TestPostgresOutbox` с `-tags integration -count=1 -race`: `PASS`, тест 0.17 секунды, пакет 1.693 секунды; не `SKIP` и не результат из кэша. Проверены повтор миграций, параллельные enqueue/claim, контракт события v1, восстановление просроченного lease, отказ старого токена, отсутствие регрессии статуса и откат заявки при ошибке INSERT outbox. Это SQL-тест с настоящей PostgreSQL, не опыт нескольких publisher в Kubernetes.

После теста PostgreSQL и publisher остались `1/1 Ready`, restarts `0`, прежний PostgreSQL PVC/PV сохранён. Основная БД сохранила три опубликованных job, pending `0`, effects `0`; smoke повторно прошёл на API `4345b1078c40`. Окружение возвращено блоком finally. Владелец удалил только указанную тестовую БД командой dropdb без force; SELECT её имени из pg_database вернул пустой результат. Основная БД, задания, topics и PVC не удалялись.

Все проверки выполнял владелец. `Passed` относится к перечисленным ветвям второй итерации. Исходная ошибка Helm и ручной обход сохранены выше, а не заменены успешным повтором.

## Диагностика

Сопоставить время HTTP, job/event ID, attempts, next_retry_at, lease_until, код ошибки, Pod UID/revision, broker ack partition/offset и PostgreSQL-отметку. Не публиковать данные Secret и полный payload. HTTP-проверка использует локальный port-forward и не доказывает сетевой путь обычного Pod через Service.

## Причина

Проверяемая гипотеза: commit PostgreSQL обеспечивает сохранение приёма независимо от немедленной публикации. Lease ограничивает владение, токен ограждает поздние изменения, broker ack предшествует отметке публикации. Фактическая причина возможных отклонений устанавливается только по результатам.

## Восстановление

Канонический release publisher, одна реплика и готовность восстановлены. Первоначальная команда потребовала ручного возврата bootstrap; повтор исправленной Go-команды после commit `e84d7a25fe15` прошёл автоматически, без force и удаления Deployment. Журналы и строки outbox сохранены; отдельная тестовая БД удалена. При откате приложения новые таблицы оставить: миграция добавляет объекты и не требует разрушительного отката.

## Поведение Kubernetes

Ожидаются отдельный Deployment publisher, штатный SIGTERM при остановке, потеря readiness при недоступной зависимости без liveness-перезапуска. Kubernetes не обеспечивает атомарность PostgreSQL/Kafka и не знает, что означает опубликованная заявка.

## Поведение приложения

Ожидаются `202` после commit, `409` при конфликте, сохранённый retry с ограничением времени попытки и задержки, ack-before-mark и восстановление просроченного claim. SIGTERM не разрешает новый claim, но позволяет закончить принятую попытку в бюджете Pod.

## Что результат сможет подтвердить

Подтверждены конкурентная идемпотентность HTTP, одна строка outbox на job, публикация после broker ack, сохранение заявки при недоступном для publisher адресе Kafka и публикация прежнего события после автоматического восстановления. Изолированный SQL integration-тест подтвердил атомарный откат, конкурентный claim, lease и ограждение старого токена в пределах выполненного теста. Очистка тестовой БД и регрессия рабочего API выполнены.

## Что результат не подтверждает

Worker, consumer groups, offsets потребителя, эффекты, DLQ/retry обработки, CronJob, HA, потерю диска и exactly-once delivery. Замена адреса клиента не является остановкой Kafka broker; отдельный broker outage ещё предстоит. Падение после broker ack до PostgreSQL-отметки и дубликаты публикации требуют отдельного опыта.

## Остаточный риск

Один broker и local-path диски не отказоустойчивы. Копия клиентского Secret не обновляется автоматически при ротации Strimzi. Событие может быть опубликовано повторно при неопределённом результате ack/DB commit. При длительной недоступности publisher backlog растёт; admission/backpressure и предел объёма outbox пока не реализованы. Обработка не успевших попасть к worker событий ограничена retention Kafka.
