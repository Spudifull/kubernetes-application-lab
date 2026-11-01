# Опыты оповещений

Контракт — [alerting](../design/alerting.md), результаты — [отчёт первого прогона](../verification/alerting.md). Команды выполняет владелец в основном kind; внешние уведомления не отправляются.

## Проверки и установка

Начать на зафиксированном чистом Git-состоянии. Приложение не пересобирать; новый образ нужен только тестовому receiver.

```powershell
$env:Path = "$(Resolve-Path .cache\kubectl\v1.35.9);$env:Path"
git rev-parse HEAD
git status --short
go test -count=1 -race ./cmd/alert-receiver ./cmd/labctl
go vet ./cmd/alert-receiver ./cmd/labctl
task alerting-helm-lint
task alerting-rules-test
task alert-receiver-build
task alerting-render
task observability-render
```

При любой ошибке остановиться и сохранить вывод. До установки просмотреть YAML: шесть правил, namespace/selector, пороги/for, один receiver, ограниченные labels/body/store, отсутствие hostPath/token/ClusterRole, единственный внутренний webhook. Не применять только успешную часть failed render. `alerting-rules-test` использует локальный promtool или безопасный Docker fallback; сам агент тесты не выполнял.

```powershell
task alerting-install
task observability-install
task alerting-status
task prometheus-targets
task application-metrics-verify
task outbox-status
task worker-group-status
task smoke
```

Нужны свежий pending=0, правило inactive, группа Stable/lag=0. Дождаться успешной оценки правила после reconciliation: первый status до загрузки правила может закончиться ошибкой; сохранить его и повторить после готовности, не удалять доказательства. Receiver не имеет PodMonitor, поэтому новый scrape target не ожидается. При частичном обновлении сохранить status/logs и исправить конфигурацию, не создавать второй monitoring release.

Сохранить фактические imageID/revision receiver и его UID:

```powershell
kubectl --context kind-kubernetes-application-lab get pods -n kubernetes-application-lab-observability -l app.kubernetes.io/name=lab-alert-receiver -o json
kubectl --context kind-kubernetes-application-lab logs deployment/lab-alert-receiver -n kubernetes-application-lab-observability --timestamps
```

## Окно API и окно наблюдения

Использовать уже открытый API forward 18081. Если его нет, в отдельном окне:

```powershell
$env:Path = "$(Resolve-Path .cache\kubectl\v1.35.9);$env:Path"
kubectl --context kind-kubernetes-application-lab port-forward deployment/lab-api 18081:8080 -n kubernetes-application-lab --address 127.0.0.1
```

В окне наблюдения запустить **до создания отказа**, оставить работающим:

```powershell
$env:Path = "$(Resolve-Path .cache\kubectl\v1.35.9);$env:Path"
task alerting-watch
```

Этот процесс сам опрашивает Prometheus, Alertmanager и receiver, сохраняет каталоги evidence и заканчивается после полного цикла либо через 8 минут. Он не создаёт отказ. Наблюдение Pending обязательно; опрос, начатый уже после Firing, не закрывает пропущенную фазу.

## Окно отказа и восстановления

После сообщения `Watching OutboxBacklogGrowing` в другом окне:

```powershell
$env:Path = "$(Resolve-Path .cache\kubectl\v1.35.9);$env:Path"
task publisher-unreachable-start
task job-enqueue
task job-status
task outbox-status
```

Не продолжать после ошибки native command. Это существующий безопасный сценарий закрытого bootstrap publisher: Kafka/PVC не останавливаются. Ожидается HTTP 202 и pending outbox. Sampler SQL должен продолжить выдавать свежие данные; scrape UP не означает успешную Kafka-публикацию.

Подождать в окне наблюдения Pending → Firing и сообщение **`Firing received for OutboxBacklogGrowing fingerprint=...`**. Возраст одной строки должен превысить 60s, затем выдерживается for=1m и задержка оценки/маршрутизации. Не восстанавливать через фиксированные 30 секунд: это не даст увидеть alert.

После подтверждения приёма firing выполнить:

```powershell
task tracing-publisher-restore
Start-Sleep -Seconds 15
task job-status
task outbox-status
task worker-group-status
```

Эта команда восстанавливает bootstrap перед Helm и оставляет tracing включённым. Нужны succeeded, один effect, pending=0, lag=0. Окно наблюдения должно получить resolved того же fingerprint/startsAt и напечатать `alert_cycle_completed`. Сохранить `alert-cycle-proof.json` и poll JSON. Если observer упал/истёк timeout, publisher всё равно восстановить этой командой; новый опыт начинать только после восстановления inactive. Не выдавать неполный цикл за Passed.

## Сопоставление метрик, журналов и трассировки

В Grafana открыть Application Metrics, Alertmanager и Loki. Запрос по новому job ID из job-enqueue, временной диапазон с отказом и восстановлением:

```logql
{namespace="kubernetes-application-lab",service=~"lab-api|lab-publisher|lab-worker"} | json | job_id="UUID"
```

Нужны publisher publish failures до восстановления, broker ack после, затем worker effect/offset. TraceID ведёт в Tempo; сопоставить неуспешные и успешную попытки. Для статической сверки после завершения:

```powershell
task logging-verify
task application-trace-read
task alerting-status
task prometheus-targets
task application-metrics-verify
task dependency-metrics-verify
task smoke
```

Прислать вывод, proof и скриншоты. Диагностические Pod очищать только после записи результатов штатной worker diagnostics cleanup. Отдельно наблюдать receiver/Grafana ресурсы; таймлайн одного alert не доказывает устойчивость на нагрузке. Другие пять правил ещё требуют собственных runtime-сценариев; первым опытом весь Milestone 8 не закрывается.

## Следующий опыт: lag и отсутствие обработки worker

Первый результат [Passed в границах отчёта](../verification/worker-lag-alerts.md). Изменён только наблюдатель labctl: действующие образы приложения/receiver и установленные правила пересобирать или переустанавливать не требуется. Выполнять после успешных `go test -count=1 -race ./cmd/labctl`, `go vet ./cmd/labctl` и `git diff --check` на зафиксированной ревизии. Во всех окнах выбрать kubectl из `.cache/kubectl/v1.35.9`, как выше.

В основном окне проверить исходное состояние:

```powershell
task worker-status
task worker-group-status
task outbox-status
task alerting-status KEY=KafkaConsumerLagHigh
task alerting-status KEY=WorkerProcessingStalled
```

Нужны одна готовая реплика worker, группа Stable с одним участником, lag=0 по трём partition, pending=0 и оба правила inactive. API forward 18081 оставить открытым. В двух отдельных окнах запустить наблюдателей **до отказа**:

```powershell
task alerting-watch KEY=KafkaConsumerLagHigh
```

```powershell
task alerting-watch KEY=WorkerProcessingStalled
```

После обоих сообщений Watching в основном окне:

```powershell
task worker-stop
if ($LASTEXITCODE -ne 0) { throw 'Остановка worker не завершена; сохрани вывод' }
task job-enqueue
if ($LASTEXITCODE -ne 0) { throw 'Задание не создано; восстанови worker' }
Start-Sleep -Seconds 20
task job-status
task outbox-status
task worker-group-status
```

Не создавать другие задания до сохранения результатов: команды используют последний job locator. Ожидаются HTTP 202, опубликованный outbox, задание без SQL-эффекта, отсутствие участников группы и lag>0. Число брокеров остаётся 1: Kafka работает, остановлен только consumer. В Grafana Dependency Metrics сопоставить lag с числом участников и available worker replicas. Нужен диапазон, включающий начало отказа.

Дождаться **обоих** сообщений `Firing received for KafkaConsumerLagHigh` и `Firing received for WorkerProcessingStalled`. Первое правило выдерживает for=1m, второе for=2m; к этому добавляются интервалы scrape, оценки и маршрутизации. Не восстанавливать worker по первому уведомлению.

После обоих уведомлений восстановить диагностический tracing profile:

```powershell
task tracing-worker-deploy
if ($LASTEXITCODE -ne 0) { throw 'Восстановление worker не прошло; сохрани вывод и проверь rollout' }
Start-Sleep -Seconds 15
task job-status
task outbox-status
task worker-group-status
```

Нужны succeeded, SQL effect=1, pending=0, группа Stable/один участник/lag=0. Оба наблюдателя должны получить Resolved соответствующего fingerprint и завершиться событием `alert_cycle_completed`. Сохранить два каталога evidence, `alert-cycle-proof.json`, вывод команд и скриншот lag/участников с отказом и восстановлением. Затем выполнить совместную сверку logging-verify/application-trace-read/prometheus-targets/application-metrics-verify/dependency-metrics-verify/smoke из предыдущего раздела.

Если наблюдатель завершился ошибкой или истекли 8 минут, выполнить `task tracing-worker-deploy` независимо от полноты доказательства. После восстановления проверить оба `alerting-status` с теми же KEY. Не удалять receiver или evidence. Этот опыт проверяет отсутствие consumer и доставку двух оповещений; он не заменяет аварийное завершение после SQL-эффекта перед offset commit.

## DLQ: исчерпание попыток и доставка уведомления

[Отчёт первого прогона: Passed в указанном объёме](../verification/dlq-alert.md). Изменён только labctl; образы приложения и receiver, правила и monitoring release обновлять не требуется. Владелец выполняет go test -count=1 -race ./cmd/labctl, go vet ./cmd/labctl, git diff --check и фиксирует ревизию перед прогоном. Исходно одна реплика worker, нет активных отказов, lag/pending=0, предыдущие alert inactive. API forward 18081 работает.

Сначала остановить publisher и worker. Так новая работа останется в outbox до завершения подготовки наблюдения:

```powershell
task publisher-stop
if ($LASTEXITCODE -ne 0) { throw 'Publisher не остановлен' }
task worker-stop
if ($LASTEXITCODE -ne 0) { throw 'Worker не остановлен; верни publisher' }
task job-enqueue
if ($LASTEXITCODE -ne 0) { throw 'Задание не создано; восстанови процессы' }
task job-status
task outbox-status
task worker-retry-exhaust
if ($LASTEXITCODE -ne 0) { throw 'Ошибка не настроена; сохрани вывод и восстанови процессы' }
task tracing-worker-deploy
if ($LASTEXITCODE -ne 0) { throw 'Worker не восстановлен; сохрани вывод' }
Start-Sleep -Seconds 60
task alerting-status KEY=DLQMessagesDetected
task prometheus-query QUERY=worker-retries
task prometheus-query QUERY=worker-dlq
```

ConfigureWorkerFault допускает accepted и published; здесь ожидается accepted, outbox pending=1, effects=0. Не создавать другое задание до сохранения всех результатов: используется последний locator. Worker уже работает, но publisher ещё остановлен, поэтому сообщение не может попасть в Kafka до разрешения оператора. Дождаться inactive и recovery_value=0 с непустым signal_identity; null — недостаточно samples, повторить status после следующего scrape. Если подготовка заняла больше двух минут, может появиться outbox alert: он не заменяет DLQ alert и должен разрешиться после возврата publisher.

В отдельном окне запустить наблюдатель и дождаться Watching:

```powershell
$env:Path = "$(Resolve-Path .cache\kubectl\v1.35.9);$env:Path"
task alerting-watch KEY=DLQMessagesDetected
```

В основном окне разрешить публикацию, сохранив worker и его process counters:

```powershell
task publisher-start
if ($LASTEXITCODE -ne 0) { throw 'Publisher не восстановлен; сохрани вывод' }
Start-Sleep -Seconds 20
task job-status
task worker-reliability-status
task worker-dlq-read
task worker-group-status
task prometheus-query QUERY=worker-retries
task prometheus-query QUERY=worker-dlq
task logging-query
```

Нужны failed/error_code=retry_exhausted, failure_count=3, effects=0, подтверждённая SQL-квитанция, совпадающая DLQ-запись и source committed=end/lag=0. Сопоставить рост worker-retries и worker-dlq на том же instance с исходными samples. В Loki — повторные ошибки и порядок DLQ broker ack → SQL-квитанция → offset commit. В Tempo открыть trace_id новой постановки и проверить попытки/DLQ; application-trace-read проверяет succeeded и для этого failed job не подходит. Не заменять его ожидаемую ошибку утверждением о поломке tracing.

После Firing оставить тот же worker работать. Не перезапускать, не обнулять счётчик и не создавать новый DLQ, пока не получены Resolved и alert_cycle_completed: increment должен естественно выйти из окна 5m. Наблюдение ограничено 12 минутами; исчезновение sample, reset или замена instance прерывает доказательство. Resolved не исправляет failed job и не удаляет DLQ. Сохранить proof, polls, SQL/Kafka/metrics/logs и скриншот трассировки.

В конце выполнить publisher-status, worker-status, outbox-status, worker-group-status, оба прежних alerting-status с worker KEY и status с DLQ KEY, prometheus-targets, application-metrics-verify, dependency-metrics-verify и smoke. При прерывании вернуть publisher через publisher-start и worker через tracing-worker-deploy; failed job не воспроизводить автоматически. Диагностические Pod очищать только после сохранения данных.

## APIUnavailable: ноль реплик и восстановление HTTP

[Отчёт первого прогона: Passed в указанном объёме](../verification/api-unavailable-alert.md). Владелец выполняет go test -count=1 -race ./cmd/labctl, go vet ./cmd/labctl и git diff --check, сохраняет HEAD и чистое дерево. Образы и установленные правила не меняются. Основной kind, kubectl 1.35.9, одна фиксированная реплика API; publisher/worker/PostgreSQL/Kafka работают, предыдущие alerts inactive.

```powershell
$env:Path = "$(Resolve-Path .cache\kubectl\v1.35.9);$env:Path"
task api-alert-check
if ($LASTEXITCODE -ne 0) { throw 'Исходная доступность API не подтверждена' }
task alerting-status KEY=APIUnavailable
task outbox-status
task worker-group-status
```

Нужны /version с revision, API available=1, правило inactive/recovery_value=0, pending/lag=0. Проверка /version идёт через Kubernetes Service proxy, отдельный port-forward не нужен.

В другом окне до отказа запустить:

```powershell
$env:Path = "$(Resolve-Path .cache\kubectl\v1.35.9);$env:Path"
task alerting-watch KEY=APIUnavailable
```

После Watching в основном окне:

```powershell
task api-alert-stop
if ($LASTEXITCODE -ne 0) { throw 'Остановка API не подтверждена; сохрани вывод и проверь число реплик' }
Start-Sleep -Seconds 10
task api-alert-check KEY=unavailable
```

Остановка проверяет HPA/KEDA, владельца Helm и исходное replicaCount=1, затем меняет только scale 1→0 и ждёт исчезновения текущих Pod. Образ, шаблон и tracing profile сохранены. При HPA/KEDA команда завершается до изменения scale; не удалять scaler ради продолжения. Service/PVC/данные не удаляются. Если check выполнен до обновления endpoints, сохранить ошибку, проверить состояние и повторить после следующего согласования.

В проверке недоступности нужны spec.replicas=0, availableReplicas=0 и отказ Service proxy с причиной no endpoints. Команда возвращает успешный exit code только при ожидаемом отказе; результат самого запроса записан отдельно в api-service-proxy.json. Это ответ proxy из-за отсутствия backend, не ответ приложения и не проверка сети Windows. Ошибка подключения к control plane не принимается за отказ API. Старый API port-forward может завершиться после удаления Pod; это ожидаемо и не используется как доказательство.

В Grafana Explore/Prometheus запросить ALERTS{alertname="APIUnavailable"}, диапазон с началом опыта. У правила for=1m плюс интервалы оценки/доставки. **Сразу после первого сообщения Firing received for APIUnavailable восстановить API в основном окне, пока наблюдатель продолжает работать. Не ждать завершения alerting-watch: он завершится только после восстановления и Resolved.**

```powershell
task api-alert-start
if ($LASTEXITCODE -ne 0) { throw 'API не восстановлен; сохрани evidence и проверь Deployment' }
task api-alert-check
task smoke
Start-Sleep -Seconds 45
task prometheus-targets
task application-metrics-verify
task dependency-metrics-verify
task outbox-status
task worker-group-status
```

Возврат scale 0→1 ждёт rollout того же Deployment. Нужны /version с прежней revision, успешный in-cluster smoke и полный alert_cycle_completed при известном дефиците реплик=0. Сохранить proof, каталоги stop/start/check, наблюдения и скрин ALERTS. Кратковременное LabAPIMetricsUnavailable может также сработать; оно не заменяет APIUnavailable. Во время отсутствия API метрики worker могут оставаться UP; это не доказывает доступность HTTP API.

При timeout/прерывании восстановить API через api-alert-start. Если spec уже 1, повторное start намеренно отклоняется; проверить api-alert-check/rollout. При неуспешном восстановлении сохранить evidence; штатный tracing-api-deploy возвращает записанный образ и профиль. Для дальнейших job-enqueue открыть API port-forward заново после восстановления. Незавершённый цикл не получает Passed.

## PostgreSQLUnavailable: остановка БД с сохранением PVC

[Отчёт первого прогона: Passed в указанном объёме](../verification/postgres-unavailable-alert.md). Изменён только наблюдатель labctl, установленные правила и образы пересобирать не требуется. Перед прогоном владелец выполняет go test -count=1 -race ./cmd/labctl, go vet ./cmd/labctl, git diff --check; сохраняет HEAD и чистоту дерева. Действуют основной kind, kubectl 1.35.9, один PostgreSQL Pod, одна реплика API с database/jobs/tracing профилем. Другие опыты завершены, pending/lag=0.

В основном окне создать уникальную контрольную запись и снять состояние:

```powershell
$env:Path = "$(Resolve-Path .cache\kubectl\v1.35.9);$env:Path"
$ctx = 'kind-kubernetes-application-lab'
$ns = 'kubernetes-application-lab'
$pgKey = 'pg-alert-' + [DateTime]::UtcNow.ToString('yyyyMMddHHmmss')
"control_key=$pgKey"
task db-status
task db-record-put KEY=$pgKey VALUE=preserved
if ($LASTEXITCODE -ne 0) { throw 'Контрольная запись не создана' }
task db-record-get KEY=$pgKey
kubectl --context $ctx get pvc data-lab-postgres-0 -n $ns -o custom-columns=NAME:.metadata.name,UID:.metadata.uid,STATUS:.status.phase,VOLUME:.spec.volumeName
kubectl --context $ctx get pod -n $ns -l app.kubernetes.io/name=lab-api -o wide
task dependency-metrics-verify
task alerting-status KEY=PostgreSQLUnavailable
```

Продолжать при единственной готовой реплике БД, успешном чтении preserved, Bound PVC, pg_up=1/exporter UP, правиле inactive/recovery_value=0. PostgreSQLUnavailable может означать отказ exporter/credentials/telemetry, поэтому эти исходные условия обязательны.

В отдельном окне открыть API port-forward, если действующего туннеля к текущему Pod нет. Во время остановки БД Pod API остаётся запущенным; туннель deployment/lab-api ведёт к выбранному Pod даже после потери readiness:

```powershell
$env:Path = "$(Resolve-Path .cache\kubectl\v1.35.9);$env:Path"
kubectl --context kind-kubernetes-application-lab port-forward deployment/lab-api 18081:8080 -n kubernetes-application-lab --address 127.0.0.1
```

В третьем окне запустить наблюдатель и дождаться Watching:

```powershell
$env:Path = "$(Resolve-Path .cache\kubectl\v1.35.9);$env:Path"
task alerting-watch KEY=PostgreSQLUnavailable
```

Затем в основном окне:

```powershell
curl.exe -sS -i --noproxy "*" --max-time 5 http://127.0.0.1:18081/readyz
task db-failure-start
if ($LASTEXITCODE -ne 0) { throw 'Остановка БД не подтверждена; сохрани вывод' }
kubectl --context $ctx wait --for=delete pod/lab-postgres-0 -n $ns --timeout=90s
curl.exe -sS -i --noproxy "*" --max-time 5 http://127.0.0.1:18081/livez
curl.exe -sS -i --noproxy "*" --max-time 5 http://127.0.0.1:18081/readyz
curl.exe -sS -i --noproxy "*" --max-time 5 "http://127.0.0.1:18081/records/$pgKey"
kubectl --context $ctx get pod -n $ns -l app.kubernetes.io/name=lab-api -o wide
task alerting-status KEY=PostgreSQLUnavailable
```

Ожидаются /livez=200, /readyz=503, чтение записи=503, тот же API Pod без нового рестарта. /version сам по себе не проверяет SQL. Пока БД отсутствует, не запускать outbox-status/worker-reliability-status и другие SQL-команды: их ошибка ожидаема и не заменяет проверку alert. Прямой туннель нужен, поскольку Service перестаёт выбирать NotReady API.

В Grafana Explore/Prometheus запросить ALERTS{alertname="PostgreSQLUnavailable"}; дополнительно pg_up{job="lab-postgres-exporter"} и up{job="lab-postgres-exporter"}. Сопоставить pg_up со scrape UP: доступность exporter не равна доступности PostgreSQL. Наблюдатель использует свежий StatefulSet ready=0 как сигнал реального отсутствия БД; даже если exporter не отдаёт pg_up во время отказа, исчезнувший ряд не превращается в известный ноль восстановления.

**Сразу после Firing received for PostgreSQLUnavailable выполнить восстановление в основном окне, пока наблюдатель работает. Не ждать завершения alerting-watch.**

```powershell
task db-failure-restore
if ($LASTEXITCODE -ne 0) { throw 'БД не восстановлена; сохрани evidence' }
kubectl --context $ctx rollout status deployment/lab-api -n $ns --timeout=120s
curl.exe -sS -i --noproxy "*" --max-time 5 http://127.0.0.1:18081/livez
curl.exe -sS -i --noproxy "*" --max-time 5 http://127.0.0.1:18081/readyz
task db-record-get KEY=$pgKey
kubectl --context $ctx get pvc data-lab-postgres-0 -n $ns -o custom-columns=NAME:.metadata.name,UID:.metadata.uid,STATUS:.status.phase,VOLUME:.spec.volumeName
kubectl --context $ctx get pod -n $ns -l app.kubernetes.io/name=lab-api -o wide
Start-Sleep -Seconds 45
task alerting-status KEY=PostgreSQLUnavailable
task prometheus-targets
task application-metrics-verify
task dependency-metrics-verify
task outbox-status
task worker-group-status
task smoke
```

Нужны /readyz=200, сохранённая запись, прежние UID PVC и volume, Bound, pg_up=1/exporter UP, pending/lag=0, matching Resolved и alert_cycle_completed. Само завершение scale/rollout не доказывает SQL и сохранность записи. Наблюдение ограничено 8 минутами. При timeout/ошибке вернуть БД через db-failure-restore независимо от доказательства. Не удалять PVC, не выполнять db-restore и не переустанавливать PostgreSQL ради этого опыта. APIUnavailable может дополнительно сработать из-за NotReady API; это отдельный сигнал, не замена PostgreSQLUnavailable. Контрольную запись оставить до сохранения отчёта.
