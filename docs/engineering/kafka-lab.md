# Первая итерация Kafka

Состояние: инфраструктура, HTTP/outbox/publisher и третья итерация worker имеют `Passed` в пределах [bootstrap-отчёта](../verification/kafka-bootstrap.md), [outbox-отчёта](../verification/kafka-outbox.md) и [worker-отчёта](../verification/kafka-worker.md). История первых отказов сохранена. Полный Milestone 7 ещё не закрыт; команды реализованы в Taskfile и Go CLI, без обязательного host-shell сценария.

## Предварительные проверки

1. Выбрать основной контекст `kind-kubernetes-application-lab`. Не создавать новый кластер для Kafka и не удалять имеющиеся.
2. Проверить `kubectl version`: клиент не более чем на одну minor-версию старше/младше сервера. Для сервера `1.34` допустимы клиенты `1.33`–`1.35`; используйте поддерживаемый patch-релиз выбранной ветки. Команды установки блокируют наблюдавшийся skew `1.36`/`1.34`.
3. Выполнить `task kafka-preflight` и оценить состояние, а не только итоговый exit code. В предоставленном 25 октября снимке основной узел Ready, pressure отсутствует, requests заняты на `1175m/778Mi`, API и PostgreSQL готовы, PVC Bound. Второй kind-кластер разделяет Docker VM: его потребление нельзя выводить только из allocatable основного узла.
4. Владелец выполняет `go test ./...`, `go test -race ./...`, `go vet ./...`, затем `task kafka-strimzi-render` и `task kafka-probe-build`. Проверить сформированные RBAC, область наблюдения, ресурсы, digest образов, PSA и только внутренние listeners. Эти проверки ещё не выполнены агентом.
5. После исправлений зафиксировать изменение самостоятельно и показать чистый Git SHA перед каноническим runtime-прогоном. Сборка probe не заменяет запись SHA; сохранить ссылку `.local-kafka/probe-image-ref`.

## Установка и smoke

Выполнять по одной команде; при любой ошибке сохранить вывод и остановиться, не переходя к следующему шагу.

```text
task kafka-strimzi-install
task kafka-strimzi-status
task kafka-bootstrap
task kafka-status
task kafka-probe-build
task kafka-smoke
```

Operator — отдельный Helm release `lab-strimzi` в Kafka namespace. Он регистрирует cluster-scoped CRD/RBAC, хотя наблюдает только свой namespace. Bootstrap выполняет server-side dry-run перед применением CR и ждёт Ready. Он не изменяет release API или PostgreSQL. Dry-run CR возможен только после регистрации соответствующих CRD.

Сохранить Git SHA, Helm history Operator, версии, image references/imageID, Kafka/NodePool status, node ID, Pod UID, PVC/PV, конфигурацию обоих рабочих topics и smoke topic. События `kafka_smoke_written` и `kafka_smoke_read` должны совпасть по key, topic, partition и offset. Probe использует отдельного KafkaUser с доступом только к диагностическому topic и cluster IdempotentWrite; секреты монтируются внутри того же namespace и не печатаются.

## Штатная замена broker Pod

```text
task kafka-smoke-write
task kafka-broker-restart
task kafka-smoke-read
task kafka-status
```

Не выполнять между write и read другой smoke, который заменит локальный locator. Опыт закончить до истечения часовой retention. Restart удаляет только единственный найденный broker Pod без force/grace=0; до удаления требует Bound PVC и один node ID, после — новый готовый Pod UID и прежние PVC UID/PV/node ID. PVC не удаляется. При ошибке процесс оставляет состояние для диагностики.

Ожидается чтение **прежней**, а не вновь опубликованной записи. Это доказывает локальное сохранение при штатной замене Pod с прежним томом, но не HA, backup, потерю диска, worker redelivery или offsets consumer group.

## Диагностика и очистка

При проблеме сначала `task kafka-strimzi-status` и `task kafka-status`. Для неуспешного probe сохранить его имя, describe и logs; Pod остаётся после выполнения. Различать Pending, OOMKilled, TLS trust/hostname, ACL, отсутствие topic и истечение retention. При незаполненном PVC нельзя делать вывод о потере Kafka-сообщений: процесс мог ещё не запуститься.

После сохранения доказательств `task kafka-smoke-cleanup` удаляет только Pod с меткой диагностического клиента. Topics, outbox/задания, Kafka/PostgreSQL PVC, Secrets, namespace, Operator и кластеры не входят в cleanup. Основной API и PostgreSQL проверить прежними задачами; статусы Milestones 1–6 не меняются из-за наличия Kafka.

## Итерация 2 — HTTP и transactional outbox

Состояние — [Passed для второй итерации](../verification/kafka-outbox.md). В этой итерации worker не запускался: её итог — `published`, не `succeeded`; новый worker проверяется отдельно. Основной контракт, сроки, лимиты и границы — [в design](../design/kafka-event-processing.md#заявка-и-transactional-outbox). Команды выполняет владелец. При неожиданном отказе сохранить вывод; не пересоздавать кластер и не удалять PVC.

### Предварительные проверки

В рабочем каталоге репозитория:

```powershell
go test ./...
go test -race ./...
go vet ./...
task jobs-helm-lint
task jobs-helm-render
task db-helm-lint
```

Это ещё не проверка PostgreSQL/Kafka. После исправлений владелец создаёт commit и проверяет чистоту дерева. Затем записать SHA и собрать **новые** API и publisher, не использовать прежний API без маршрутов jobs:

```powershell
git status --short
git rev-parse HEAD
task image-build
task publisher-build
```

Сохранить обе content-identity ссылки из вывода. В каждом новом окне PowerShell активировать локальный kubectl `1.35.9`, если системный остался `1.36.1`:

```powershell
$env:Path = "$(Resolve-Path .cache\kubectl\v1.35.9);$env:Path"
kubectl config use-context kind-kubernetes-application-lab
```

### Окно 1 — подготовка схемы и API

```powershell
task kafka-status
task db-preflight
task db-deploy
task db-migrate
task db-migrate-status
```

`db-deploy` обновляет также разрешение publisher в NetworkPolicy. Изменение версии PostgreSQL chart может штатно заменить Pod; дождаться `Ready`, PVC должен остаться прежним. Сохранить имя migration Job и его журнал: в таблице миграций должна появиться `002_jobs_outbox`. Затем повторить `task db-migrate` и сохранить успешный результат повторного применения.

```powershell
task jobs-api-deploy
task publisher-secret-create
task publisher-secret-create
```

Второе создание Secret должно сообщить о совпадении, без изменения ключей. Не показывать `kubectl get secret -o yaml` и локальные пароли. Publisher пока **не развёртывать**; если он установлен предыдущей попыткой, сначала `task publisher-stop`.

### Окно 2 — соединение с API

```powershell
kubectl --context kind-kubernetes-application-lab port-forward deployment/lab-api 18081:8080 -n kubernetes-application-lab --address 127.0.0.1
```

Оставить окно открытым. При замене API Pod этот port-forward потребуется запустить заново. Данный путь не доказывает Kubernetes DNS или обычную маршрутизацию через ClusterIP.

### Окно 1 — конкурентный приём и отложенная публикация

```powershell
task jobs-concurrent-test
task job-status
task outbox-status
```

Ожидаются восемь `202` с одним ID, затем `409` для иного содержимого. В SQL-выводе по сохранённому ключу должна быть одна заявка и `outbox_rows=1`, `effects=0`, статус `accepted`, published_at отсутствует. CLI сохраняет последний job locator в `.local-kafka/job-locator.json`; новый enqueue заменяет только этот локальный указатель, не строки БД.

```powershell
task publisher-deploy
task publisher-status
```

### Окно 3 — журнал publisher

```powershell
kubectl --context kind-kubernetes-application-lab logs -f --timestamps deployment/lab-publisher -n kubernetes-application-lab
```

Сохранить `publisher_started` с ревизией, `outbox_claimed`, `outbox_broker_ack`, `outbox_published` с одним event/job ID. Ack содержит partition и offset. При замене publisher Pod журнал следует открыть заново; сначала сохранить журнал старого процесса.

В окне 1 повторить `task job-status` и `task outbox-status`: ожидаются `published`, время публикации и совпадающие partition/offset. Если публикация не мгновенная, дождаться события в окне 3 и только затем снимать итог. Пустой `result` и `effects=0` сейчас правильны.

### Недоступный адрес Kafka для publisher

Сначала сохранить успешный журнал. Затем в окне 1:

```powershell
task publisher-unreachable-start
task job-enqueue
task job-status
task publisher-status
task outbox-status
```

Команда сначала полностью останавливает прежний publisher, затем запускает процесс с закрытым адресом `127.0.0.1:19093` **внутри его Pod**. Kafka broker не останавливается, PVC и topics не меняются. Старый готовый процесс не должен продолжать публикацию. Перезапустить окно 3 для нового Pod. За срок до 25 секунд ожидается `outbox_publish_failed`; повторный SQL-снимок должен показать attempts и `kafka_publish_failed`, при этом published_at остаётся пустым. Следующую попытку определяет next_retry_at; не требовать увеличения attempts каждую секунду. API должен вернуть `202` даже при неготовом publisher.

Сохранить ошибку и pending-строку до восстановления, затем:

```powershell
task publisher-unreachable-restore
task publisher-status
task job-status
task outbox-status
task kafka-status
task db-status
task smoke
```

Восстановление сначала читает bootstrap из сохранённых значений Helm release и возвращает его через `kubectl set env`, затем выполняет обычный Helm upgrade. Первоначальный сценарий на `4345b1078c40` потребовал ручного возврата адреса из-за конфликта с менеджером `kubectl-set`; исправленная задача проверена без ручного обхода на `e84d7a25fe15`, включая повторное выполнение. API/publisher остались на чистых образах `4345b1078c40`; SHA сценария и build revision процессов указываются раздельно.

В новом журнале publisher ожидается публикация **того же** event/job ID. Успешный ответ после восстановления не заменяет снимок строки до него. При необходимости повторить `task job-enqueue KEY=<исходный_ключ>`: это должен быть тот же job ID; ключ брать из `job_enqueue_response`, не из поля event ID. Реальная остановка брокера и восстановление consumer будут отдельными опытами.

### Отдельный SQL integration-тест

В файле `internal/jobs/store_integration_test.go` используется build tag `integration`. Владелец заранее создаёт отдельную пустую локальную БД с именем `lab_jobs_test_<уникальный_суффикс>`, задаёт для неё `LAB_DB_HOST`, `LAB_DB_PORT`, `LAB_DB_NAME`, `LAB_DB_USER`, `LAB_DB_PASSWORD` и `LAB_JOBS_INTEGRATION=1`. Пароль не сохранять в Git, командной строке процесса или отчёте. Не направлять этот тест на основную базу; активных API/publisher для тестовой БД быть не должно.

```powershell
task jobs-sql-integration-test
```

Для текущего kind можно использовать отдельную БД того же PostgreSQL, не меняя основную. В дополнительном окне оставить соединение открытым:

```powershell
kubectl --context kind-kubernetes-application-lab port-forward pod/lab-postgres-0 18085:5432 -n kubernetes-application-lab --address 127.0.0.1
```

В окне запуска теста выполнить блок ниже. Он создаёт уникальную тестовую базу, получает пароль только в память процесса и восстанавливает прежние переменные окружения после теста. База остаётся для осмотра; автоматического удаления нет. Не выводить содержимое Secret, `$encodedPassword` или переменные окружения.

```powershell
$ctx = 'kind-kubernetes-application-lab'
$ns = 'kubernetes-application-lab'
$testDb = 'lab_jobs_test_' + [guid]::NewGuid().ToString('N')
kubectl --context $ctx exec pod/lab-postgres-0 -n $ns -- createdb -U lab $testDb
if ($LASTEXITCODE -ne 0) { throw 'Создание отдельной тестовой БД не прошло' }
"test_database=$testDb"

$names = @('LAB_DB_HOST','LAB_DB_PORT','LAB_DB_NAME','LAB_DB_USER','LAB_DB_PASSWORD','LAB_JOBS_INTEGRATION')
$savedEnv = @{}
foreach ($name in $names) { $savedEnv[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
try {
    $encodedPassword = kubectl --context $ctx get secret lab-postgres-credentials -n $ns -o 'jsonpath={.data.password}'
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($encodedPassword)) { throw 'Не удалось получить пароль тестового подключения' }
    $env:LAB_DB_PASSWORD = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($encodedPassword.Trim()))
    $encodedPassword = $null
    $env:LAB_DB_HOST = '127.0.0.1'
    $env:LAB_DB_PORT = '18085'
    $env:LAB_DB_NAME = $testDb
    $env:LAB_DB_USER = 'lab'
    $env:LAB_JOBS_INTEGRATION = '1'
    task jobs-sql-integration-test
    if ($LASTEXITCODE -ne 0) { throw 'SQL integration-тест не прошёл; сохранить вывод и осмотреть тестовую БД' }
}
finally {
    foreach ($name in $names) { [Environment]::SetEnvironmentVariable($name, $savedEnv[$name], 'Process') }
    $encodedPassword = $null
}
```

Проверить, что тест выполнился, а не был пропущен. На чистой ревизии `e84d7a25fe15` получен `PASS` с настоящей PostgreSQL, затем отдельная тестовая БД удалена; подробности в [отчёте](../verification/kafka-outbox.md). Тест применяет миграции дважды, выполняет конкурентные enqueue/claim, проверяет lease и временно добавляет отрицательное SQL-ограничение только в изолированной БД. Собственные строки и ограничение удаляются при штатном завершении; сам тест не удаляет БД и не публикует Kafka-события. При аварии осмотреть тестовую БД, не удаляя основную.

### Завершение и возврат

Вернуть publisher к одной Ready-реплике с каноническим bootstrap, сохранить журналы и SQL-снимки, завершить port-forward через Ctrl+C. Обычный сценарий не удаляет задания или outbox. Если требуется временно отказаться от jobs-профиля, остановить publisher и выполнить `task db-api-deploy`; таблицы оставить. Возврат базового stateless API — отдельная регрессия, не удаление БД. Crash между Kafka ack и DB mark, несколько publisher в Kubernetes и реальный broker outage пока не имеют доказательства и не получают `Passed` автоматически.

## Третья итерация — worker

Третья итерация worker [Passed](../verification/kafka-worker.md) на чистом образе `858f2ee181e4`: одиночная заявка, дубликат, batch с двумя consumers, завершение принятой работы при SIGTERM, восстановление и очистка. Для воспроизведения следовать [порядку проверки по окнам](kafka-worker-lab.md). Это не доказательство аварийной redelivery или закрытия всего Milestone 7.

До начала новых опытов сохранять восстановленное состояние API/publisher/PostgreSQL, прежние задания и PVC. Не удалять topics и не увеличивать retention ради проверки старых событий. Retry/DLQ, аварийные точки, реальный broker outage и CronJob проверены последующими отдельными опытами; ссылки и границы собраны в [каталоге результатов](../verification/README.md).
