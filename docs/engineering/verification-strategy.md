# Стратегия проверки

Первоначальная инфраструктурная итерация Milestone 8 была Prepared до runtime-проверок; [отчёт](../verification/prometheus-scraping.md) сохраняет исходные отказы и последующие успешные scrape/recovery-проверки. Затем отдельно проверены прикладные метрики, зависимости, трассы, журналы, панели и оповещения. [Финальная приёмка](../verification/grafana-dashboards.md#итоговая-приёмка-milestone-8) закрывает локальный объём этапа; отсутствие Collector/exporter не означает отсутствие обработки задания. Статические тесты не заменяют runtime-доказательства.

**Состояние: Current.**

Проект разделяет четыре уровня доказательства.

## 1. Статическая корректность

Проверяется синтаксис, схема API, формирование Helm-манифестов и политика безопасности, если соответствующие инструменты подключены.

Успешный `helm template` доказывает только возможность сформировать манифесты.

## 2. Установка

Временный кластер `kind` подтверждает, что ресурсы принимаются сервер API Kubernetes и приходят в ожидаемое состояние.

## 3. Минимальная проверка доступности

После установки клиент выполняет короткий запрос через ту границу, которую требуется проверить: `Service`, `kubectl port-forward` или позднее Gateway.

## 4. Сценарная проверка

Проверяется конкретная эксплуатационная гипотеза: корректное завершение, обновление без неожиданных ошибок, `OOMKilled`, ограничение CPU по квоте (`CPU throttling`), HPA, откат, действие `NetworkPolicy`, ограничение `RBAC` или восстановление после отказа зависимости.

## Правило достаточности

Каждое утверждение подтверждается на соответствующей границе.

```text
манифест сформирован
≠
Pod готов

Pod готов
≠
Service направляет трафик

rollout завершён
≠
клиент не видел ошибок

HPA увеличил replicas
≠
система выдержала дополнительную нагрузку
```

## Регрессия

Если лабораторный сценарий обнаружил дефект конфигурации или приложения, исправление должно получить минимальную воспроизводимую проверку, различающую старое и новое поведение.

## Исполняемые проверки Helm

Единый интерфейс находится в `Taskfile.yml`. `task test` проверяет Go-код. `task helm-lint` и `task helm-render` проверяют chart без установки. `task deploy` выполняет серверную проверку `Namespace`, затем устанавливает или обновляет Helm release и ждёт готовности. `task smoke` проверяет запрос временного Pod через DNS и `ClusterIP Service`, а `task verify-recovery` проверяет замену удалённого Pod и повторяет внутрикластерную минимальную проверку.

Для команд с циклами, условиями и разбором Kubernetes JSON Taskfile вызывает `cmd/labctl/`, а не PowerShell. Пять прежних сценариев и проверки Milestone 5 переведены на Go с явным выбором контекста; прежние `.ps1` удалены. Статические проверки и сборка для Windows, Linux и macOS не доказывают работу CLI на всех этих системах. После профильных прогонов в Windows [перенос публичных задач](../verification/cross-platform-tooling.md) имеет статус `Passed` в пределах проверенной среды; запуск на Linux и macOS остаётся непроверенным. Исторические результаты других отчётов не меняются.

Для установки дополнительно проверяются состояние release, принадлежность `Deployment` и `Service` Helm, готовность Pod и соответствие `/version` собранному образу. Для обновления сравниваются две ссылки на образ, номера ревизий release, история Helm и образ нового Pod. Успех `helm upgrade` без этих наблюдений не считается доказательством обновления.

`task smoke-api-proxy` использует proxy API server только для диагностики. Его успешный результат не доказывает работу Kubernetes DNS или обычного сетевого пути клиента через `ClusterIP`.

Для отдельного сетевого кластера `task network-cni-status` проверяет установку Cilium, но не действие `NetworkPolicy`. `task network-smoke` проверяет базовый путь после установки CNI, а `task network-diagnostics-start` и `task network-diagnostics-status` сохраняют различимые ошибки DNS, соединения и HTTP. Реальные ответы клиента сопоставляются с `Service.spec.selector`, портами, готовностью Pod и условиями `EndpointSlice`; повтор на зафиксированной конфигурации описан в [`отчёте`](../verification/service-dns-diagnostics.md). `curl_exit=6` устанавливает отказ разрешения имени, но не точный DNS RCODE. Только отдельная проверка deny/allow подтверждает применение `NetworkPolicy`.

`task network-policy-test` поэтапно проверяет исходную доступность, ingress deny/allow, egress deny и отдельные разрешения DNS/HTTP в ограниченном namespace. Каждый отказ сопоставляется с готовностью API и конечной точки, а не с одним фактом `kubectl apply`. После commit исправленного сценария новый прогон подтвердил все переходы и удаление экспериментальных ресурсов; запрос к отдельному API после очистки не выполнялся: [`../verification/network-policy-isolation.md`](../verification/network-policy-isolation.md).

Подготовленный Gateway-сценарий разделяет принятие `GatewayClass`, `Gateway` и `HTTPRoute`, фактический HTTP-ответ через data-plane Service и TLS-проверку с явно доверенным лабораторным CA. Отрицательные `HTTPRoute` отделены от маршрута приложения и удаляются после сохранения условий `Accepted`/`ResolvedRefs` и ответов клиента. Успешный `helm upgrade`, наличие CRD или статический `helm template` не подтверждают маршрутизацию; подробные признаки и границы — в [`../verification/gateway-routing-tls.md`](../verification/gateway-routing-tls.md).

Для RBAC `task rbac-test` сопоставляет `kubectl auth can-i` с реальными HTTPS-ответами временного Pod под отдельным ServiceAccount. Только точное сочетание разрешённого `get` собственного Pod и трёх `403` на лишние действия подтверждает проверяемую часть least privilege. `401`, `404` и сетевые ошибки разбираются отдельно; токен не печатается. После фиксации сценария повторная проверка дала ожидаемые ответы авторизации и прочитала журнал ранее созданного Pod; временный Pod удалён. Статус и границы — в [`../verification/rbac-access-control.md`](../verification/rbac-access-control.md).

Для Pod Security Admission `task pod-security-test` сначала сверяет метку `enforce=restricted` и настройки API, затем требует отказа именно из-за `hostPID` при серверном dry-run. Недопустимый Pod не создаётся. Отдельный допустимый Pod должен пройти admission и завершиться; неожиданный отказ или сбой его процесса разбираются отдельно. Первый опыт остановился на ошибке допустимости прежнего отрицательного манифеста до admission. Повтор после commit `fe61a235` подтвердил отказ PSA, завершение допустимого Pod и доступность API после очистки; границы результата и статус [`Passed`](../verification/pod-security-admission.md) приведены в отчёте.

Для этапа 3 `task helm-render-rollout` показывает профиль двух реплик. `task rollout-client-build` готовит отдельный ограниченный клиентский образ, а `task rollout-load-start` создаёт клиентский Pod. Журнал `task rollout-load` содержит индивидуальный `run_id`, время начала и окончания, числа успешных запросов, HTTP-ошибок, ошибок соединения и превышений срока. Нулевое число ошибок одного опыта не означает гарантии для всех нагрузок. `task rollout-status` связывает этот журнал с состоянием release, Pod и конечных точек.

`task inflight-work` ожидает серверный `work_started` с тем же `run_id` до удаления выбранного Pod. Отдельный наблюдающий Pod проверяет переход `/readyz` и ответ новой работы непосредственно у завершающегося процесса. При намеренно неисправном обновлении ошибка `task rollout-fail` ожидаема: сначала сохраняют состояние Helm и Kubernetes, затем выполняют явный `task rollout-rollback ROLLBACK_REVISION=N` и проверяют готовность и ответ через `Service`.

Для CPU-опыта используются два профиля с одинаковым request и различным CPU limit. `task resources-cpu-cgroup` возвращает тип cgroup, квоту и накопительные счётчики текущего контейнера; снимки до и после `task resources-cpu-start` сравниваются только в пределах одного Pod. `task resources-cpu-status` сохраняет время и статус каждого ответа клиента. Рост `nr_throttled` и `throttled_usec`, а не приближение показания `kubectl top` к limit, является прямым свидетельством ограничения квоты. Один чистый локальный опыт подтвердил это различие; численные результаты принадлежат [`../verification/cpu-throttling.md`](../verification/cpu-throttling.md).

Для памяти используется одноразовый Pod, изолированный от Helm release API. Условие `Failed` служит только поводом для диагностики; доказательство `OOMKilled` требует поля `reason` завершённого контейнера вместе с установленным memory limit. `exitCode: 137` может дополнять картину, но не заменяет причину. `Evicted` из-за давления на узел является другим исходом. Один локальный опыт подтвердил `OOMKilled` отдельного контейнера и доступность основного API после опыта; границы доказательства — в [`../verification/oomkilled.md`](../verification/oomkilled.md).

Для Metrics Server три сигнала проверяются раздельно: готовность его `Deployment`, доступность агрегированного API `metrics.k8s.io` и реальные образцы CPU/памяти узла и прикладных Pod через `kubectl top`. `task metrics-server-render` проверяет только формирование ресурсов; `task metrics-server-install` устанавливает отдельный компонент в `kube-system`; `task metrics-server-status` сохраняет состояние и диагностику; `task metrics-server-verify` требует ответов Metrics API и показателей. Чистый повтор подтвердил все три сигнала в выделенном `kind`, но не чистую установку с нуля. Наличие метрик не доказывает работу HPA и не заменяет точные счётчики cgroup из CPU-опыта; подробности — в [`../verification/metrics-server.md`](../verification/metrics-server.md).

Для HPA `task helm-render-hpa` должен показать отсутствие `Deployment.spec.replicas` и отдельный объект `autoscaling/v2 HorizontalPodAutoscaler`. `task deploy-hpa` проверяет доступность Metrics API, развёртывает профиль и ждёт две Ready-реплики; это не доказывает реакцию на нагрузку. Ограниченный CPU-клиент запускается через `task hpa-load-start`, его результаты сохраняет `task hpa-load-status`, а `task hpa-observe` записывает временной ряд метрики, желаемого и готового числа реплик. После окончания CPU-нагрузки отдельный клиент через `Service` наблюдает период уменьшения числа реплик. Число Ready Pod и ответы клиента оцениваются отдельно от `desiredReplicas`; отчёт [`../verification/hpa-autoscaling.md`](../verification/hpa-autoscaling.md) имеет статус `Passed` для сохранённого чистого прогона.

Для ограничения размещения `task scheduling-validate` проверяет только допустимость объекта на API server; `task scheduling-start` создаёт отдельный Pod с CPU request выше `allocatable` единственного узла. Доказательство требует `PodScheduled=False`, события `FailedScheduling` с причиной нехватки CPU и сохранённого состояния узла. `Pending` само по себе не устанавливает причину. После сбора данных удалить только экспериментальный Pod; подробности находятся в [`../verification/scheduling-capacity.md`](../verification/scheduling-capacity.md).

Поведение завершения требует отдельного наблюдения readiness и журналов процесса. Выполненные профильные отчёты:

- [`../verification/graceful-termination.md`](../verification/graceful-termination.md);
- [`../verification/pod-recovery.md`](../verification/pod-recovery.md);
- [`../verification/service-networking.md`](../verification/service-networking.md).
- [`../verification/helm-install-upgrade.md`](../verification/helm-install-upgrade.md).

Статус `Passed` присваивается только после выполнения сценария на указанной чистой ревизии конфигурации; для сценариев приложения дополнительно нужен образ, собранный из неё. Успешные модульные тесты или статическая проверка chart не заменяют эти сценарии.

## Milestone 7 — Kafka

Первый шаг — `task kafka-preflight`, только сбор состояния основного кластера. Вывод не подтверждает установку Kafka или достаточность ресурсов автоматически. Владелец предоставил снимок, по которому выбран бюджет одной broker/controller-ноды без изменения действующих компонентов. Установка, server-side dry-run CR, mTLS smoke и чтение прежней записи после штатной замены Pod проверены: [bootstrap-отчёт](../verification/kafka-bootstrap.md) имеет `Passed` на чистой ревизии `c5c52ee3118e`. Прежний skew устранён клиентом kubectl `1.35.9`; первый отказ конфигурации smoke сохранён в отчёте.

Следующие итерации проверяются до перехода к очередной: broker/topics и реальный produce/consume; outbox при недоступном broker; consumer groups и offsets; retries/DLQ и идемпотентность; crash до/после PostgreSQL commit; broker recovery; CronJob. Integration-тесты должны использовать настоящие Kafka и PostgreSQL с явным opt-in, а не зависеть от них в обычном unit-прогоне. Моки не доказывают rebalance и сохранность offsets. Базовый stateless API проверяется отдельно без обеих зависимостей. Наблюдаемость подключается отдельным профилем Milestone 8.

## Milestone 8 — наблюдаемость

Инфраструктура и [метрики приложения](../verification/application-metrics.md) имеют Passed в границах выполненных опытов: реальные targets/samples, SQL freshness, counters вокруг новых заданий и replay, отказ publisher с восстановлением, повторная загрузка панели после замены Grafana Pod. Последующие [метрики зависимостей](../verification/dependency-metrics.md), [tracing](../verification/application-tracing.md), [logging](../verification/application-logging.md) и [alerts](../verification/alerting.md) подтверждены отдельными runtime-отчётами. [Итоговая приёмка Milestone 8](../verification/grafana-dashboards.md#итоговая-приёмка-milestone-8) сохраняет ограничения локального стенда, исторические рестарты и неполноту наблюдения между снимками. При повторе нужны собственные образы, SHA, SQL/offset и telemetry-доказательства; модульные тесты не заменяют этот прогон.

Вторая итерация [проверена владельцем](../verification/kafka-outbox.md), статус `Passed`: HTTP enqueue, PostgreSQL job/outbox, broker ack, автоматическое восстановление publisher и SQL integration-тест. Сценарий и SQL-тест — `e84d7a25fe15`, API/publisher — `4345b1078c40`. Ошибка bootstrap доказывает недоступность Kafka для конкретного publisher, не отказ broker. Третья итерация [Passed](../verification/kafka-worker.md) на чистом образе `858f2ee181e4`: результат SQL до offset commit, дубликат, две реплики, SIGTERM во время работы, восстановление и очистка подтверждены владельцем. Инструкция [прогона](kafka-worker-lab.md) сохраняется для воспроизведения; аварийная повторная доставка отдельно имеет [Passed](../verification/kafka-redelivery.md). Конечные повторы/DLQ, управляемый отказ брокера и CronJob имеют отдельные [отчёты](../verification/README.md) с границами доказанного поведения. Opt-in SQL-тест требует отдельной БД `lab_jobs_test_*`; пропуск не означает успех.
