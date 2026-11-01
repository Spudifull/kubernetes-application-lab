# Проверка централизованных журналов

Сценарий и граница — [контракт](../design/application-logging.md), результаты — [отчёт](../verification/application-logging.md). Команды выполняет владелец из корня репозитория. Новая сборка API/publisher/worker не нужна: используется развёрнутый чистый образ с уже проверенным JSON/tracing. Версию CLI/config и версии приложений фиксировать раздельно.

## Подготовка и установка

В основном окне PowerShell:

```powershell
$env:Path = "$(Resolve-Path .cache\kubectl\v1.35.9);$env:Path"
git rev-parse HEAD
git status --short
go test -count=1 -race ./cmd/labctl
if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
go vet ./cmd/labctl
if ($LASTEXITCODE -ne 0) { throw 'Go vet failed' }
task logging-helm-lint
if ($LASTEXITCODE -ne 0) { throw 'Helm lint failed' }
task logging-render
if ($LASTEXITCODE -ne 0) { throw 'Logging render failed' }
task observability-render
if ($LASTEXITCODE -ne 0) { throw 'Monitoring render failed' }
```

Просмотреть сохранённый `lab-logging.yaml` и monitoring YAML: образы, securityContext, два PodMonitor, namespace Role/RoleBinding, отсутствие host mounts/ClusterRole, четыре индексных labels, retention и PVC. Ничего ещё не установлено. После исправлений нужен новый commit и повтор этих проверок.

```powershell
task logging-install
if ($LASTEXITCODE -ne 0) { throw 'Keep failed resources and evidence; inspect before continuing' }
task logging-status
if ($LASTEXITCODE -ne 0) { throw 'Logging not ready' }
task observability-install
if ($LASTEXITCODE -ne 0) { throw 'Grafana provisioning failed' }
task prometheus-targets
task application-metrics-verify
task dependency-metrics-verify
task worker-group-status
task smoke
```

`observability-install` обновляет существующий monitoring release для datasource; не устанавливать второй стек. При ошибке сохранить каталог evidence и журналы `statefulset/lab-loki`, `deployment/lab-alloy` в namespace наблюдаемости. Не удалять PVC. Все исходные зависимости должны оставаться доступны.

## Отдельные окна

В окне API держать ранее работающий port-forward на 18081; если его нет:

```powershell
$env:Path = "$(Resolve-Path .cache\kubectl\v1.35.9);$env:Path"
kubectl --context kind-kubernetes-application-lab port-forward deployment/lab-api 18081:8080 -n kubernetes-application-lab --address 127.0.0.1
```

В окне Grafana после обновления Pod:

```powershell
$env:Path = "$(Resolve-Path .cache\kubectl\v1.35.9);$env:Path"
task grafana-forward
```

Не запускать второй forward на занятый порт. Если прежний процесс жив, использовать его; если связан со старым Pod — завершить его `Ctrl+C` и открыть новый. Для CLI-запросов Loki отдельный forward не нужен. Grafana: `http://127.0.0.1:13000`, прежний пользователь `lab-admin` и пароль Secret.

## Обычное задание

Сохранить реальные env tracing трёх Deployment; ожидается `LAB_TRACING_ENABLED=true`, диагностическое sampling=1. Если выключено — вернуть три профиля `tracing-api-deploy`, `tracing-publisher-deploy`, `tracing-worker-deploy` из [предыдущей процедуры](application-tracing-lab.md), сохранить изменения revisions/UID и заново открыть API forward. Не заменять чистые образы на сборку с dirty-ревизией.

```powershell
task job-enqueue
if ($LASTEXITCODE -ne 0) { throw 'Job enqueue failed' }
Start-Sleep -Seconds 10
task job-status
task logging-verify
task outbox-status
task worker-group-status
```

Сохранить `job_id`, `event_id`, `trace_id` и каталог `logging-verify`. Ожидаются пять обязательных событий с общим контекстом. При задержке сбора повторить `logging-verify` в пределах 30 минут, не создавать новое задание для подмены неуспешного результата.

Grafana → Explore → **Loki** → Code. Подставить UUID из вывода:

```logql
{namespace="kubernetes-application-lab",service=~"lab-api|lab-publisher|lab-worker"} | json | job_id="UUID"
```

Выбрать последние 30 минут, раскрыть строки трёх сервисов, проверить время/Pod и TraceID → Tempo. После `| json` Grafana показывает извлечённые поля: это не означает, что они индексируются. Снимок с UUID и строками трёх процессов прикладывается как иллюстрация; raw response CLI остаётся основным доказательством.

## Управляемые повторы и восстановление

В основном окне, пока API forward открыт:

```powershell
task worker-stop
if ($LASTEXITCODE -ne 0) { throw 'Worker did not stop' }
task job-enqueue
if ($LASTEXITCODE -ne 0) { throw 'Job enqueue failed; restore worker before leaving' }
task worker-retry-success
if ($LASTEXITCODE -ne 0) { throw 'Fault setup failed; keep evidence and restore tracing-worker-deploy' }
task tracing-worker-deploy
if ($LASTEXITCODE -ne 0) { throw 'Worker restore failed' }
Start-Sleep -Seconds 45
task job-status
task worker-reliability-status
task logging-retry-verify
task outbox-status
task worker-group-status
```

Ожидаются `failure_count=2`, `succeeded`, один SQL-эффект и lag=0 в независимых проверках, два `worker_retry_scheduled` и последующий commit в Loki с одним контекстом. В Grafana запрос тот же, UUID нового задания. Проверить, что API не ждал завершения обработки: `202` означает приём, не готовый результат. Остановка worker создаёт отсутствие consumer, число broker не должно измениться. После опыта worker должен иметь одну доступную реплику.

## Ресурсы и итог

Не менее 15 минут держать Grafana с запросами и повторять `kubectl get pods`/`top pods --containers` в namespace наблюдаемости через 30 секунд. Сохранить начало/конец, CPU/память Loki/Alloy/Grafana, UID, imageID, restartCount, PVC/PV и requests/limits до/после. Первые пробы не заменяют полный интервал. Убедиться, что Loki PVC Bound, прежние PVC на месте, два новых targets UP и прежние targets не потеряны.

В отдельном окне наблюдения (31 снимок, журнал остаётся в игнорируемом каталоге):

```powershell
$env:Path = "$(Resolve-Path .cache\kubectl\v1.35.9);$env:Path"
$ctx = 'kind-kubernetes-application-lab'
$obsNs = 'kubernetes-application-lab-observability'
$sel = 'app.kubernetes.io/name in (lab-loki,lab-alloy,grafana)'
$resourceLog = Join-Path '.local-observability' ('logging-resources-' + [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssZ') + '.txt')
0..30 | ForEach-Object {
    & {
        "observed_at=$([DateTime]::UtcNow.ToString('o'))"
        kubectl --context $ctx get pods -n $obsNs -l $sel -o wide
        kubectl --context $ctx top pods -n $obsNs -l $sel --containers
        kubectl --context $ctx get pods -n $obsNs -l $sel -o json
    } 2>&1 | Tee-Object -FilePath $resourceLog -Append
    if ($_ -lt 30) { Start-Sleep -Seconds 30 }
}
"resource_log=$resourceLog"
```

Снимок Pod JSON содержит UID, imageID, restartCount и ресурсы, но не включает содержимое Secrets. Ошибку любой пробы сохранить; она означает отсутствие измерения, а не нулевое потребление. Длительный просмотр может пережить 12-минутный лимит forward: открыть его заново, когда предыдущий завершится, и записать этот разрыв отдельно от перезапуска Pod.

В конце выполнить `logging-status`, `prometheus-targets`, `application-metrics-verify`, `worker-group-status`, `smoke`. Временные job diagnostics очищаются прежней штатной командой только после записи результатов; рабочие release/PVC не удаляются. Прислать вывод и скриншот Explore. Выполненный прогон 2026-11-01 имеет Passed в границах [отчёта](../verification/application-logging.md); новые прогоны оцениваются по собственным результатам. Общий Milestone 8 ещё включает оповещения, оставшиеся сценарии отказов наблюдаемости и итоговую сквозную проверку.
