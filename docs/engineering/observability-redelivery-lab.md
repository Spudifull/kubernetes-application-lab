# Crash/redelivery с текущей наблюдаемостью

Контракт обработки — [Kafka](../design/kafka-event-processing.md), трассировки — [application tracing](../design/application-tracing.md). [Совместная проверка](../verification/worker-crash-observability.md) имеет Passed; [старый опыт](../verification/kafka-redelivery.md) сохранён отдельно.

## Подготовка

Основной kind/Kubernetes 1.34, kubectl 1.35.9, fixed worker=1, API/publisher/worker в диагностическом tracing profile, PostgreSQL/Kafka/Collector/Loki исправны, pending/lag=0. Использовать записанные образы; новая сборка приложения не требуется. Изменён labctl: новый deploy-crash-tracing использует существующий crash hook с tracing profile. Он требует остановленного worker и не ждёт readiness аварийного Pod.

Владелец выполняет:

```powershell
$env:Path = "$(Resolve-Path .cache\kubectl\v1.35.9);$env:Path"
$ctx = 'kind-kubernetes-application-lab'
$ns = 'kubernetes-application-lab'
go test -count=1 -race ./cmd/labctl ./cmd/worker ./internal/worker
go vet ./cmd/labctl ./cmd/worker ./internal/worker
git diff --check
git rev-parse HEAD
git status --short
task tracing-status
task outbox-status
task worker-group-status
```

Продолжать только при успешных проверках. API forward 18081 должен работать в отдельном окне. Не создавать другие задания до сохранения результатов: используется последний locator. Сохранить job/event/trace ID новой постановки.

## Зафиксировать промежуток SQL commit → отсутствие Kafka commit

```powershell
task worker-stop
if ($LASTEXITCODE -ne 0) { throw 'Worker не остановлен' }
task worker-long-job
if ($LASTEXITCODE -ne 0) { throw 'Задание не создано' }
task outbox-status
task worker-group-status
```

Нужны опубликованная новая outbox row, effects=0, известные source topic/partition/offset и остановленный worker. Восемь секунд работы увеличивают окно наблюдения первого процесса; это не гарантирует доставку всех его spans.

```powershell
task tracing-worker-crash
if ($LASTEXITCODE -ne 0) { throw 'Аварийный профиль не применён' }
$crashPod = kubectl --context $ctx get pods -n $ns -l 'app.kubernetes.io/name=lab-worker,app.kubernetes.io/instance=lab-worker' -o jsonpath='{.items[0].metadata.name}'
if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($crashPod)) { throw 'Worker Pod не найден' }
kubectl --context $ctx wait "pod/$crashPod" -n $ns '--for=jsonpath={.status.containerStatuses[0].lastState.terminated.exitCode}=86' --timeout=180s
if ($LASTEXITCODE -ne 0) { throw 'Ожидаемая авария 86 не зафиксирована; сохрани состояние' }
kubectl --context $ctx get "pod/$crashPod" -n $ns -o json | Tee-Object .local-kafka/observability-crash-pod.json
kubectl --context $ctx logs "pod/$crashPod" -n $ns -c worker --previous --timestamps | Tee-Object .local-kafka/observability-crash-previous.log
task job-status | Tee-Object .local-kafka/observability-crash-job-before.txt
task outbox-status | Tee-Object .local-kafka/observability-crash-sql-before.txt
task worker-group-status | Tee-Object .local-kafka/observability-crash-group-before.txt
task logging-query
task application-trace-inspect
```

Сохранить данные **до stop/deploy**, удаляющих аварийный Pod. Нужны succeeded/effects=1 и completed_at/result в SQL, при этом committed source offset не больше исходного offset (или commit ещё отсутствует), lag>0 либо честное unknown. Журнал должен содержать worker_crash_before_offset_commit для целевого job/event/source. При CrashLoop очередной --previous может уже показывать duplicate_ignored → crash вместо первого effect: source coords и SQL остаются опорой. Отсутствующий target commit в одном ограниченном логе сам по себе недостаточен; обязательна сверка offsets.

## Восстановить тот же record без второго SQL-эффекта

```powershell
task worker-stop
if ($LASTEXITCODE -ne 0) { throw 'Аварийный worker не остановлен' }
task tracing-worker-deploy
if ($LASTEXITCODE -ne 0) { throw 'Worker не восстановлен' }
Start-Sleep -Seconds 45
kubectl --context $ctx logs deployment/lab-worker -n $ns -c worker --timestamps | Tee-Object .local-kafka/observability-crash-recovered.log
task worker-status
task job-status
task outbox-status
task worker-group-status
task prometheus-query QUERY=worker-duplicates
task logging-query
task application-trace-inspect
```

Нужны worker_received → worker_duplicate_ignored → worker_offset_committed для того же event/topic/partition/offset, SQL effects=1 и неизменные completed_at/result/outbox coords. Committed должен достигнуть source offset+1, группа Stable/один участник, lag=0. Новый offset означал бы другую запись и не является доказательством этой redelivery.

В Loki искать по job ID, различать Pod, span_id и исходные координаты. В Tempo открыть trace_id исходного POST. Сравнить сохранённые попытки по span ID/parent ID, события duplicate_observed и commit восстановленной попытки. Abrupt os.Exit не выполняет deferred span End/flush: часть первой попытки может отсутствовать или иметь неэкспортированный parent. application-trace-inspect сохраняет полный ответ Tempo и inspected-spans.json, перечисляет отсутствующие parent ID, но не утверждает успешную SQL/offset цепочку. Обычный application-trace-read требует SQL completion и commit в одной попытке и для duplicate-only восстановления закономерно может не пройти. Пропуски не дорисовывать и не считать отсутствие span доказательством отсутствия действия.

## Отдельно проверить рост counter на одном процессе

Первый доступный duplicates sample после восстановления может уже быть 1: новый worker успевает прочитать неподтверждённый record до первого scrape. Нельзя объединять ноль старого Pod и единицу нового в один process counter.

После сохранения истинной redelivery выше выполнить отдельный диагностический replay:

```powershell
task prometheus-query QUERY=worker-duplicates
task worker-replay
if ($LASTEXITCODE -ne 0) { throw 'Дополнительный replay не выполнен' }
Start-Sleep -Seconds 45
task prometheus-query QUERY=worker-duplicates
task worker-status
task job-status
task outbox-status
task worker-group-status
task logging-query
```

Ожидается прирост duplicates на одном и том же instance/Pod, SQL effects остаётся 1. Replay создаёт **новый Kafka record** и отдельно проверяет наблюдаемость дедупликации; он не заменяет доказательство аварийной redelivery прежнего source offset. Сохранить новые coords отдельно и не смешивать traces обеих публикаций.

## Завершение и восстановление после ошибки

```powershell
task prometheus-targets
task application-metrics-verify
task dependency-metrics-verify
task smoke
```

При любой ошибке после включения отказа сохранить доступные previous logs/Pod status, затем worker-stop → tracing-worker-deploy. Не ждать полного истечения CrashLoopBackOff, не менять offsets/PVC/topics, не выполнять replay до сохранения первой части. Диагностические Pod удалять штатным worker-diagnostics-cleanup только после отчёта. Трассировки и журналы показывают наблюдаемое; один SQL-эффект и разрыв commit доказываются отдельно SQL/Kafka, не числом spans.
