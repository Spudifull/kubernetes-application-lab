> Historical result from the private development history. Original identifiers are preserved; this is not a rerun of the public snapshot. Reviewed screenshots and selected archived evidence are included; attachment dumps remain private. See [provenance](../provenance.md) and the [evidence index](README.md#первичные-доказательства-milestone-8). Local paths, where present, were redacted.

# Проверки и лабораторные сценарии

- [Ветки, контейнерная проверка и окружения Milestone 9](branching-container.md) — текущие проверки и границы Prepared/runtime.
- [GHCR и development GitOps](gitops-development.md) — registry identity и первая manual Argo CD sync с фактическим Pod imageID.

## Первичные доказательства Milestone 8

Источник 22 файлов — приватный коммит `a8cf93bcc3c4a6316550a58e009d3406f80cd599`. Original означает неизменность относительно этого архива, включая ранее подготовленные выборки. Attachment dumps не включены. Учебные IP, offsets и trace ID сохранены.

В двух Sanitized-файлах только абсолютный путь репозитория в PowerShell prompt заменён на `[repository]`: строки commands итоговых проверок и http_excerpts PostgreSQL. Причина — удаление пользовательского пути. Имена файлов, измерения, timestamps, статусы сохранены. Исходный SHA-256 итоговых проверок: `8ae0de08e5bd5480c546db438ff73b8b212e5e6f9873fc1b4e769b3ceff18ea7`; PostgreSQL: `1ff505c31fcc617a890b012845b595e29487e67ad5a66d426d22d1c69904e1a8`. Для Original исходный SHA-256 равен публичному.

| Файл / сценарий | Тип / решение | Проверяемые данные | Отчёт | Публичный SHA-256 |
|---|---|---|---|---|
| [alerting-outbox-cycle-2026-11-01.json](evidence/alerting-outbox-cycle-2026-11-01.json) | JSON / Original | События и наблюдаемые значения | [Отчёт](alerting.md) | `d84fbba26af21a36252d9a193998ce02e8ea86090cb5f069f0583076a7a6d89d` |
| [alerting-publisher-recovery-2026-11-01.json](evidence/alerting-publisher-recovery-2026-11-01.json) | JSON / Original | События и наблюдаемые значения | [Отчёт](alerting.md) | `29a42e5032194e5f6b28b6c0a7b10d4fe6a8aa58328568ce6ea260e62aed38ff` |
| [alerting-publisher-trace-2026-11-01.json](evidence/alerting-publisher-trace-2026-11-01.json) | JSON / Original | Связи spans | [Отчёт](alerting.md) | `6f1c60f023d22202925514b385b1221cd3b978d1d4048773716dd45f9d5bf69b` |
| [api-unavailable-partial-2026-11-01.json](evidence/api-unavailable-partial-2026-11-01.json) | JSON / Original | Частичный результат и отказ наблюдателя | [Отчёт](api-unavailable-alert.md) | `fa8f7ab198804d1f1bb1d7b4808a4a2ffca1d31c39de0d0b5865ad6ff98c5b85` |
| [api-unavailable-recovery-2026-11-01.json](evidence/api-unavailable-recovery-2026-11-01.json) | JSON / Original | События и наблюдаемые значения | [Отчёт](api-unavailable-alert.md) | `9ed4301024aea5b03a831e59230094fd1819557984e67dcd238e9d18132c6c83` |
| [application-logging-events-2026-11-01.json](evidence/application-logging-events-2026-11-01.json) | JSON / Original | События и наблюдаемые значения | [Отчёт](application-logging.md) | `263888e227ab81d04d917e213f5df45f9389762b9c44585e6e100c1e71bb4da6` |
| [application-logging-resources-2026-11-01.json](evidence/application-logging-resources-2026-11-01.json) | JSON / Original | Ресурсы и перезапуски | [Отчёт](application-logging.md) | `20e843e1636405b8491c9a8555e8a9304a40268eb84e722fd393af8efb258501` |
| [application-logging-samples-2026-11-01.txt](evidence/application-logging-samples-2026-11-01.txt) | TXT / Original | События и наблюдаемые значения | [Отчёт](application-logging.md) | `82c2d22b6fea98dfdc486909c9ba8435dd733bd08ac25786e447f89896f24264` |
| [application-tracing-dlq-2026-11-01.json](evidence/application-tracing-dlq-2026-11-01.json) | JSON / Original | События и наблюдаемые значения | [Отчёт](application-tracing.md) | `4ade8c63d9d55776835020d814881657edc6bca1270d6f535bcf30b033e7b389` |
| [application-tracing-resources-2026-11-01.json](evidence/application-tracing-resources-2026-11-01.json) | JSON / Original | Ресурсы и перезапуски | [Отчёт](application-tracing.md) | `6bd8c22fc332d7e51feabfec667db88ddc016b2078eb4a05f69a9197c6d7e0a0` |
| [collector-outage-2026-11-01.json](evidence/collector-outage-2026-11-01.json) | JSON / Original | События и наблюдаемые значения | [Отчёт](collector-outage.md) | `87a0a3ea878c9b7d39975fb882e63ad773612459477d740c0fd7915b4070c1f7` |
| [collector-recovery-trace-2026-11-01.json](evidence/collector-recovery-trace-2026-11-01.json) | JSON / Original | Связи spans | [Отчёт](collector-outage.md) | `a4b3cdfb0273a5e02470bb66f4bbb29650cb711f68a44bb2d6e315abd9abdfa3` |
| [dlq-alert-2026-11-01.json](evidence/dlq-alert-2026-11-01.json) | JSON / Original | События и наблюдаемые значения | [Отчёт](dlq-alert.md) | `064f85648b872bbbafa56b9d5f346d9b67d79e607d6fd110ff0d6feb493c1e17` |
| [final-dlq-correlation-2026-11-01.json](evidence/final-dlq-correlation-2026-11-01.json) | JSON / Original | События и наблюдаемые значения | [Отчёт](dlq-alert.md) | `ade27b58d42f6a20366123fdb772ae48d0b940d43aa44f083d224302867cf684` |
| [final-dlq-trace-2026-11-01.json](evidence/final-dlq-trace-2026-11-01.json) | JSON / Original | Связи spans | [Отчёт](dlq-alert.md) | `1a93aaebe054b804043e80c258722f68ee9a304fb7124e90b382a58f003de00d` |
| [milestone8-final-checks-2026-11-01.json](evidence/milestone8-final-checks-2026-11-01.json) | JSON / Sanitized | События и наблюдаемые значения | [Отчёт](grafana-dashboards.md) | `ff0625c25f585f3fc62432ce71a4f5384203537568de575c3f5b165ffa6da9dc` |
| [postgres-unavailable-alert-2026-11-01.json](evidence/postgres-unavailable-alert-2026-11-01.json) | JSON / Sanitized | События и наблюдаемые значения | [Отчёт](postgres-unavailable-alert.md) | `b5cc962d1391d4ec0a2c9251ddfe13a0fa2a1ec035f036aa14faddb54fb9ed2a` |
| [worker-crash-counter-2026-11-01.json](evidence/worker-crash-counter-2026-11-01.json) | JSON / Original | duplicates 2→3, effects=1, lag=0 | [Отчёт](worker-crash-observability.md) | `5079eaa006d1272b395a12ab2601cd82fea98f46a9730aaf46e11e9830c3d793` |
| [worker-crash-partial-2026-11-01.json](evidence/worker-crash-partial-2026-11-01.json) | JSON / Original | Частичный результат и отказ наблюдателя | [Отчёт](worker-crash-observability.md) | `02afb568b4e8f55f29ebc832c563bbd55f7a2181cf08937a5b5060131ef3a8a4` |
| [worker-crash-recovery-2026-11-01.json](evidence/worker-crash-recovery-2026-11-01.json) | JSON / Original | События и наблюдаемые значения | [Отчёт](worker-crash-observability.md) | `04fe9c63018104b690913757c7b125064d8619b4e0bf11eb5559d4630eaa71dd` |
| [worker-crash-trace-2026-11-01.json](evidence/worker-crash-trace-2026-11-01.json) | JSON / Original | Связи spans | [Отчёт](worker-crash-observability.md) | `f83453221af21048b8afed9f048454c9346ae37aec641007acd03e5c1138b8e1` |
| [worker-lag-alerts-2026-11-01.json](evidence/worker-lag-alerts-2026-11-01.json) | JSON / Original | События и наблюдаемые значения | [Отчёт](worker-lag-alerts.md) | `0a1154abf33a24664fff13cf2473f6a24a7452d40d842d62bab5bfe94d3b2c5c` |


Milestone 8 завершён в границах локального стенда; [итоговая приёмка и ограничения](grafana-dashboards.md#итоговая-приёмка-milestone-8), [архив заключительных проверок](evidence/milestone8-final-checks-2026-11-01.json) (Sanitized).

## Milestone 8 — наблюдаемость

- [Покрытие панелей](grafana-dashboards.md) — `Passed` для обновлённых панелей с реальными samples и просмотром Loki/Tempo; четыре группы в двух dashboard JSON.
- [Отсутствие Collector](collector-outage.md) — `Passed`: задание с одним SQL-эффектом при нуле endpoints, ошибки экспорта без рестартов приложения и новая связная трассировка после восстановления.
- [Crash/redelivery с наблюдаемостью](worker-crash-observability.md) — `Passed`: SQL/offset gap, повторная доставка исходного record без второго эффекта, фактически сохранённые spans и отдельный прирост duplicates 2→3 на одном instance.
- [PostgreSQLUnavailable](postgres-unavailable-alert.md) — `Passed`: полный цикл уведомления, /livez=200 при /readyz=503, восстановление контрольной записи/готовности и прежний PVC.
- [APIUnavailable](api-unavailable-alert.md) — `Passed`: Pending/Firing/Received, восстановление HTTP, matching Resolved и targets UP; цикл завершён отдельным status после тайм-аута исходного наблюдателя.
- [Оповещение DLQ](dlq-alert.md) — `Passed` для failed/effects=0, трёх ошибок, SQL/Kafka-квитанции, роста counters и полного цикла уведомления; новая полная трассировка и терминальные DLQ-события Loki пока не подтверждены.
- [Оповещения lag и остановки worker](worker-lag-alerts.md) — `Passed`: оба Pending/Firing/Received/Resolved, один SQL-эффект и восстановление lag=0.
- [Оповещения outbox](alerting.md) — `Passed` для первого цикла на 05264fa: Pending/Firing/Received/Resolved с одним fingerprint, fresh pending=0, один SQL effect, lag=0. Совместная диагностика publisher через Loki/Tempo и финальное здоровье подтверждены. Другие циклы и границы указаны в отдельных отчётах.

- [Централизованные журналы](application-logging.md) — `Passed` в ограниченном объёме на d92960e: Loki/Alloy, обычное задание и два повтора, общий контекст, переход в Tempo, 31 снимок ресурсов без перезапусков, финальные 23/23 UP и smoke. Оповещения и отказ backend/сборщика сюда не входят.

- [`tracing-infrastructure.md`](tracing-infrastructure.md) — `Passed` для диагностического OTLP span через Collector/Tempo/Grafana, 21 UP target и 31 снимка ресурсов без перезапусков. Прикладная трассировка проверена отдельным отчётом application-tracing; этот результат относится только к транспорту.

- [`dependency-metrics.md`](dependency-metrics.md) — `Passed` в ограниченном объёме: установка, сбор метрик, панель, цикл lag 0→1→0, SQL-права и PVC на 641639000c99; Kafka ACL отказы и неизменные offsets/end на 405c58554a8c. Образы/UID Pod зафиксированы, 21 снимок ресурсов без перезапусков; у Grafana малый запас памяти.

- [`prometheus-scraping.md`](prometheus-scraping.md) — `Passed` для инфраструктуры/API scrape, datasource Grafana, ограниченного повтора после OOM и цикла отказа/восстановления 1→0→1 без остановки HTTP API. Полный Milestone 8 этим не закрыт.
- [`application-metrics.md`](application-metrics.md) — `Passed` в ограниченном объёме: нагрузка, повторная доставка, отказ publisher и восстановление, HTTP p95, SQL integration, повторная загрузка панели и 15 минут без перезапусков Grafana. Память до 497Mi при лимите 512Mi — риск малого запаса; Kafka/PostgreSQL server exporter, tracing, Loki и доставка оповещений не входят в этот набор.

Раздел хранит воспроизводимые доказательства поведения стенда. Архитектурный контракт остаётся в `architecture/` или `design/`.

## Требования к сценарию

Каждый выполненный сценарий должен содержать цель, проверяемый контракт, ревизию и среду, исходные условия, способ создать нагрузку или отказ, ожидаемые и наблюдавшиеся признаки, диагностику, причину, восстановление, поведение Kubernetes, поведение приложения, границу доказанного свойства и остаточный риск.

## Качество доказательства

Один снимок Grafana без ревизии, конфигурации и процедуры воспроизведения недостаточен.

Предпочитать сохранённые команды, машиночитаемый вывод, события Kubernetes, метрики, результаты генератора нагрузки, журнал клиента и конфигурацию, на которой получен результат.

Не писать `Passed`, если сценарий не выполнялся.

## Milestone 1.1 и Milestone 2

- [`graceful-termination.md`](graceful-termination.md) — переход readiness и управляемое завершение после `SIGTERM`;
- [`pod-recovery.md`](pod-recovery.md) — замена удалённого Pod и восстановление ответа через Service;
- [`service-networking.md`](service-networking.md) — DNS и HTTP-запрос временного Pod через `ClusterIP Service`.
- [`helm-install-upgrade.md`](helm-install-upgrade.md) — чистая установка и обновление образа через Helm release.

Восстановление Pod, корректное завершение API и путь через Service имеют статус `Passed` после повторов 24 октября 2026 года на чистом образе commit `5d233c29e7ec`. Установка и обновление Helm получили `Passed` после чистого прогона 25 октября на commit `e036b30329b0`: новый временный кластер, неизменный Pod при повторе с тем же образом, новый готовый Pod и успешный smoke после второй сборки. Предварительный прогон 8 октября 2026 года на образе `f9e7b663af7a-dirty` подтверждал сетевой путь и восстановление Pod для прежних YAML-манифестов, но не заменяет новые результаты.

## Milestone 3

- [`rolling-update-under-load.md`](rolling-update-under-load.md) — измерение клиентских ответов через Service во время обновления двух реплик;
- [`inflight-request-shutdown.md`](inflight-request-shutdown.md) — принятый запрос, `SIGTERM` и отказ новой работе;
- [`failed-rollout-rollback.md`](failed-rollout-rollback.md) — неисправный локальный образ, диагностика и явный откат Helm.

Обновление под нагрузкой имеет статус `Passed` для одного чистого повтора с HTTP `preStop` без клиентских ошибок; отчёт сохраняет оба прежних неуспешных результата, включая два ответа `503` из 1799 запросов на сборке `b01c0ad77c85-dirty`. Завершение принятого запроса и неисправное обновление с откатом также имеют статус `Passed` после полных повторов 24 октября на чистом образе commit `5d233c29e7ec`; отсутствие клиентских ошибок при непрерывной нагрузке во время неисправного обновления не измерялось. Прогон Milestone 2 на образе `f9e7b663af7a-dirty` не является доказательством для нового кода и chart.

## Milestone 4

- [`cpu-throttling.md`](cpu-throttling.md) — `Passed` для сравнения двух CPU limits и счётчиков cgroup;
- [`oomkilled.md`](oomkilled.md) — `Passed` для изолированного Pod и восстановления доступности API;
- [`metrics-server.md`](metrics-server.md) — `Passed` для API метрик и показателей узла и Pod после повторного обновления release, не для чистой установки;
- [`hpa-autoscaling.md`](hpa-autoscaling.md) — `Passed` для одного чистого цикла 2 → 4 → 2 и клиентских ответов при уменьшении;
- [`scheduling-capacity.md`](scheduling-capacity.md) — `Passed` для `FailedScheduling: Insufficient cpu` и удаления временного Pod.

## Milestone 5

Перенос исполняемых проверок на Go описан в [`cross-platform-tooling.md`](cross-platform-tooling.md), статус `Passed` для выполнения в Windows: после commit подтверждены NetworkPolicy, Gateway, RBAC, Pod Security, scheduling, цикл HPA, обновление под нагрузкой, полный серверный журнал `inflight-work` и восстановление через rollback. Отдельно проверены создание комплекта Gateway-сертификатов Go-командой и отказ от повторной записи; действующий CA не изменялся. CLI собран для Linux и macOS, но на этих ОС не запускался. Статусы ниже включают сохранённые исторические прогоны.

- [`service-dns-diagnostics.md`](service-dns-diagnostics.md) — `Passed`: повтор на зафиксированном сценарии и чистом образе разделил ошибку разрешения имени, отказ до HTTP и ответ приложения; точный DNS RCODE не измерялся.
- [`network-policy-isolation.md`](network-policy-isolation.md) — `Passed`: после commit `df2bbdf` подтверждены переходы ingress/egress deny/allow и удаление экспериментальных ресурсов; запрос к отдельному API после очистки не выполнялся.
- [`gateway-routing-tls.md`](gateway-routing-tls.md) — `Passed`: образ commit `92cf68d`, сценарий commit `32c27a3`; подтверждены HTTP/HTTPS-ответы, отказ без лабораторного CA и отрицательные маршруты в локальном стенде; Go-пробы и оба пути нового `gateway-forward` повторены после commit `9236955`.
- [`rbac-access-control.md`](rbac-access-control.md) — `Passed`: после commit независимая проверка дала `yes/0` и три `no/1`, клиентский журнал — `200/403/403/403`; временный Pod удалён.
- [`pod-security-admission.md`](pod-security-admission.md) — `Passed`: после commit `fe61a235` подтверждены отказ PSA для `hostPID`, завершение ограниченного Pod, очистка и доступность API; первый отказ на ошибке допустимости сохранён в отчёте.

Базовая сеть сначала проверялась на образе `07b537514363-dirty`, а отрицательный DNS-запрос давал тайм-аут. Повтор на зафиксированном сценарии с образом `92cf68d43214` дал быстрый отказ разрешения имени; диагностические ресурсы удалены и доступность API перепроверена. Для `NetworkPolicy`, Gateway API, RBAC и Pod Security Admission также получены успешные прогоны с указанными в отчётах границами. В RBAC-опыте повторная проверка после commit использовала журнал ранее созданного Pod. Статусы этих сценариев нельзя выводить из успешной установки CNI или обычной проверки доступности.

## Milestone 6 — PostgreSQL и состояние

- [`postgresql-statefulset.md`](postgresql-statefulset.md) — `Passed`: запуск PostgreSQL, PVC, права на PGDATA и обновление с исправленными probes;
- [`persistent-storage-recovery.md`](persistent-storage-recovery.md) — `Passed`: запись после замены Pod при прежнем PVC на текущем узле;
- [`database-dependency-failure.md`](database-dependency-failure.md) — `Passed`: временная недоступность БД, восстановление без перезапуска API, изолированный отказ с неверным паролем и удаление экспериментального Pod;
- [`database-migrations.md`](database-migrations.md) — `Passed`: повторный Job, намеренная SQL-ошибка, откат контрольной таблицы и успешный следующий Job на чистом образе `803c1bc2363c`;
- [`database-backup-restore.md`](database-backup-restore.md) — `Passed`: архив на хосте, восстановление `before-backup` в отдельную базу при сохранении `after-backup` в основной, удаление тестовой базы и прежний PVC/PV.

Запуск PostgreSQL, сохранение записи после замены Pod и оба отказа зависимости подтверждены 24 октября 2026 года: API собран из чистой ревизии `df935dc5bf08`, исправление probes обновлено после commit `a57b85b`. Миграционный опыт на чистом образе `803c1bc2363c` подтвердил повторное применение и откат контрольной таблицы после SQL-ошибки. Backup/restore подтвердил восстановление контрольной записи из архива размером 2735 байт в отдельную базу того же экземпляра. Все пять профильных отчётов имеют `Passed` с указанными границами. Финальная регрессия базового API на образе `803c1bc2363c` при остановленной БД также прошла; PostgreSQL затем восстановлен до одной готовой реплики. Go-проверки после изменения отрицательной миграции успешно выполнены владельцем 25 октября 2026 года. Дополнительный опыт 25 октября подтвердил отказ при отсутствующем Secret, `schema_missing` при отсутствии таблицы, восстановление готовности и очистку тестовой базы при сохранении основных данных. Этап закрыт в границах пяти профильных отчётов.

## Milestone 7 — Kafka и worker

[`kafka-broker-outage.md`](kafka-broker-outage.md) — `Passed`: фактическая остановка единственного broker/controller при временно остановленном Cluster Operator, приём в outbox, чтение прежнего marker и восстановление той же заявки с одним эффектом. PVC/PV/node ID сохранены, offsets 8/2/2, lag=0, smoke и очистка успешны. Задержка Operator и прерванный restore с успешным повтором сохранены в отчёте; причина задержки не установлена.

[`cronjob.md`](cronjob.md) — `Passed` на f5bf6d89a472: два автоматических запуска с разными назначенными моментами, два ручных повтора первого без второго задания/outbox/эффекта, RBAC yes/no/no. Итоговые offsets 9/3/2, lag=0, pending=0; финальные доступность, smoke и очистка подтверждены, CronJob оставлен suspended. Срабатывание Forbid/deadline и причина исчезновения первого Job не установлены этим опытом.

[`kafka-retry-dlq.md`](kafka-retry-dlq.md) — `Passed` на worker `de6f15ba5c4d`: успех после двух ошибок, DLQ после трёх, запрет source commit при недоступном DLQ producer и восстановление, пять видов некорректных событий без задания. Go/SQL/Helm-проверки успешны; финальная обычная заявка, smoke, доступность зависимостей и удаление 30 диагностических Pod подтверждены. Итоговые source offsets 7/2/2, lag=0. Это не опыт отказа самого брокера или exactly-once DLQ.

[`kafka-redelivery.md`](kafka-redelivery.md) — `Passed` для локального опыта на `011b2e8c144c`: SQL-эффект → exit 86 без offset commit → та же запись после восстановления → дубликат без второго эффекта → commit 1 для partition 2. SQL-тест с race, финальная доступность API/publisher/PostgreSQL, smoke, отсутствие тестовой БД и очистка диагностических Pod подтверждены. Первый отказ диагностики при PreparingRebalance сохранён. Инструкция — [аварийная повторная доставка](../engineering/kafka-redelivery-lab.md).

[`kafka-worker.md`](kafka-worker.md) — `Passed` для третьей итерации на чистом образе `858f2ee181e4`: SQL-эффект до ручного offset commit, повторная публикация без второго эффекта, две реплики с распределением 2+1 и batch из 12 заявок, SIGTERM во время восьмисекундной работы, восстановление и очистка. Всего 19 наблюдавшихся заданий с одним эффектом, итоговые offsets 8/5/7 и lag 0. Незасчитанные попытки сохранены; crash-redelivery, retry/DLQ и полный Milestone 7 этим не закрыты.

[`kafka-outbox.md`](kafka-outbox.md) — `Passed` для второй итерации: конкурентный HTTP enqueue, миграция `002`, публикация и восстановление прежнего события после недоступного адреса Kafka; исправленный сценарий и SQL integration-тест прошли на чистой ревизии `e84d7a25fe15`, API/publisher сохранили build revision `4345b1078c40`. Тестовая БД удалена, smoke прошёл. Исходный конфликт Helm и ручной обход сохранены в истории. Этот отчёт не проверяет worker и настоящий broker outage; worker закрыт отдельным отчётом выше.

[`kafka-bootstrap.md`](kafka-bootstrap.md) — `Passed`: установлены Strimzi/KRaft и topics/users; на чистой ревизии `c5c52ee3118e` подтверждены mTLS roundtrip, новый broker Pod UID при прежних node ID/PVC/PV и чтение исходной записи на том же offset. API smoke прошёл, PostgreSQL остался готовым; SQL-интеграция Kafka с БД ещё не проверялась. Диагностические Pod сохранены, первый неуспешный smoke не удалён из истории. Этот отчёт подтверждает только инфраструктурную итерацию; последующие outbox/worker и завершение базового объёма отражены в профильных отчётах выше.


[`keda.md`](keda.md) — `Passed`: KEDA 2.21.0, числовая метрика lag, масштабирование worker 1→2→3→2→1, все 30 заданий succeeded с одним outbox/эффектом, снимки соединений PostgreSQL во время нагрузки, offsets 23/15/10 и lag=0. Возврат fixed Helm, readiness/smoke и cleanup подтверждены; KEDA остаётся установленным, scaler/HPA приложения удалены. Расширения Forbid/Job deadline/отказа зависимости с восстановлением также Passed внутри отчёта CronJob; обработка SIGTERM до SQL отдельно не доказана.

## Целевой каталог

По мере развития проекта должны появиться сценарии корректного завершения `Pod`, ошибки `readinessProbe`, вредной конфигурации `livenessProbe`, `OOMKilled`, ограничение CPU по квоте (`CPU throttling`), HPA, неудачного обновления и отката, недоступности зависимости, прерывания `worker`, `drain` узла, внезапной потери узла, запрета `NetworkPolicy`, отказа `RBAC` и восстановления данных после пересоздания `Pod`.

- [Прикладная трассировка через outbox/Kafka](application-tracing.md) — Passed для обычного задания, просмотра Grafana и восстановления контекста после замены publisher Pod; два отказа worker с последующим успехом также подтверждены; исчерпание попыток/DLQ и просмотр трассировки также подтверждены в указанном объёме; полный ответ Tempo для DLQ архивирован, связи и порядок последних spans сверены. Одно задание при отказе telemetry endpoint завершилось успешно, доставка новой трассировки после восстановления подтверждена; SDK disabled подтверждён отдельным заданием с пустым trace_id и одним эффектом; 31 снимок шести workloads за 15 минут подтвердил отсутствие перезапусков в лёгком режиме. Текущее здоровье после разрывов dashboard подтверждено: 21/21 UP, smoke успешен. Исторические разрывы, сравнение накладных расходов и предельная нагрузка не исследованы.
