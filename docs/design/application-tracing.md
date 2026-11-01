# Трассировка задания через outbox и Kafka

**Состояние:** обычный путь одного задания через три сервиса, SQL/offset и просмотр Grafana имеют [Passed в ограниченном объёме](../verification/application-tracing.md). Перенос контекста после остановки/запуска publisher с новым UID также подтверждён. Два контролируемых отказа worker с последующим успехом и сохранением контекста также подтверждены. Исчерпание трёх попыток и переход в DLQ подтверждены журналами, SQL, чтением DLQ и просмотром трассировки Grafana; полный ответ Tempo сохранён, parent ID и порядок завершающих DLQ/SQL/offset spans сверены. Обработка одного задания при недоступном telemetry endpoint подтверждена: три ошибки экспорта, succeeded/effect=1, Ready и lag=0. Профили и доставка новой трассировки восстановлены; SDK disabled подтверждён отдельным заданием с пустым trace_id и одним эффектом; 31 снимок шести workloads за 15 минут подтвердил отсутствие перезапусков в лёгком режиме. Разрывы dashboard не объяснены; сравнение накладных расходов и предельная нагрузка не проверены. Проверенный [транспорт Collector/Tempo](tracing-infrastructure.md) действует независимо от этой итерации. Порядок запуска — [инструкция](../engineering/application-tracing-lab.md).

## Границы и распространение контекста

API, publisher и worker используют OpenTelemetry Go SDK 1.44.0 и OTLP/HTTP exporter. Небольшая HTTP-обёртка выбирает только `POST /jobs` и `GET /jobs/{id}`: она не добавляет URL, query, тело или произвольные заголовки в атрибуты. Проверки готовности и `/metrics` не создают spans. Ответ задания содержит `X-Lab-Trace-ID` и `traceparent`, когда контекст действителен. Идентификатор ответа GET относится к этому GET, а не обязательно к исходной постановке задания.

Миграция `004_outbox_trace_context.sql` добавляет `lab_outbox.trace_parent`: пустая строка для прежних событий либо 55 символов W3C `traceparent`. Миграции 001–003 не изменяются. Новые API и publisher требуют 004 даже при выключенном SDK; миграция выполняется до их обновления. Прежние бинарные файлы совместимы с добавочным столбцом. Откат приложения не требует удаления столбца или данных.

Контекст SQL span постановки сохраняется в той же транзакции, что job и outbox. Он исключён из бизнес-payload и `request_hash`. Повтор по тому же idempotency key сохраняет первый контекст. Publisher восстанавливает его после claim; каждый publish attempt получает отдельный span. Claim измеряется до извлечения контекста и записывается с фактическими началом и концом; пустые опросы не создают трассировок.

Producer добавляет единственный Kafka header `traceparent`. Worker извлекает его через стандартный W3C propagator, создаёт consumer span и дочерние SQL/work/offset spans. Повтор обработки создаёт соседний consumer span под тем же producer. Учебная повторная публикация берёт сохранённый контекст outbox; она не создаёт producer span от диагностического процесса. DLQ producer также передаёт контекст, но бизнес-конверт DLQ не меняется. Прежнее событие без контекста начинает новую трассировку на границе процесса.

Выбрана модель parent для обработки одного сообщения, допустимая в [OpenTelemetry messaging](https://opentelemetry.io/docs/specs/semconv/messaging/messaging-spans/). HTTP span заканчивается при ответе; асинхронные потомки могут начаться после его завершения. Ожидание outbox не изображается непрерывным HTTP-вызовом. Broker ack — событие publisher, а не завершение задания. Отметка published_at и consumer SQL effect — разные операции. Порядок SQL → Kafka offset commit сохраняется.

Разрешён только `traceparent`: `tracestate`, baggage и прочие заголовки не сохраняются и не распространяются. Неверный, чрезмерный или неоднозначный контекст отбрасывается без отказа бизнес-запросу. Из Kafka принимается не более 64 заголовков для разбора, сам traceparent ограничен 55 символами. Отсутствие контекста не означает ошибку задания.

## Spans и журналы

| Процесс | Основные spans / события |
|---|---|
| API | HTTP route template, jobs.validate, jobs.enqueue.transaction, jobs.get; HTTP status и ошибка БД |
| Publisher | outbox.publish_attempt, outbox.claim, lab.jobs.v1 publish, broker_ack, retry_scheduled, outbox.mark_published.transaction, outbox.schedule_retry |
| Worker | lab.jobs.v1 process, jobs.start.transaction, worker.work, retry state/failure, jobs.complete.transaction, DLQ prepare/publish/mark, lab-workers commit, duplicate_observed, attempt_canceled |

SQL spans используют фиксированные имена без SQL-текста, параметров и результатов строк. Ошибки экспортируются фиксированными классификациями без исходного driver/broker error. Допустимые диагностические атрибуты: job/event ID, partition, offset и номер попытки. Они не добавляются в Prometheus labels.

Сохраняется slog: JSON, UTC `time`, `service`, `event`, прежний `msg`, Pod и предметные поля события. Операции задания добавляют trace_id/span_id; идентификаторы могут существовать и у несэмплированного контекста. Наличие записи в журнале не доказывает доставку span в Tempo. Централизованный сбор Loki относится к отдельной итерации.

## Sampling, пределы и отказ

В базовых Helm values SDK выключен. При включении применяется ParentBased + 10% новых корневых трассировок; решение действительного родителя сохраняется, поэтому это не ограничитель входного трафика. Короткий диагностический профиль задаёт 100%. Оба значения и endpoint задаются через Helm values; профиль отказа меняет только endpoint на закрытый loopback порт.

В каждом процессе: очередь 128 spans, batch 32, flush interval 1s, export timeout 1s, повторные попытки экспорта ограничены 1s. Enqueue SDK неблокирующий, при переполнении telemetry теряется. Ограничения span: 32 атрибута, значение до 256 символов, 16 событий, 8 links. Завершение SDK ограничено 2s; существующие бюджеты Pod не увеличены: API 20s, publisher 35s, worker 30s. Суммарные верхние бюджеты API: preStop 3s + drain 3s + HTTP shutdown 10s + SDK 2s; publisher: attempt 25s + SQL cleanup до 2s + health 3s + SDK 2s; worker: attempt 18s + leave до 2s + health 3s + SDK 2s.

Некорректная локальная конфигурация SDK запрещает старт. Недоступность Collector/Tempo не участвует в readiness/liveness и не меняет транзакции или offsets. Сообщение tracing_export_failed ограничено одним за 30s и не раскрывает исходную ошибку. Очередь в памяти не обеспечивает сохранность spans при SIGKILL, переполнении или длительном отказе. Эти границы требуют опыта; сквозная доставка, накладные расходы и завершение процесса пока не подтверждены.

## Подготовка crash/redelivery диагностики

tracing-worker-crash использует существующий job-scoped crash hook с tracing profile, без ожидания readiness аварийного workload. Нормальный tracing-worker-deploy явно очищает fault через прежний Helm-путь. Выполненный runtime-опыт имеет [Passed](../verification/worker-crash-observability.md); частичная трассировка аварийной попытки сохранена без восстановления отсутствующих spans.

application-trace-inspect сохраняет фактический ответ Tempo и нормализованные spans/события, сообщает число process spans worker и отсутствующие parent IDs. Он допускает частичную трассировку аварии, но отвергает чужой trace ID, дубликаты span ID и неверные интервалы/идентификаторы. Число spans не утверждает SQL-успех или завершённость попытки. Обычный application-trace-read сохраняет прежнюю строгую проверку успешной цепочки. os.Exit не выполняет deferred End/flush: пустоты первой попытки не восстанавливаются искусственно. Тот же инспектор пригоден для просмотра failed/DLQ trace без ложного требования succeeded.
