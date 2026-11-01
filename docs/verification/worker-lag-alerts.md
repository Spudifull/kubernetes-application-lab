> Historical result from the private development history. Original identifiers are preserved; this is not a rerun of the public snapshot. Reviewed screenshots are included; raw captures remain private. See [provenance](../provenance.md). Local paths, where present, were redacted.

# Оповещения при остановке worker

- Статус: `Passed` для двух циклов при контролируемой остановке worker, 2026-11-01.
- Подготовка наблюдателя: commit `7d194dc`. Выполнение версии с KEY подтверждено; отдельный снимок HEAD/чистоты дерева в присланных окнах отсутствует.
- Образ приложения: revision `0b77cd84de5e`.
- Среда: основной kind, Kubernetes 1.34.
- Контракт: [оповещения](../design/alerting.md). Процедура: [инструкция](../engineering/alerting-lab.md#следующий-опыт-lag-и-отсутствие-обработки-worker).

## Цель и исходные условия

Проверить два независимых цикла Pending → Firing → приём внутренним receiver → Resolved для KafkaConsumerLagHigh и WorkerProcessingStalled. Исходно worker Ready=1, группа Stable/один участник, lag=0 по трём partition, outbox pending=0, правила inactive. Kafka и publisher работают.

## Отказ и механизм

worker-stop задаёт ноль реплик через Helm. API принимает одно задание, publisher отправляет его в Kafka, consumer отсутствует. Число брокеров не должно меняться. Kubernetes обеспечивает изменение числа реплик; приложение и Kafka обеспечивают последующую обработку накопленного сообщения после восстановления.

## Наблюдавшиеся признаки

Владелец выполнил go test -count=1 -race ./cmd/labctl (1.205s), go vet ./cmd/labctl и git diff --check без ошибок. Агент эти проверки повторно не выполнял. Архив (private original artifact omitted; see provenance) содержит JSON двух наблюдателей (62 и 60 снимков), события восстановления, строки SQL данного задания и SHA256 трёх исходных вложений.

Все времена UTC. Job `b892e2c2-ce9d-4fa9-8b70-2e3b2770c346`, event `bb20efc2-2292-495d-9588-e54a53d2ec23`, trace `c48a653679b485022201ca7f5b3358f4`.

| Признак | KafkaConsumerLagHigh | WorkerProcessingStalled |
|---|---|---|
| Исходное inactive / lag=0 | 16:19:50.5597002 | 16:20:00.9607369 |
| Первый наблюдаемый Pending / lag=1 | 16:21:55.8160328 | 16:21:56.2168053 |
| Первый наблюдаемый Firing / lag=1 | 16:22:55.8124916 | 16:23:56.210131 |
| Firing принят receiver | 16:22:53.876350096 | 16:23:53.859387040 |
| Fingerprint | eff9d2afec534c55 | 8528c6f24c0e9b11 |
| Resolved принят receiver | 16:24:53.865134112 | 16:24:53.860906898 |
| alert_cycle_completed | 16:24:55.8453314 | 16:24:56.2449927 |

Firing startsAt: соответственно 16:22:39.146 и 16:23:39.146; у resolved сохранены те же startsAt/fingerprint, endsAt обоих 16:24:39.146. Приём webhook может предшествовать первому опросу, увидевшему Firing. Старые outbox receipts не использованы для новых циклов. Время старта receiver во всех снимках 15:07:33.792549256Z, evicted=0.

API принял задание в 16:20:39.635205 с HTTP 202. Publisher опубликовал его в partition 0 / offset 15; SQL показывал published, effects=0. В 16:21:15 группа Empty, members=0; committed=15, end=16, lag=1 для partition 0, остальные две lag=0. Причина задержки — отсутствие consumer, а не отказ publisher или брокера.

## Восстановление и диагностика

После tracing-worker-deploy создан Pod lab-worker-866ddc4dd7-88865 вместо lab-worker-866ddc4dd7-nr2kw. Задание succeeded в 16:24:09.377990, SQL показывает одну outbox row и один effect. В 16:24:35 группа Stable/один участник; committed=end по partition: 16/16, 14/14, 17/17, все lag=0. Оба наблюдателя зафиксировали lag=0 до перехода правил в inactive; интервал до Resolved соответствует оценке и доставке оповещений.

logging-verify вернул восемь строк API/publisher/worker с общими job/event/trace ID, включая effect и offset commit. application-trace-read подтвердил семь проверяемых spans цепочки (это не общее число spans); полный ответ Tempo и новый скриншот здесь не архивированы. prometheus-targets, application-metrics-verify, dependency-metrics-verify и smoke завершились без ошибок: 69 записей targets в трёх проверках, все UP. Smoke сообщил revision 0b77cd84de5e и удалил свой Pod.

## Что результат подтверждает

Оба правила независимо прошли Pending/Firing, стали активными в Alertmanager, доставили firing/resolved во внутренний receiver и вернулись в inactive при известном lag=0. Ранее опубликованное задание обработано после возвращения worker с одним SQL-эффектом. Неизвестные или исчезнувшие метрики не использованы как доказательство восстановления.

## Границы и остаточный риск

Этот опыт не доказывает crash/redelivery после SQL commit, HA брокера, долговечную доставку уведомлений или поведение при недоступной telemetry. Receiver хранит данные в памяти; отсутствие его рестарта/вытеснения подтверждено только за окно опыта. Очистка оставленных диагностических Pod в присланном выводе не подтверждена. Отдельные файлы alert-cycle-proof.json не присланы; полный цикл проверен по сохранённым JSON консоли и событиям завершения.
