> Historical result from the private development history. Original identifiers are preserved; this is not a rerun of the public snapshot. Reviewed screenshots are included; raw captures remain private. See [provenance](../provenance.md). Local paths, where present, were redacted.

# Проверки и лабораторные сценарии

Milestone 8 завершён в границах локального стенда; [итоговая приёмка и ограничения](grafana-dashboards.md#итоговая-приёмка-milestone-8), архив заключительных проверок (private original artifact omitted; see provenance).

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
