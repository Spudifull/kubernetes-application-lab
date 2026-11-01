# Kafka worker: порядок проверки

Состояние: третья итерация имеет [Passed](../verification/kafka-worker.md) на чистом образе `858f2ee181e4`, включая SIGTERM во время работы, восстановление и очистку. Это не закрытие всего Milestone 7. Контракт — [Kafka и обработка заданий](../design/kafka-event-processing.md). Команды ниже предназначены для воспроизведения; создание commit не заменяет проверку поведения.

## 1. Предварительные проверки

Из корня репозитория, по одной команде с проверкой успешного завершения:

```powershell
go test ./...
go test -race ./...
go vet ./...
task worker-helm-lint
task worker-helm-render
task db-helm-lint
```

Отдельно выполнить `task worker-sql-integration-test` в изолированной PostgreSQL БД `lab_jobs_test_<уникальный_суффикс>`, настроив `LAB_JOBS_INTEGRATION=1` и `LAB_DB_*` по [процедуре SQL integration](kafka-lab.md#отдельный-sql-integration-тест). В её блоке заменить `task jobs-sql-integration-test` на `task worker-sql-integration-test`. Не направлять тест на основную БД. Сохранить настоящий `PASS`, а не `SKIP`; после проверки удалить только свою временную БД, подтвердить её отсутствие и очистить переменные пароля из сеанса.

После исправлений создать commit, затем сохранить:

```powershell
git status --porcelain
git rev-parse HEAD
```

Первый вывод должен быть пустым. Образ worker собрать из этого чистого состояния. Прежние API/publisher `4345b1078c40` с jobs-профилем совместимы: пересобирать их и повторно менять миграции 001/002 не требуется. Сохранить отдельно SHA сценария, build revision каждого процесса и ссылки образов.

## 2. Основное окно — подготовка

Использовать kubectl 1.35.9, если глобальный 1.36 всё ещё несовместим с сервером 1.34:

```powershell
$env:Path = "$(Resolve-Path .cache\kubectl\v1.35.9);$env:Path"
task kafka-status
task db-status
task publisher-status
task worker-build
task db-deploy
task worker-secret-create
task worker-secret-create
```

`db-deploy` обновляет разрешение worker в NetworkPolicy. Изменение версии chart может заменить PostgreSQL Pod: дождаться готовности и убедиться, что PVC/PV прежние. Не удалять БД, namespace, Kafka topics или тома. Повтор создания Secret должен подтвердить совпадение; содержимое Secret не выводить.

Проверить реальный предел соединений:

```powershell
kubectl --context kind-kubernetes-application-lab exec pod/lab-postgres-0 -n kubernetes-application-lab -- psql -X -U lab -d lab -c "SHOW max_connections;"
task worker-deploy
task worker-status
task worker-group-status
```

Бюджет пулов — 12 соединений при API/publisher/worker по одной реплике, 16 при двух worker, дополнительно временные реплики и административные клиенты. Сравнить с фактической конфигурацией БД, не только с наличием свободной памяти. `worker-group-status` создаёт временный клиент, который не вступает в группу и не меняет offsets; Pod остаётся для доказательств.

## 3. Отдельные окна наблюдения

Окно 2 — API port-forward. Если такой процесс уже работает, второй не запускать:

```powershell
kubectl --context kind-kubernetes-application-lab port-forward deployment/lab-api 18081:8080 -n kubernetes-application-lab --address 127.0.0.1
```

Окно 3 — журналы текущих worker:

```powershell
kubectl --context kind-kubernetes-application-lab logs -f --timestamps --prefix -l 'app.kubernetes.io/name=lab-worker,app.kubernetes.io/instance=lab-worker' --all-containers=true --max-log-requests=5 --tail=200 -n kubernetes-application-lab
```

После изменения числа реплик открыть эту команду заново: selector выбирает текущие Pod, а не гарантирует подписку на будущие. До остановки сохранить журнал выбранного процесса. Для каждого назначения нужны Pod name/UID, revision, group, partitions и времена; число Ready Pod само по себе не доказывает распределение.

## 4. Одна новая заявка

В основном окне:

```powershell
task job-enqueue
task job-status
task outbox-status
task worker-group-status
```

При необходимости повторить `job-status` после завершения работы. Сопоставить один job/event ID: publisher ack → `worker_received` → `worker_work_started` → `worker_effect_committed` → `worker_offset_committed`. Ожидаются `succeeded`, непустые `completed_at` и результат `outcome=completed`, одна строка эффекта. В SQL-снимке сохранить результат до следующего опыта. Committed offset должен достигнуть как минимум offset обработанной записи + 1; более поздние записи могли уже увеличить его.

Новая группа читает earliest доступных записей. Старые три опубликованные заявки могут обработаться автоматически, если сообщения ещё не истекли по retention. Их существование в PostgreSQL не доказывает сохранность в Kafka; основной опыт проводить на новой заявке.

## 5. Дубликат без повторного SQL-эффекта

```powershell
task worker-replay
task job-status
task outbox-status
task worker-group-status
```

По умолчанию используется последняя одиночная заявка из `.local-kafka/job-locator.json`; явный выбор — `task worker-replay KEY=<job_UUID>`. Команда намеренно публикует прежний event новым Kafka record, используя publisher identity в отдельном Pod. Обычный worker по-прежнему только читает основной topic.

Ожидаются `worker_event_republished` с новым offset, `worker_duplicate_ignored`, затем commit нового offset. Число эффектов, `completed_at` и результат должны остаться прежними. Outbox сохраняет исходный publisher offset: replay не переписывает его. Это опыт дедупликации при повторной публикации, **не** доказательство аварийной redelivery после SQL commit.

## 6. Две реплики и rebalance

```powershell
task worker-deploy-two
task worker-status
task worker-group-status
task worker-batch
task worker-group-status
```

Перезапустить окно 3 после появления второго Pod. В стабильной группе ожидаются два member, распределение трёх partitions как 2+1 без пересечения. `worker-batch` выдаёт 12 разных заявок и ждёт `succeeded` с результатом для каждой, сохраняя ID в `.local-kafka/worker-batch.json`. Коррелировать выполнение с Pod и partition: случайные ключи не гарантируют равномерного распределения всех 12 записей.

Диагностика отдельно читает offsets и log end: это не атомарный снимок. `lag=null` при отсутствии committed offset не означает нулевой lag. После остановки нагрузки повторить снимок. Успешный batch сам по себе не доказывает корректность rebalance — нужны назначения и отсутствие пропущенных эффектов/ошибочных commit.

## 7. SIGTERM во время принятой работы

Вернуть одну реплику, дождаться стабильной группы и снова открыть её журнал в окне 3:

```powershell
task worker-deploy
task worker-group-status
task worker-long-job
```

Дождаться в серверном журнале `worker_work_started` именно для новой восьмисекундной заявки. **Пока работа выполняется**, в основном окне:

```powershell
task worker-stop
task job-status
task outbox-status
task worker-group-status
```

Сохранить `worker_work_started` → `worker_shutdown_requested` → результат и commit → `worker_shutdown_completed`, удаление Pod и `succeeded` через API. `worker_shutdown_requested` означает отмену общего контекста: при данном опыте она вызвана SIGTERM; сбой health listener также инициирует завершение. Если остановка пришла до принятия записи или после её завершения, это не доказательство завершения принятой работы — повторить с новой заявкой. Заявка, оставшаяся неподтверждённой из-за rebalance/тайм-аута, должна повториться после восстановления, а не молча потеряться.

```powershell
task worker-deploy
task worker-status
task worker-group-status
task publisher-status
task db-status
task smoke
```

`worker-stop`/`worker-deploy*` меняют число реплик через Helm, без второго менеджера поля replicas. Ожидаемый возврат: одна готовая реплика worker, API/publisher/PostgreSQL доступны, выполненные заявки и прежние PVC сохранены.

## 8. Завершение и границы

После сохранения журналов диагностических Pod:

```powershell
task worker-diagnostics-cleanup
```

Удаляются только Pod с меткой диагностического клиента. Очистка не удаляет jobs/outbox, группу, topics, Secret или PVC и не сбрасывает offsets. Port-forward завершить через Ctrl+C, если он больше не нужен.

Владелец выполнил предварительные проверки, изолированный SQL-тест, enqueue/replay, batch с двумя worker, SIGTERM-повтор с сохранённым журналом, восстановление и очистку. Результаты и границы сохранены в [отчёте Passed](../verification/kafka-worker.md). При повторе сохранять собственные SHA/образ, SQL-снимки, client/member ID, назначения, offsets и полный журнал до удаления Pod. Авария между SQL и Kafka commit, конечные retry/DLQ, настоящий broker outage и CronJob имеют последующие отдельные [отчёты](../verification/README.md); результат базового worker не подменяет их доказательства.
