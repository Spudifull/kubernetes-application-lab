# CronJob: одноразовый приём заданий в outbox

Состояние — [Passed](../verification/cronjob.md) для расписания и повтора логического запуска на f5bf6d89a472. Контракт — [Kafka и обработка заданий](../design/kafka-event-processing.md). Основной kind должен уже иметь PostgreSQL с миграцией 003, работающие publisher/worker/Kafka и нулевой исходный lag. Helm chart использует существующий образ API, но требует новой сборки с режимом `enqueue-scheduled`. Развёртывание API/publisher/worker для этого опыта не требуется.

## Проверки до установки

```powershell
go test ./...
go test -race ./cmd/api ./cmd/labctl ./internal/scheduled
go vet ./...
task cron-helm-lint
helm lint helm/lab-postgres
```

После успешных проверок зафиксировать код и сохранить SHA/чистоту рабочего дерева до сборки. Собрать `task image-build`: он обновляет локальную ссылку API-образа, но не заменяет работающий API. Последующие команды миграции/развёртывания API будут использовать новую ссылку — учитывать это отдельно.

```powershell
task image-build
task db-deploy
task cron-deploy
task cron-status
task worker-group-status
task outbox-status
```

`db-deploy` обновляет канонический NetworkPolicy PostgreSQL: разрешается доступ от Pod lab-cron к TCP 5432. В основном kind наличие NetworkPolicy не доказывает применение сетевой изоляции CNI. Сохранить состояние PostgreSQL и PV до/после. `cron-deploy` проверяет манифест через server dry-run, загружает записанный образ и устанавливает отдельный release lab-cron с enabled=true, suspend=true. Расписание ещё не запускает задания. По умолчанию chart вообще не создаёт ресурсы.

Проверить RBAC:

```powershell
kubectl --context kind-kubernetes-application-lab auth can-i get jobs -n kubernetes-application-lab --as=system:serviceaccount:kubernetes-application-lab:lab-cron
kubectl --context kind-kubernetes-application-lab auth can-i list jobs -n kubernetes-application-lab --as=system:serviceaccount:kubernetes-application-lab:lab-cron
kubectl --context kind-kubernetes-application-lab auth can-i get secrets -n kubernetes-application-lab --as=system:serviceaccount:kubernetes-application-lab:lab-cron
```

Ожидается yes/no/no. Namespace-scoped Role позволяет `get` любого Job в этом namespace, поскольку имена родительских Job создаются динамически; ограничения на единственный Job в RBAC нет. Код обращается только к родительскому Job и проверяет имя, namespace, controller owner и назначенное время. Не выдавать это за ограничение RBAC на собственный Job.

## Реальные запуски по расписанию

```powershell
task cron-resume
task cron-status
```

Расписание каждую минуту в Etc/UTC. Дождаться двух завершённых Job, затем сразу выполнить `task cron-suspend`. Не оставлять расписание включённым после опыта. Suspend не останавливает уже запущенный Job; startingDeadlineSeconds=30 ограничивает запоздалые старты, но после resume возможен ещё допустимый недавний запуск. Для каждого Job сохранить name/UID, аннотацию `batch.kubernetes.io/cronjob-scheduled-timestamp`, owner CronJob, Pod UID, image и журнал:

```powershell
kubectl --context kind-kubernetes-application-lab get jobs -n kubernetes-application-lab -l app.kubernetes.io/name=lab-cron -o yaml
kubectl --context kind-kubernetes-application-lab logs -n kubernetes-application-lab -l app.kubernetes.io/name=lab-cron --all-containers=true --prefix=true --timestamps --tail=100
task outbox-status
task worker-group-status
```

Job Complete означает только транзакционный приём, не завершение задания worker. Сопоставить `scheduled_job_enqueued` с SQL: разные назначенные моменты имеют разные ключи и job_id; каждое задание в итоге succeeded, outbox_rows=1, effects=1, pending=0. API-локатор `task job-status` не обновляется CronJob и может показывать прежнее ручное задание.

## Повтор одного логического запуска

Из сохранённого автоматически созданного Job взять точное назначенное время. В PowerShell задать его как `$scheduledAt` и повторить два раза:

```powershell
task cron-run "SCHEDULED_AT=$scheduledAt"
task cron-run "SCHEDULED_AT=$scheduledAt"
task outbox-status
task worker-group-status
```

Каждая команда создаёт отдельный ручной Job из того же Helm template, передаёт только явное назначенное время и сохраняет журнал. Ожидаются тот же ключ и job_id, created=false, неизменное число outbox/эффектов. Эти ручные Job не доказывают работу scheduler или concurrencyPolicy; они проверяют повторный транзакционный приём того же логического запуска. Для нового назначенного времени ожидается новый ключ.

## Восстановление и очистка

При ошибке чтения Job metadata, API/RBAC или PostgreSQL процесс завершится ненулевым кодом; Job имеет backoffLimit=2, activeDeadlineSeconds=90. После исправления зависимости повторять тот же назначенный момент. Не заменять его временем восстановления: получится другое логическое задание. После неопределённого результата SQL commit тот же ключ позволяет безопасно проверить повтор.

```powershell
task cron-suspend
task cron-status
task outbox-status
task worker-group-status
task db-status
task smoke
```

Сохранить результаты и журналы до очистки. TTL удаляет завершённые Job через час, history limits сохраняют до трёх успешных и трёх неуспешных запусков. После сбора доказательств:

```powershell
task cron-cleanup
task cron-status
task worker-diagnostics-cleanup
```

Cleanup приостанавливает CronJob и удаляет только помеченные lab-cron Job с их Pod, включая ещё активные; выполнять после завершения наблюдения. Приостановленный CronJob и RBAC остаются для повторного опыта. SQL, Kafka topics, offsets и PVC сохраняются. Сценарии длительного активного Job с пропуском по Forbid, недоступности API/БД, deadline и доставки SIGTERM требуют отдельных наблюдений; наличие настроек само по себе их не доказывает.

## Расширение: Forbid, deadline и отказ PostgreSQL для CronJob

Расширение прошло отдельные [проверки Forbid/deadline/отказа зависимости](../verification/cronjob.md); ниже сохранена процедура воспроизведения. Прежний базовый Passed ограничен первоначальным опытом. Требуется новая сборка API с ограниченной задержкой до SQL. Сначала выполнить Go/Helm-проверки, зафиксировать код, затем `task image-build` и `task cron-deploy`. Это возвращает штатные значения и suspended CronJob. Работающие HTTP API/publisher/worker не обновляются.

Профиль можно сменить только при suspend=true и отсутствии активных автоматических/ручных lab-cron Job. Изменение template не меняет уже созданные Job. Перед очисткой сохранить metadata, журналы, события и SQL. Не смешивать эти опыты с KEDA.

### Forbid

```powershell
task cron-profile-forbid
task cron-resume
kubectl --context kind-kubernetes-application-lab get jobs -n kubernetes-application-lab -l app.kubernetes.io/name=lab-cron -w
```

Назначенный Job сначала читает metadata и выводит scheduled_job_prepared с ключом, затем ждёт 75 секунд **до SQL**, activeDeadlineSeconds=120. На следующей минутной границе он должен ещё работать. Сохранить события CronJob и снимки Job до завершения: ожидается JobAlreadyActive или другой явный сигнал контроллера о пропущенном запуске при активном Job, без второго автоматического Job для следующего момента. Одного отсутствия второго Job недостаточно без наблюдения активного первого на границе расписания.

Как только пропуск наблюдался, остановить watch Ctrl+C, выполнить `task cron-suspend`, дождаться завершения первого Job (до 120 s), сохранить результат. Suspend не останавливает его: после задержки он должен создать одно задание. Не ждать следующего нового расписания перед suspend: Forbid может позже допустить запоздалый запуск в пределах startingDeadlineSeconds=30. Сопоставить фактические assigned timestamps, а не число строк watch.

```powershell
task cron-status
kubectl --context kind-kubernetes-application-lab get events -n kubernetes-application-lab --field-selector involvedObject.kind=CronJob --sort-by=.lastTimestamp
kubectl --context kind-kubernetes-application-lab logs -n kubernetes-application-lab -l app.kubernetes.io/name=lab-cron --prefix=true --timestamps --tail=100
task outbox-status
task worker-group-status
task cron-profile-normal
```

### Deadline и SIGTERM до SQL

```powershell
task cron-profile-deadline
task cron-resume
```

После появления первого нового автоматического Job сразу выполнить `task cron-suspend`. У Job задержка 75 s, но activeDeadlineSeconds=15 и backoffLimit=0. Дождаться Failed/DeadlineExceeded; сохранить Job YAML, Pod status, журналы с scheduled_job_prepared и, если процесс получил сигнал вовремя, scheduled_job_canceled_before_sql. У Kubernetes есть ещё terminationGracePeriodSeconds=10: не требовать удаления точно на пятнадцатой секунде. Задать `$scheduledAt` назначенным моментом из Job и выполнить `task cron-run-status "SCHEDULED_AT=$scheduledAt"`: ожидаются jobs/outbox_rows/effects=0/0/0. Результат доказывает deadline только при reason=DeadlineExceeded, а обработку SIGTERM — только при соответствующем журнале. После завершения вернуть `task cron-profile-normal`.

### Ошибка зависимости без остановки общего PostgreSQL

```powershell
task cron-profile-db-failure
task cron-resume
```

После появления первого Job выполнить `task cron-suspend`, дождаться его конечного Failed. Только этому template задан LAB_DB_PORT=1; PostgreSQL, API и рабочая очередь не меняются. Сохранить исходный scheduled timestamp из metadata Job, ключ из scheduled_job_prepared и ошибки scheduled jobs schema unavailable. Задать `$scheduledAt` этим моментом, проверить `task cron-run-status "SCHEDULED_AT=$scheduledAt"`: jobs/outbox_rows/effects=0/0/0. Другие компоненты должны оставаться Ready. Возможные повторы Pod относятся к одному Job и одному назначенному моменту.

После Failed выполнить `task cron-profile-normal`, задать `$scheduledAt` точным исходным timestamp и дважды выполнить `task cron-run "SCHEDULED_AT=$scheduledAt"`. Ожидается created=true после восстановления, затем created=false с тем же job_id, один outbox/эффект, succeeded. Это не проверка неоднозначного результата SQL commit и не отказ Kubernetes API.

Финально выполнить outbox/group/status/smoke проверки и `task cron-cleanup`, `task worker-diagnostics-cleanup`. CronJob должен остаться suspended в normal-профиле, без Job/Pod. Если профиль normal заблокирован активным Job, дождаться его ограниченного завершения; не скрывать ошибку удалением Job до сохранения доказательств.
