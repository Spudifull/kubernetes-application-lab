> Historical result from the private development history. Original identifiers are preserved; this is not a rerun of the public snapshot. Reviewed screenshots are included; raw captures remain private. See [provenance](../provenance.md). Local paths, where present, were redacted.

# Отсутствие Collector при работающем приложении

- Статус: `Passed` для временной остановки настоящего Collector и последующего восстановления доставки новой трассировки.
- Ревизия сценария: `e3165c8a1da71182c597e95ad89cc1818cf93d73`, перед прогоном показано чистое дерево. Ревизия приложения в smoke: `0b77cd84de5e`; точные образы и imageID трёх процессов сохранены в архиве.
- Среда: основной kind, Kubernetes 1.34, kubectl 1.35.9.
- Процедура: [отказ Collector](../engineering/collector-outage-lab.md).
- Контракт: [прикладная трассировка](../design/application-tracing.md).

## Цель и исходные условия

Проверить независимость обработки задания от настоящего OTLP Collector. Исходно Collector=1, приложение в tracing profile, одна реплика worker, исправные PostgreSQL/Kafka/Tempo/Loki, pending/lag=0. Ревизию сценария, образы, UID и счётчики перезапусков сохранить перед выполнением.

## Отказ и ожидаемые признаки

Уменьшить Collector до нуля, дождаться удаления его Pod и отсутствия готовых endpoints. При прежнем endpoint приложения выполнить новое задание. Ожидаются ошибки tracing_export_failed, HTTP 202 → succeeded, один SQL-эффект, pending/lag=0 и неизменные UID/restarts готовых процессов приложения. Kubernetes не должен перезапускать приложение из-за отсутствия приёмника трассировок.

## Восстановление

Вернуть Collector до одной реплики в finally, дождаться rollout. Новым заданием подтвердить обработку и связанную трассировку; повторить проверки targets/метрик/smoke. Сохранить предыдущие журналы и идентификаторы обоих заданий.

## Наблюдавшиеся признаки

Прогон владельца 2026-11-01 сохранён в архиве доказательств (private original artifact omitted; see provenance). Агент проверил предоставленный вывод и локальные файлы, команды кластера не повторял. Исходно Collector Ready=1/1, pending=0, группа Stable/один участник, lag всех трёх partitions=0. Контрольное задание `1cf724d3-3624-40c9-942d-c7a409020e4d` завершилось успешно; трассировка `0debbce567148bb07222cbc155766561` прошла проверку связной цепочки из семи выбранных spans.

collector-stop уменьшил Deployment до нуля и дождался удаления Pod `lab-otel-collector-659f97ff44-mcvpk`. В выводе Deployment Ready=0/0, AVAILABLE=0. У EndpointSlice `lab-otel-collector-947tq` endpoints=null, last-change-trigger-time=17:59:08Z. Это фактическое отсутствие приёмника, без подмены endpoint приложения.

Во время отказа POST в 17:59:10.0730373Z вернул 202 для job `f9750fef-ad18-4cd4-b4aa-af0d5db1078d`, event `b3ff1254-bfad-4e8e-997a-a02f0183306d`, trace `56f8b1bfdcc72a4729a1339738219834`. SQL completed_at=17:59:11.311794Z, succeeded, duration_ms=1000, effects=1, outbox_rows=1; запись опубликована в lab.jobs.v1 / partition 0 / offset 19. В 18:00:44 группа Stable/один участник, committed=end по partitions 20/20, 14/14, 19/19; везде lag=0. Pending=0. Loki вернул восемь связанных строк, включая worker_effect_committed и worker_offset_committed.

Журналы сохранили tracing_export_failed/error_code=telemetry_unavailable: API в 17:59:10.819854373Z и 18:00:40.844571125Z, publisher в 17:59:11.055094205Z, worker в 17:59:11.233659647Z. Прямые запросы curl --fail к /livez и /readyz вернули alive и ready. В трёх снимках до отказа, во время и после него совпадают UID, containerID, imageID и restartCount=0 всех трёх процессов; ready=true. Готовность между снимками непрерывно не измерялась, но замены контейнера или перезапуска в этом интервале не зафиксировано.

Блок finally выполнил collector-start; rollout завершился успешно. Снимок 18:02:09.1033479Z подтвердил replicas=1 и readyReplicas=1. Новое задание `edbc31cc-d1e8-4371-a4b2-b51190343ac8` принято в 18:02:09.8100103Z и завершено в 18:02:11.518844Z; effects=1, outbox_rows=1, partition 0 / offset 20. Трассировка `94b79d0f56d79d5aa0f0693cea4162af` в 18:02:26 прошла проверку семи выбранных spans: три сервиса, связность родителей и SQL completion перед offset commit. Фактический ответ Tempo (private original artifact omitted; see provenance) сохранён отдельно. Семь — размер проверенной цепочки, а не утверждение общего числа spans.

После восстановления группа Stable, committed=end 21/21, 14/14, 19/19 и lag=0. Targets/application-metrics-verify/dependency-metrics-verify прошли: 69 наблюдений targets в трёх проверках, все UP; smoke завершился успешно, его Pod удалён. После сохранения отчёта владелец сообщил об очистке диагностических worker Pod; отдельный вывод удаления не предоставлен.

## Причина и распределение ответственности

Остановка Deployment убрала приёмник OTLP. SDK сообщил об ошибках экспорта, сохранив работу прикладного HTTP/SQL/Kafka пути; это не ошибка обработки задания. Kubernetes выполнил явное масштабирование Collector до нуля и обратно, а приложение не перезапускал. Возобновление доставки доказано новой трассировкой после восстановления, без изменения образов приложения.

## Граница доказательства

Опыт ограничен временной недоступностью Collector. Отказы exporters метрик, долговечность очередей SDK и гарантированная доставка всех spans периода отказа не проверяются. Частичные трассировки не дополняются вымышленными spans.
