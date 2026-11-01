# Kafka worker: конечные повторы и DLQ

Сценарий имеет [Passed](../verification/kafka-retry-dlq.md) для локального выполнения 25–26 октября 2026 года на worker `de6f15ba5c4d`, включая финальную регрессию и очистку. Контракт — [Kafka и обработка заданий](../design/kafka-event-processing.md). Команды ниже предназначены для воспроизведения с собственными доказательствами. Работать в основном kind, не удаляя прежние задания, topics, offsets, Secret или PVC. При ошибке остановить последовательность и сохранить диагностику.

## 1. Проверки и подготовка

```powershell
go test ./...
go test -race ./...
go vet ./...
task worker-helm-lint
task worker-helm-render
```

Повторить `task worker-sql-integration-test` по [процедуре отдельной SQL-БД](kafka-lab.md#отдельный-sql-integration-тест). Задача теперь запускает оба TestPostgresWorker*, включая TestPostgresWorkerReliability; нужны два настоящих PASS, не SKIP. После успешного результата удалить только свою временную БД, подтвердить её отсутствие и восстановить переменные окружения. Сохранить выводы, затем владелец фиксирует изменения. Перед сборкой записать `git status --porcelain` и `git rev-parse HEAD`; дерево должно быть чистым.

В каждом новом окне выбрать локальный kubectl 1.35.9, если системный клиент несовместим с сервером 1.34:

```powershell
$env:Path = "$(Resolve-Path .cache\kubectl\v1.35.9);$env:Path"
kubectl config use-context kind-kubernetes-application-lab
```

По одной команде:

```powershell
task worker-stop
task image-build
task db-migrate
task db-migrate-status
task worker-build
task kafka-bootstrap
task worker-dlq-secret-create
task worker-dlq-secret-create
task worker-deploy
task worker-status
task worker-reliability-status
```

Новый API-образ нужен Job миграции, который содержит `003_worker_reliability.sql`; запущенные API/publisher можно оставить на прежних образах, формат события не менялся. Сохранить журнал напечатанного migration Job и наличие `003_worker_reliability` в outbox-status. Прежние миграции 001/002 не меняются. Kafka bootstrap согласует существующие CR и добавляет отдельного read-only пользователя lab-dlq-inspector; он не удаляет topics или данные. Повтор создания Secret должен сообщить о совпадении, без печати ключей. Новый worker не читает работу до появления схемы 003 и остаётся неготовым при её отсутствии.

В отдельном окне оставить API port-forward, если он ещё не работает:

```powershell
kubectl --context kind-kubernetes-application-lab port-forward deployment/lab-api 18081:8080 -n kubernetes-application-lab --address 127.0.0.1
```

Журналы worker сохранять до удаления его Pod; новая диагностическая команда сохраняет Pod до явной очистки. Выводы можно записывать через Tee-Object в `.local-kafka/`; файлы не входят в Git. Не считать Readiness или успешный Helm upgrade доказательством обработки или DLQ.

## 2. Две ошибки, затем успешная третья попытка

```powershell
task worker-stop
task job-enqueue
task job-status
task outbox-status
task worker-retry-success
task worker-deploy
```

До настройки ошибки убедиться, что именно новая заявка published и effects=0. Настройка требует отсутствия worker Pod и выбирает job из locator, не меняя его содержимое. Не выполнять другой enqueue до сохранения результата.

```powershell
kubectl --context kind-kubernetes-application-lab logs deployment/lab-worker -n kubernetes-application-lab -c worker --timestamps
task job-status
task outbox-status
task worker-reliability-status
task worker-group-status
```

При необходимости повторить наблюдение после завершения попыток. Нужны две worker_retry_scheduled (failure_number 1 и 2), затем эффект и offset commit. SQL: failure_count=2, failures_before_success=2, succeeded, effects=1, одна outbox-строка, для этого задания нет DLQ. Committed offset source partition достигает минимум source offset+1. Сравнивать конкретные job/event/source, не общие счётчики.

## 3. Три ошибки и DLQ

```powershell
task worker-stop
task job-enqueue
task outbox-status
task worker-retry-exhaust
task worker-deploy
```

Снова настройка относится только к новой опубликованной заявке без эффекта. После обработки:

```powershell
task job-status
task worker-reliability-status
task worker-dlq-read
task worker-group-status
kubectl --context kind-kubernetes-application-lab logs deployment/lab-worker -n kubernetes-application-lab -c worker --timestamps
```

Ожидаются failure_count=3, effects=0, failed/error_code=retry_exhausted и completed_at только после успешной DLQ. Журнал: три ошибки → worker_dlq_broker_ack → worker_dlq_recorded → worker_offset_committed. SQL-квитанция хранит исходные координаты и отдельные DLQ partition/offset; worker-dlq-read напрямую читает именно подтверждённую DLQ-запись и сверяет envelope и key с БД, не вступая в группу. Сохранить результаты до следующего enqueue.

## 4. Ошибка публикации в DLQ и восстановление

```powershell
task worker-stop
task job-enqueue
task outbox-status
task worker-retry-exhaust
task worker-dlq-unreachable
```

Подождать worker_dlq_publish_failed после трёх управляемых ошибок, затем сохранить журнал, job-status, worker-reliability-status и worker-group-status. Ожидаются failure_count=3, effects=0, processing без completed_at, pending DLQ-квитанция без published_at/dlq_offset. Исходный offset не должен быть подтверждён (committed <= source offset либо -1). Liveness остаётся проверкой процесса; readiness не проверяет отдельный DLQ producer. Команда меняет только адрес отдельного DLQ producer, не останавливает broker и не меняет обычный consumer bootstrap.

После сохранения исходного состояния:

```powershell
task worker-stop
task worker-deploy
task job-status
task worker-reliability-status
task worker-dlq-read
task worker-group-status
```

Обычные stop/deploy снимают неверный адрес через Helm. Дождаться failed после broker ack и подтверждения source offset. Счётчик остаётся 3, новых управляемых попыток работы нет; та же исходная запись доставлена в DLQ. Если DLQ ещё не появилась, повторить снимки после её публикации. Новый record с другим source offset не считать восстановлением исходного.

## 5. Некорректные записи без пригодного задания

Восстановленный обычный worker должен работать. Выполнять один режим за раз:

```powershell
task worker-poison MODE=invalid-json
task worker-reliability-status
task worker-dlq-read KEY=latest-invalid
task worker-group-status
```

После появления квитанции сверить её source partition/offset с worker_invalid_published именно текущего запуска. Затем теми же командами повторить MODE=unknown-version, MODE=unknown-type, MODE=missing-job, MODE=oversized. Не переходить к следующему режиму до чтения текущей DLQ: latest-invalid выбирает последнюю подтверждённую SQL-квитанцию, которая при незавершённой обработке ещё может быть от прошлого опыта.

Для missing-job используется корректный event с отсутствующим UUID, ожидается inconsistent_event; прочие режимы дают invalid_event. У этих DLQ-квитанций job_id отсутствует, задания/эффекты в БД не создаются. Envelope содержит source, reason, размеры и SHA-256; исходные ключ и body туда не копируются. Все source offsets подтверждаются только после DLQ. После каждого отказа источник должен продолжать обрабатываться; финальный обычный enqueue проверяет отсутствие блокировки.

## 6. Возврат и очистка

```powershell
task worker-stop
task worker-deploy
task job-enqueue
task job-status
task outbox-status
task worker-group-status
task publisher-status
task db-status
task smoke
```

Дождаться succeeded с одним эффектом для финальной обычной заявки, одной готовой реплики worker и Stable группы. Убедиться, что отказ DLQ отключён, прежние PVC/PV сохранены. Сохранить диагностику до очистки:

```powershell
task worker-diagnostics-cleanup
```

SQL-квитанции, retry state, задания, DLQ и исходные topics остаются для осмотра. После сохранения доказательств закрыть ненужные port-forward через Ctrl+C. Отдельный опыт реального broker outage, авария между DLQ ack и SQL commit, обработка под принудительным rebalance и ротация сертификатов этим сценарием не подтверждаются.
