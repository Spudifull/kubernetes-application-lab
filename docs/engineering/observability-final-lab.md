# Заключительная проверка Milestone 8

Проверенные сценарии сохраняются в отдельных отчётах; эта процедура закрывает оставшиеся пробелы, не требует повторять все шесть alert cycles. [Покрытие панелей](../verification/grafana-dashboards.md) содержит соответствие четырём группам брифа. Весь этап до выполнения остаётся незавершённым.

## Обновить версионированные панели

Владелец выполняет go test -count=1 -race ./cmd/labctl, go vet ./cmd/labctl, git diff --check, observability-helm-lint, observability-render. После просмотра rendered YAML применить observability-install, затем grafana-status, prometheus-targets и smoke. Эта установка может пересоздать Grafana; старый port-forward после смены Pod нужно закрыть и открыть заново. Приложение и его образы пересобирать не требуется.

На /d/lab-application-metrics и /d/lab-dependency-metrics проверить новые панели и ссылки Loki/Tempo. Сохранить скриншоты с данными и диапазоном времени. Небольшая серия GET по созданному job в течение минуты даёт трафик для latency; из одного запроса длительную нагрузку не выводить. Средняя и p95 имеют разные определения. Данные новых панелей и успешный provisioning нужны до Passed.

## Завершающий DLQ-путь

API/publisher/worker имеют штатный tracing profile, Collector/Loki/Tempo исправны, fixed worker=1, нет активного отказа. В отдельном окне API forward 18081. В основном окне worker-stop → job-enqueue → worker-retry-exhaust → tracing-worker-deploy. Worker должен быть остановлен до постановки задания и настройки SQL-отказа; publisher останавливать не требуется. Если какой-либо шаг после worker-stop завершился ошибкой, восстановить worker через tracing-worker-deploy в finally. Не создавать другие задания до сохранения всех ID и результатов.

После трёх неудачных попыток сохранить job-status, worker-reliability-status, worker-dlq-read, outbox-status, worker-group-status и метрики worker-retries/worker-dlq. Нужны failed/retry_exhausted, failure_count=3, effects=0, SQL-квитанция и совпадающая DLQ-запись, pending/lag=0. Новый alert при этом возможен; ранее доказанные уведомления не выдаются за доказательство этого нового запуска.

Через 30 секунд после терминального состояния выполнить logging-query и application-trace-inspect. Если Loki ещё не вернул терминальные строки, повторить чтение после очередного интервала; повторять задание нельзя. В Loki требуются worker_dlq_broker_ack → worker_dlq_recorded → worker_offset_committed с общими job/event/source координатами. Сохранить также прямой журнал worker. Полный ответ Tempo должен содержать попытки и конечные DLQ publish → SQL-квитанция → commit с соответствующими parent IDs и интервалами. application-trace-read для succeeded здесь не применять; инспектор сохраняет spans, но сам по себе не доказывает конечную DLQ-цепочку. Архив проверяет агент после предоставления вывода.

В Grafana открыть trace ID из новой постановки, раскрыть завершающие spans и сохранить скриншот. В Loki фильтровать этот Job ID и сохранить терминальные строки. Не удалять DLQ, квитанции, offsets или PVC ради очистки.

## Итоговое состояние

Владелец выполняет go test -count=1 -race ./..., go vet ./..., профильные Helm lint для observability/dependencies/logging/alerting и application-tracing-helm-check. Повторить prometheus-targets, application-metrics-verify, dependency-metrics-verify, grafana-status, tracing-status, logging-status, smoke, outbox-status и worker-group-status. Сохранить Helm releases, Pod readiness/restarts/resources, PVC, CronJob и KEDA/ScaledObject/HPA; установленный KEDA без активного ScaledObject — допустимый fixed-worker режим. Секреты и полные Secret YAML не выводить. Проверить собранную telemetry на секретные поля, SQL-тексты и payload, не обещая глобальный аудит всех исторических данных.

После сохранения результатов worker-diagnostics-cleanup удаляет только диагностические Pod. Остаточный DLQ alert может естественно завершиться после выхода increment из окна 5m; не обнулять счётчики перезапуском ради Resolved. Финальное закрытие связывает результаты с отдельными отчётами, перечисляет ограничения и не подменяет их статусом одного сводного файла.
