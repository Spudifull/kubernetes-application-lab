# Проверка трассировки настоящего задания

Контракт — [application-tracing](../design/application-tracing.md), обычный путь и перенос контекста после замены publisher Pod имеют [Passed в ограниченном объёме](../verification/application-tracing.md), остальные опыты — Prepared. Все команды выполняет владелец. Инфраструктуру Collector/Tempo повторно устанавливать не нужно, если она исправна. Основной kind/Kubernetes 1.34, совместимый kubectl, фиксированный worker=1; Cron suspended, отсутствие активных отказов, pending/lag=0.

## Подготовка и обновление

1. В каждом PowerShell-окне добавить `.cache/kubectl/v1.35.9` в PATH. Сохранить HEAD, git status, прежние Helm revisions, images/imageIDs и UID workloads/PVC. Не удалять PVC, topics или offsets.
2. Выполнить `go test -count=1 -race ./...`, `go vet ./...`, `git diff --check`, `task application-tracing-helm-check`. Просмотреть rendered profiles: три SDK env, внутренний endpoint, sampling=1, прежние limits, probes и securityContext. Новых ресурсов приложения или прав не требуется.
3. Отдельно выполнить `task jobs-sql-integration-test` с прежним явным включением и новой изолированной `lab_jobs_test_<UUID>` БД. Использовать порядок безопасной подготовки/очистки из [процедуры SQL integration](kafka-lab.md#отдельный-sql-integration-тест). Проверка дополнена переносом traceparent и сохранением первого контекста при идемпотентном повторе. Основную БД для теста не использовать.
4. После исправлений создать commit, сохранить SHA и чистый git status. Собрать `task image-build`, `task publisher-build`, `task worker-build`. Проверять новые образы; старый образ не содержит SDK.
5. Выполнить `task db-migrate`, `task db-migrate-status`: необходима успешная 004. Затем последовательно `task tracing-api-deploy`, `task tracing-publisher-deploy`, `task tracing-worker-deploy`. Обычные deploy-команды выключают диагностический профиль; не подменять ими эти три шага.
6. Выполнить `task tracing-status`, `task application-metrics-verify`, `task dependency-metrics-verify`, `task smoke`, `task outbox-status`, `task worker-group-status`. Сохранить реальные образы, revision в startup logs, UID и resources трёх процессов.

## Окна и обычное задание

В отдельном окне держать API port-forward на `127.0.0.1:18081`; в трёх других — журналы deployment/lab-api, deployment/lab-publisher, deployment/lab-worker с `--timestamps`. Grafana открывается через `task grafana-forward`. Приложение отправляет spans во внутренний Collector; `collector-forward` ему не нужен. `application-trace-read` читает Tempo через Kubernetes API и также не требует tempo-forward.

В основном окне:

```powershell
task job-enqueue
Start-Sleep -Seconds 5
task job-status
task outbox-status
task worker-group-status
task application-trace-read
```

`job_enqueue_response` содержит trace_id; locator `.local-kafka/job-trace-id` используется последней командой. При задержке экспорта повторить чтение, не создавать повторно задание. Команда сохраняет фактический ответ Tempo и verified-span-chain.json: ID, parent ID, имена сервисов и интервалы SQL-before-offset. В Grafana Explore выбрать Tempo и открыть этот trace ID. Проверить POST, enqueue transaction, publisher/producer, consumer, SQL effect и offset; сохранить скриншот. `job-status` должен показать succeeded, outbox-status — один effect и одну строку outbox. Проверить в реальном ответе Tempo также claim, broker_ack и published_at span. Отсутствующие spans не считать доставленными по наличию ID в журнале.

GET состояния — отдельный HTTP-запрос; при необходимости открывать его trace ID отдельно. Для идемпотентного повтора исходный бизнес-путь остаётся под первым durable traceparent, а новый POST имеет собственный HTTP span. Для основного доказательства использовать новое задание, а не concurrent probe.

## Перезапуск, retry и DLQ

Остановить publisher через `task publisher-stop`, поставить одно новое задание, сохранить trace ID, outbox pending и trace_parent. Удалить остановленный Pod не требуется. `task publisher-start` возвращает тот же Helm-профиль, но новый процесс: проверить startup revision/UID и восстановление цепочки через `task application-trace-read TRACE_ID=<ID>`. HTTP уже завершён; consumer должен иметь тот же trace ID. После обработки pending/lag=0 и effect=1.

Для retries/DLQ использовать уже проверенные `worker-retry-success` / `worker-retry-exhaust` из worker lab, предварительно остановив worker. Вернуть его именно через `task tracing-worker-deploy`: обычный worker-deploy сбрасывает профиль. Сохранить trace ID каждой новой постановки до следующего enqueue. Проверить несколько consumer attempts под одним producer, retry events и SQL spans. При исчерпании попыток ожидаются DLQ producer/receipt и failed без успешного SQL effect; обычный application-trace-read предназначен для succeeded и закономерно не подтвердит такую цепочку. Для DLQ проверять фактический ответ Tempo/UI и соответствующие SQL/Kafka доказательства. Некорректные события без traceparent не имеют исходного POST.

## Недоступность telemetry и отключение SDK

После нормального опыта выполнить три команды `tracing-api-unavailable`, `tracing-publisher-unavailable`, `tracing-worker-unavailable`. Они разворачивают те же recorded images с endpoint `127.0.0.1:14319` внутри каждого Pod. Перезапустить API port-forward после смены API Pod. Поставить новое задание и подтвердить HTTP 202 → succeeded, один effect, outbox/lag=0, готовность процессов и отсутствие перезапусков. Сохранить фиксированные tracing_export_failed; отсутствие свежей трассировки при отказе ожидаемо. Не останавливать настоящий Collector или Tempo и не изменять брокер ради этого опыта.

Восстановить тремя `tracing-*-deploy`, перезапустить API port-forward и подтвердить новое задание со всеми spans. Для SDK disabled выполнить `jobs-api-deploy`, `publisher-deploy`, `worker-deploy`, снова открыть port-forward и поставить задание: тот же SQL/offset результат без нового trace ID при отсутствии входного traceparent. Прежние данные и spans не удаляются.

Снять не менее 15 минут CPU/memory/RESTARTS API/publisher/worker и Collector/Tempo/Grafana, сохранить начало/конец, UID и imageIDs. Проверить штатное завершение без force-kill и прежние targets/smoke. После фиксации закрыть временные port-forward, удалить только диагностические Pods установленными cleanup-командами. Оставить один fixed worker, правильные bootstrap и выключенный SDK либо явно записать сохранённый диагностический профиль. Миграция 004 остаётся.
