# Проверка Milestone 6 в PowerShell

**Состояние: Current для инструкции.** Выполненные сценарии и границы подтверждения описаны в [verification](../verification/README.md); дополнительные проверки полного брифа ниже также выполнены, результаты сохранены в профильных отчётах. Команды выполняет владелец, работая из корня репозитория. До проверки не удалять существующие кластеры, PVC и исходный Secret. Канонический контракт — [PostgreSQL и сохраняемое состояние](../design/postgresql-stateful-storage.md).

## Предварительная проверка

```powershell
kubectl config use-context kind-kubernetes-application-lab
git status --short
git rev-parse HEAD
go test ./...
go test -race ./...
go vet ./...
task db-helm-lint
task db-helm-render
task db-preflight
```

Дождаться чистого рабочего дерева после commit. Если кластер недоступен, не пересоздавать его автоматически. Сначала сохранить `kubectl --context kind-kubernetes-application-lab get nodes` и диагностировать доступ.

## PostgreSQL и миграция

```powershell
task db-secret-create
task db-deploy
task db-status
task image-build
task db-migrate
task db-migrate-status
task db-api-deploy
task db-record-put KEY=storage-proof VALUE=before-restart
task db-record-get KEY=storage-proof
```

Сохранить имя Job из `task db-migrate`, затем `kubectl --context kind-kubernetes-application-lab logs job/ИМЯ_JOB -n kubernetes-application-lab --timestamps`. Сохранить `imageID` PostgreSQL и API, Pod UID, PVC/PV и события. После успешного первого Job повторить `task db-migrate` для проверки пропуска применённой версии. Не включать DB-профиль API до первого успешного Job.

## Сохранение после замены Pod

```powershell
task db-pod-restart
task db-record-get KEY=storage-proof
task db-status
```

Проверить изменение UID Pod и прежние PVC/PV. Не принимать одно только `Running` как доказательство сохранения записи.

## Отказ БД и неверные учётные данные

```powershell
task db-failure-start
task db-status
task db-failure-restore
task db-status
task db-record-get KEY=storage-proof
task db-bad-credentials-start
task db-bad-credentials-status
task db-bad-credentials-cleanup
task db-record-get KEY=storage-proof
```

После `db-failure-start` отдельно наблюдать `/readyz`, `/livez`, время HTTP-ошибки и число рестартов API через прямой путь к Pod: через обычный Service NotReady Pod может быть уже недоступен. `db-bad-credentials-status` должен показать `authentication_failed` у отдельного release; рабочий Secret не менять. При любой ошибке опыта сначала восстановить PostgreSQL командой `task db-failure-restore`.

## Отрицательная миграция, backup и restore

```powershell
task db-migrate-fail
task db-migrate-status
task db-migrate
task db-record-put KEY=backup-proof VALUE=before-backup
task db-backup
task db-record-put KEY=backup-proof VALUE=after-backup
task db-restore KEY=backup-proof
task db-restore-cleanup
task db-record-get KEY=backup-proof
```

Для отрицательного Job нужен образ с текущей реализацией контрольной таблицы. До запуска проверить отсутствие `public.lab_migration_rollback_probe` и `public.lab_intentionally_missing_table`, сохранить версии миграций и контрольную запись. После `Failed` сохранить имя, `describe`, `logs` и exit code до TTL-очистки, затем проверить отсутствие контрольной таблицы и сохранность исходных данных. Подробные критерии — в [отчёте о миграциях](../verification/database-migrations.md). Не считать ошибку запуска Pod доказательством транзакционного отката SQL. `db-backup` откажется перезаписать `.local-db/backup.dump`; старый файл сохранять или удалять только явным действием после фиксации результата. `db-restore` требует отсутствия отдельной базы `lab_restore`; основную базу команда не удаляет. До очистки отдельным запросом к восстановленной базе подтвердить `before-backup`, а через рабочий API — `after-backup`, как описано в [сценарии восстановления](../verification/database-backup-restore.md).

## Проверка базового профиля

После сохранения результатов выполнить `task deploy`, остановить PostgreSQL через `task db-failure-start`, дождаться нуля реплик и выполнить `task smoke` вместе с запросом `/readyz`. В блоке `finally` обязательно восстановить БД командой `task db-failure-restore`. Базовый API должен оставаться доступным при PostgreSQL `0/0`; [финальный прогон](../verification/postgresql-statefulset.md#финальная-регрессия-базового-профиля-api) это подтвердил. Основной API после опыта остаётся в базовом профиле; для операций с БД выполнить `task db-api-deploy`. `task db-deploy` и обычная очистка не удаляют PVC.

## Дополнительные проверки полного брифа

**Состояние: дополнительные ветви выполнены и подтверждены в профильных отчётах.** Основные пять отчётов и регрессия базового профиля уже подтверждены. 25 октября владелец предоставил успешные результаты всех трёх Go-проверок после изменения отрицательной миграции; они сохранены в [отчёте о миграциях](../verification/database-migrations.md#проверки-go-после-изменения-отрицательной-миграции). Отказ при отсутствующем Secret, состояние API при отсутствии таблицы и восстановление после исправления конфигурации и схемы также проверены 25 октября; результаты находятся в отчётах о запуске PostgreSQL и миграциях. Эти одноразовые диагностические команды используют существующий chart, отдельный release `lab-api-badcred`, отдельную ConfigMap и пустую базу `lab_schema_probe`. Имя release уже допускается NetworkPolicy PostgreSQL. Рабочий Secret и основная база `lab` не изменяются.

### Проверки Go

Текущие изменения документации не требуют пересборки образа. Выполнить на исходниках, соответствующих зафиксированному Go-коду, и сохранить вывод:

```powershell
git rev-parse HEAD
git status --short
go test ./...
go test -race ./...
go vet ./...
```

### Окно 1: изолированное состояние без Secret

Работать из корня репозитория. При существовании одноимённого release, ConfigMap, отсутствующего по замыслу Secret или тестовой базы остановиться и сохранить их состояние; эти команды не предназначены для перезаписи ресурсов прежнего опыта.

```powershell
$ctx = 'kind-kubernetes-application-lab'
$ns = 'kubernetes-application-lab'
$imageTag = (Get-Content .local-image-ref -Raw).Trim().Split(':')[-1]

helm list --kube-context $ctx -n $ns --filter '^lab-api-badcred$'
kubectl --context $ctx get configmap lab-schema-probe-config -n $ns --ignore-not-found
kubectl --context $ctx get secret lab-postgres-missing-for-probe -n $ns --ignore-not-found -o name
kubectl --context $ctx exec -n $ns lab-postgres-0 -- psql -U lab -d lab -v ON_ERROR_STOP=1 -c "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname='lab_schema_probe') AS probe_db_exists;"
```

Ожидаются отсутствие объектов и `probe_db_exists=f`. Затем:

```powershell
kubectl --context $ctx exec -n $ns lab-postgres-0 -- createdb -U lab lab_schema_probe
if ($LASTEXITCODE -ne 0) { throw 'Тестовая база не создана; остановить опыт' }

kubectl --context $ctx create configmap lab-schema-probe-config -n $ns --from-literal=POSTGRES_DB=lab_schema_probe --from-literal=LAB_DB_HOST=lab-postgres --from-literal=LAB_DB_PORT=5432
if ($LASTEXITCODE -ne 0) { throw 'Тестовая ConfigMap не создана; остановить опыт' }

helm install lab-api-badcred helm/lab-api --kube-context $ctx -n $ns -f helm/lab-api/values-db.yaml --set-string image.tag=$imageTag --set-string database.configMapName=lab-schema-probe-config --set-string database.secretName=lab-postgres-missing-for-probe
if ($LASTEXITCODE -ne 0) { throw 'Экспериментальный release не создан' }

Start-Sleep -Seconds 10
kubectl --context $ctx get pods -n $ns -l app.kubernetes.io/instance=lab-api-badcred
kubectl --context $ctx describe pods -n $ns -l app.kubernetes.io/instance=lab-api-badcred
```

Ожидается `CreateContainerConfigError`, а Events должны указывать на отсутствующий `lab-postgres-missing-for-probe`. Это отказ подготовки окружения контейнера: процесса API и его HTTP-ответов на этом шаге ещё нет. Helm install намеренно не использует `--wait`, поскольку готовность в этом состоянии не ожидается.

### Окно 1: исправление ссылки на Secret и отсутствующая схема

В том же окне заменить только ссылку экспериментального release на существующий рабочий Secret. Пароль не читается и не копируется:

```powershell
helm upgrade lab-api-badcred helm/lab-api --kube-context $ctx -n $ns -f helm/lab-api/values-db.yaml --set-string image.tag=$imageTag --set-string database.configMapName=lab-schema-probe-config --set-string database.secretName=lab-postgres-credentials
if ($LASTEXITCODE -ne 0) { throw 'Ссылка на Secret не исправлена' }

Start-Sleep -Seconds 10
kubectl --context $ctx get pods -n $ns -l app.kubernetes.io/instance=lab-api-badcred
$probePod = (kubectl --context $ctx get pods -n $ns -l app.kubernetes.io/instance=lab-api-badcred -o json | ConvertFrom-Json).items | Where-Object { $_.status.phase -eq 'Running' -and -not $_.metadata.deletionTimestamp } | Select-Object -First 1
if (-not $probePod) { throw 'Экспериментальный API не запущен; сохранить describe и остановить опыт' }
kubectl --context $ctx logs "pod/$($probePod.metadata.name)" -n $ns --timestamps --tail=40
```

Теперь ожидается работающий, но NotReady API и `reason=schema_missing`: соединение с новой базой успешно, однако `lab_records` ещё не существует.

### Окно 2: прямой туннель к экспериментальному Pod

```powershell
kubectl --context kind-kubernetes-application-lab port-forward deployment/lab-api-badcred 18084:8080 -n kubernetes-application-lab --address 127.0.0.1
```

После `Forwarding` в окне 1:

```powershell
curl.exe -i --noproxy "*" --max-time 5 http://127.0.0.1:18084/livez
curl.exe -i --noproxy "*" --max-time 5 http://127.0.0.1:18084/readyz
curl.exe -i --noproxy "*" --max-time 5 http://127.0.0.1:18084/records/schema-proof
```

Ожидаются `200`, `503`, `503`. В журнале причиной отказа готовности должна быть именно `schema_missing`, не `database_unavailable` или `authentication_failed`.

### Окно 1: восстановление схемы и явная очистка

Применить существующий SQL-файл только к изолированной базе. Это проверка реакции API на появление таблицы; механизм Job и откат миграции проверены отдельно в основном сценарии.

```powershell
Get-Content internal/database/migrations/001_lab_records.sql -Raw | kubectl --context $ctx exec -i -n $ns lab-postgres-0 -- psql -U lab -d lab_schema_probe -v ON_ERROR_STOP=1
if ($LASTEXITCODE -ne 0) { throw 'Схема тестовой базы не восстановлена' }

kubectl --context $ctx rollout status deployment/lab-api-badcred -n $ns --timeout=90s
curl.exe -i --noproxy "*" --max-time 5 http://127.0.0.1:18084/readyz
curl.exe -i --noproxy "*" --max-time 5 http://127.0.0.1:18084/records/schema-proof
kubectl --context $ctx get pods -n $ns -l app.kubernetes.io/instance=lab-api-badcred
```

Ожидаются готовность `200` и чтение отсутствующего ключа `404` вместо ошибки БД. Сохранить все результаты, затем остановить туннель окна 2 через `Ctrl+C`. Очистка удаляет только release и базу данного дополнительного опыта:

```powershell
helm uninstall lab-api-badcred --kube-context $ctx -n $ns --wait --timeout=90s
kubectl --context $ctx get pods -n $ns -l app.kubernetes.io/instance=lab-api-badcred
```

Дождаться отсутствия экспериментальных Pod. `dropdb` без `--force` откажется при ещё открытых соединениях; в таком случае дождаться завершения Pod, не принуждая удаление базы. Затем:

```powershell
kubectl --context $ctx exec -n $ns lab-postgres-0 -- dropdb -U lab lab_schema_probe
kubectl --context $ctx delete configmap lab-schema-probe-config -n $ns
kubectl --context $ctx exec -n $ns lab-postgres-0 -- psql -U lab -d lab -v ON_ERROR_STOP=1 -c "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname='lab_schema_probe') AS probe_db_exists; SELECT key,value FROM lab_records WHERE key IN ('storage-proof','backup-proof') ORDER BY key;"
task smoke
```

Ожидаются `probe_db_exists=f`, исходные записи основной базы (`storage-proof=before-restart`, `backup-proof=after-backup`) и успешный smoke базового API. Предоставленный владельцем прогон подтвердил эти значения; результаты внесены в отчёты о запуске PostgreSQL и миграциях. В последнем снимке после uninstall экспериментальный Pod ещё был `Terminating`; окончательное отсутствие Pod отдельно не подтверждено, при этом удаление тестовой базы и ConfigMap и сохранность основных записей проверены.
