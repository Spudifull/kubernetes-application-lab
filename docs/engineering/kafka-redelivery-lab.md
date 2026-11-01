# Kafka worker: авария перед подтверждением offset

Сценарий имеет [Passed](../verification/kafka-redelivery.md) для локального опыта 25 октября 2026 года на `011b2e8c144c`, включая восстановление и очистку. Проверяемый контракт — [Kafka и обработка заданий](../design/kafka-event-processing.md). Команды ниже предназначены для воспроизведения; retry/DLQ сюда не входят.

## 1. Предварительные проверки

В PowerShell из корня репозитория, по одной команде, продолжать только после успешного завершения:

```powershell
go mod tidy
go test ./...
go test -race ./...
go vet ./...
task worker-helm-lint
task worker-helm-render
```

Обновления зависимостей с другого компьютера включены в рабочее дерево; `go mod tidy` согласует суммы с текущим графом. Отдельный SQL integration-тест worker повторить по [порядку проверки worker](kafka-worker-lab.md#1-предварительные-проверки) на изолированной БД: изменился pgx. Нужен настоящий PASS, не SKIP.

После проверок и фиксации изменений владельцем сохранить `git status --porcelain` и `git rev-parse HEAD`. Собирать образ из чистого дерева. Агент не создаёт commit автоматически. API/publisher можно оставить на прежних образах: формат события и миграции 001/002 не меняются; их build revision записать отдельно.

```powershell
task kafka-status
task db-status
task publisher-status
task worker-build
task worker-secret-create
task worker-deploy
task worker-group-status
```

Если требуется локальный kubectl 1.35.9, добавить `.cache/kubectl/v1.35.9` в Path по прежней инструкции. Все команды ниже используют основной `kind-kubernetes-application-lab`, Kubernetes 1.34 и namespace `kubernetes-application-lab`. Нельзя направлять опыт в другой кластер.

## 2. Остановить consumer и создать новую заявку

В отдельном окне открыть API port-forward, если он ещё не работает:

```powershell
kubectl --context kind-kubernetes-application-lab port-forward deployment/lab-api 18081:8080 -n kubernetes-application-lab --address 127.0.0.1
```

В основном окне:

```powershell
task worker-stop
task job-enqueue
task job-status
task outbox-status
task worker-group-status
```

Не продолжать, пока новая заявка не опубликована (`published_at`, `kafka_partition`, `kafka_offset` непустые), worker Pod отсутствуют, группа Empty. Сохранить исходные offsets и job/event ID. У заявки ещё нет эффекта. `job-enqueue` сохраняет locator в `.local-kafka/job-locator.json`; не выполнять другой enqueue до завершения опыта. Дождаться публикации можно повторением `outbox-status`.

## 3. Включить аварию для сохранённого job ID

```powershell
task worker-deploy-crash
task worker-status
```

Команда требует replicas=0 и отсутствие worker Pod, загружает новый образ и запускает одну реплику с выбранным job ID. Helm намеренно не ждёт готовности: успешное выполнение команды означает только принятие конфигурации. Завершение процесса с кодом 86 — ожидаемый отказ.

Сохранить имя единственного Pod и дождаться аварии:

```powershell
$crashPod = kubectl --context kind-kubernetes-application-lab get pods -n kubernetes-application-lab -l 'app.kubernetes.io/name=lab-worker,app.kubernetes.io/instance=lab-worker' -o jsonpath='{.items[0].metadata.name}'
if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($crashPod)) { throw 'Worker Pod not found' }
kubectl --context kind-kubernetes-application-lab wait "pod/$crashPod" -n kubernetes-application-lab '--for=jsonpath={.status.containerStatuses[0].lastState.terminated.exitCode}=86' --timeout=180s
```

После успешного wait сохранить доказательства **до любого stop/deploy**, которые удалят Pod:

```powershell
kubectl --context kind-kubernetes-application-lab get "pod/$crashPod" -n kubernetes-application-lab -o yaml | Tee-Object .local-kafka/redelivery-crash-pod.yaml
kubectl --context kind-kubernetes-application-lab logs "pod/$crashPod" -n kubernetes-application-lab -c worker --previous --timestamps | Tee-Object .local-kafka/redelivery-crash.log
task job-status | Tee-Object .local-kafka/redelivery-job-before.txt
task outbox-status | Tee-Object .local-kafka/redelivery-sql-before.txt
task worker-group-status | Tee-Object .local-kafka/redelivery-group-before.txt
```

Ожидаются `succeeded`, один SQL-эффект, непустые `completed_at` и result. Для target partition committed offset должен быть не больше offset исходной записи; отсутствие committed offset также допустимо, но `lag=null` не считать нулём.

Журнал содержит `worker_effect_committed` → `worker_crash_before_offset_commit` для одного event и координат Kafka. При автоматическом перезапуске тот же target снова вызывает аварию перед commit: поэтому `--previous` может уже показывать `worker_duplicate_ignored` → авария вместо первого эффекта. Это допустимо при наличии SQL-снимка и тех же topic/partition/offset. Ни один target не должен иметь `worker_offset_committed` при включённом отказе. Новый PID/перезапуск не означает повторную публикацию publisher.

Если ожидание или получение журнала завершились ошибкой, не засчитывать сценарий. Сохранить доступную диагностику и восстановить worker следующим разделом. Не удалять группу и не сбрасывать offsets.

В выполненном опыте worker-group-status во время PreparingRebalance завершился `invalid group assignment`, не прочитав offsets. До включения обычного worker удалось получить достаточный снимок после worker-stop: committed offset target ещё отсутствовал. При таком отказе сначала сохранить журналы аварии и SQL, выполнить worker-stop и повторить worker-group-status, сохранив результат отдельно. Координатор может ещё показывать прежнего member после исчезновения Pod: не считать это доказательством живого процесса или требовать немедленного Empty. Не выполнять worker-deploy до получения снимка неподтверждённого offset; иначе исходное окно измерения потеряется.

## 4. Отключить аварию и проверить повторную доставку

После сохранения доказательств:

```powershell
task worker-stop
task worker-deploy
task worker-status
kubectl --context kind-kubernetes-application-lab logs deployment/lab-worker -n kubernetes-application-lab -c worker --timestamps | Tee-Object .local-kafka/redelivery-recovered.log
task job-status | Tee-Object .local-kafka/redelivery-job-after.txt
task outbox-status | Tee-Object .local-kafka/redelivery-sql-after.txt
task worker-group-status | Tee-Object .local-kafka/redelivery-group-after.txt
task publisher-status
task db-status
task smoke
```

Обычные `worker-stop` и `worker-deploy` явно снимают отказ через Helm. Если журнал ещё не содержит нужной обработки, повторить чтение журнала и снимки после неё. Нужны `worker_received` → `worker_duplicate_ignored` → `worker_offset_committed` для **того же** event ID, topic, partition и offset. Committed offset должен достигнуть минимум исходного offset + 1. Новый Kafka offset означал бы другую запись и не доказывал бы аварийную повторную доставку.

Сравнить SQL/HTTP до и после: один эффект, неизменные result и `completed_at`, одна outbox-строка и прежние publisher partition/offset. Финально одна Ready-реплика worker, стабильная группа и нулевой lag обработанной partition после остановки нагрузки. Пустые partitions могут не иметь committed offset; lag=null не означает измеренный нулевой lag. PostgreSQL, API и publisher доступны, прежние PVC/PV сохранены.

Сохранить журналы диагностических Pod и выполнить:

```powershell
task worker-diagnostics-cleanup
```

Port-forward завершить через Ctrl+C, когда он больше не нужен. Локальные файлы `.local-kafka/` исключены из Git: существенные значения нужно перенести в отчёт вместе с SHA, образами, Pod UID и временем. Только после выполнения оценить статус отчёта.
