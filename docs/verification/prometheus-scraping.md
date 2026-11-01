> Historical result from the private development history. Original identifiers are preserved; this is not a rerun of the public snapshot. Reviewed screenshots are included; raw captures remain private. See [provenance](../provenance.md). Local paths, where present, were redacted.

# Prometheus: установка, обнаружение и samples

- Статус: `Passed` в границах инфраструктурной итерации Milestone 8: установка, сбор API, datasource Grafana, отказ scrape и восстановление. Не весь Milestone 8.
- Ревизия канонического опыта отказа/восстановления: `86e3b88b2048dd7605333de23cb59145c130d15d`, дерево чистое по выводу владельца; ревизия образа API — `d47851ccb0b7`.
- Base SHA / HEAD начала подготовки: `fda9b9238f78ea3d91fb393dbad459a7bac492af`, ветка dev, дерево исходно чистое.
- Контракт: [наблюдаемость](../design/observability-architecture.md).
- Процедура: [инструкция](../engineering/observability-lab.md).

## Цель и исходные условия

Проверить отдельный stack, CRD, discovery API, scrape/PromQL, правило и готовность Grafana/Alertmanager. Приложение, PVC, CronJob и управление worker должны сохраниться.

Read-only наблюдения 28–29 октября 2026 года выполнены агентом напрямую через kubectl/Docker до появления новых CLI-команд:

- основной kind, узел Ready, Kubernetes 1.34.0, containerd 2.1.3, Docker Server 25.0.3;
- allocatable 7794856Ki; requests 2350m/2978Mi, limits 6350m/6022Mi; node events отсутствуют;
- API/PostgreSQL/publisher/worker Ready без перезапусков; worker одна реплика, CronJob suspended/active=0; KEDA установлен;
- Kafka broker Ready; Strimzi Operator Ready, перезапусков 7, lastState Error/exit 1. Хвост предыдущего журнала содержит shutdown, но не устанавливает причину. Нужен актуальный снимок перед установкой;
- monitoring.coreos.com CRD отсутствуют; StorageClass standard присутствует; top nodes вернул Metrics API not available;
- Docker stats примерно 7.73MiB на весь kind, kubelet около 8MiB на узел и 0.1MiB на большинство Pod. Эти величины не приняты за достоверное потребление приложений;
- /proc/meminfo: MemTotal 7794856 kB, MemAvailable 3891076 kB. Это общая Linux-среда, не каждый Pod; требуется повтор;
- Helm в PATH процесса агента отсутствует. Новые Go/Helm-проверки не получены. Прежние releases видны по метаданным Secret; содержимое Secret не читалось.

Это не доказательство установки monitoring или устойчивости под нагрузкой.

## Проверка и ожидаемые признаки

Выполнить профильные Go/Helm-проверки и команды инструкции. Сохранить каталоги `.local-observability/<UTC>-<action>/`, SHA/dirty, версии, Helm revisions, Pod images/imageIDs, PVC/PV и StorageClass reclaim policy.

Ожидается: Operator/Prometheus/Grafana/Alertmanager Ready, CRD Established, API target UP, непустой lab_build_info, вычисленное lab:api_targets_up:sum, Ready worker из kube-state-metrics, Grafana database ok. Затем перепроверить основной API, Kafka/SQL и отсутствие новых отказов.

## Результаты, восстановление и границы

29 октября владелец предоставил успешные результаты `go test ./cmd/labctl` (2.274s), `go test -race ./cmd/labctl` (2.449s), `go vet ./cmd/labctl`, `task observability-helm-lint` и `task observability-render`. Это предварительный прогон на изменённом дереве от указанного Base SHA, не проверка установленной платформы.

Preflight `20261029T070824.982951800Z-preflight` остановился на Helm 4.3.1: `helm list --all` вернул `Error: unknown flag: --all`. Причина — несовместимый аргумент CLI, а не отказ Kubernetes. Сохранён исходный `helm-releases.json`. По [документации Helm 4](https://helm.sh/docs/helm/helm_list/) флаг удалён; оба вызова inventory/ownership заменены явным набором status filters, поддерживаемым Helm 3/4. Тесты ownership обновлены; успешный повтор описан ниже.

Render сохранён в `20261029T070839.586781000Z-render`. При просмотре подтверждены закреплённые image tags Operator/Prometheus/Alertmanager/Grafana/kube-state-metrics/sidecar/certgen, requests/limits, restricted securityContext Deployment kube-state-metrics и selectors/PVC Prometheus. Это проверка конфигурации, не server-side admission или работающий scrape.

Повтор 29 октября на чистом `ffe1c41dcb701cb75e92e0809856ece9921b79ac` подтвердил `go test ./cmd/labctl` (1.552s), `go test -race ./cmd/labctl` (2.477s), `go vet ./cmd/labctl`. Первый preflight в новом PowerShell был отклонён проверкой skew: kubectl 1.29 против server 1.34. После добавления существующего kubectl 1.35.9 в PATH текущего процесса Go-проверки повторены успешно (test/race из cache), полный `task observability-preflight` завершился.

Снимок `.local-observability/20261029T071233.984652100Z-preflight`: Helm 4.3.1 перечислил все семь прежних releases, мониторинг ещё не установлен; Docker 12 CPU/7981932544 bytes; Linux MemAvailable 3869336 kB. Узел Ready, MemoryPressure/DiskPressure/PIDPressure=False. Прикладные API/PostgreSQL/publisher/worker и Kafka broker работают без перезапусков. Strimzi Operator имеет уже 9 перезапусков: рост относительно предыдущего снимка сохраняется как отдельное наблюдение с неустановленной причиной. Недостоверно малые Docker/kubelet memory readings и отсутствие Metrics API также сохраняются; за нулевую загрузку они не принимаются. Preflight не изменял ресурсы.

На момент завершения preflight установка ещё не выполнялась: targets/samples, Grafana API, monitoring revisions/imageIDs отсутствовали. Первый установочный прогон и его неисправности описаны ниже.

## Первая установка 29 октября: неисправности и подготовленное исправление

На ревизии команд `ba79ebf` владелец выполнил установку. Артефакты: `20261029T071510.471772100Z-install`, `20261029T071834.526988500Z-status`, затем targets/query/grafana-status в 07:18:39–07:18:44 UTC. Оба releases получили revision 1/deployed, но итоговая команда завершилась ошибкой готовности Alertmanager. Это не успешная проверка всей платформы.

- Alertmanager CR Reconciled=False: `undefined receiver "null" used in route`; StatefulSet не создан, Service не имеет endpoints. Причина: Helm сохранил default дочерний маршрут Watchdog к receiver null, тогда как наш список receivers содержал только local-null. Исправление явно заменяет дочерние routes пустым списком.
- Grafana Pod `lab-monitoring-grafana-84c849b55c-z9ghv`: основной контейнер Ready без перезапусков; оба sidecar получили OOMKilled при 64Mi и по одному перезапуску в снимке. Из-за неготовности всего Pod Service proxy вернул отсутствие endpoints. Подготовлено штатное provisioning Prometheus/Alertmanager через ConfigMap без этих двух процессов; RBAC/token Grafana для Kubernetes больше не нужны.
- Prometheus, Operator и kube-state-metrics готовы. Первые targets включали 11 UP endpoints, но ещё не API, а запросы build/recording-rule вернули пустые результаты. Повторное read-only чтение агентом позже обнаружило 12 UP endpoints, включая lab-api: отсутствие API было переходной задержкой discovery, селекторы не менялись. Это не устраняет остальные неисправности.
- Prometheus PVC Bound, PV `pvc-5235b20e-1622-4203-9bd8-09b87cf37fc0`, 2Gi. Он сохраняется при повторном Helm upgrade; Grafana Secret также не заменяется.

Избыточный вывод полного Helm status JSON сокращён в CLI до имени/revision/status, полные данные остаются в артефактах. Новые Go-проверки, render исправленного профиля и повтор установки ещё не выполнены. Последующие результаты сохраняются рядом, исходный неуспешный прогон не удаляется. Статус остаётся Prepared.

Повтор 07:24 UTC: Go test/race/vet и render исправленного профиля прошли по выводу владельца (test 1.542s, race 2.485s). Установка `20261029T072420.373668100Z-install` была остановлена до изменения ресурсов проверкой headroom: requests 3250Mi, limits 6582Mi, allocatable 7612Mi. Причина — проверка исключала только instance=lab-monitoring, но Pod, созданный Operator, имеет instance=lab-monitoring-prometheus. Его 272Mi requests/560Mi limits ошибочно прибавились к полному новому бюджету повторно. Правильная исходная сумма limits без заменяемой платформы — 6022Mi; вместе с новым бюджетом 1280Mi и резервом 256Mi — 7558Mi. Подготовлено распознавание точных instance и управляющих StatefulSet Prometheus/Alertmanager; чужие Pod продолжают учитываться. Регрессионный тест проверяет собственные и чужие namespace/instance/owner. Повтор этих проверок после исправления ещё не выполнен.

Несмотря на остановку upgrade, сбор API уже подтверждён владельцем в 07:24:31–07:24:35 UTC: 12 UP targets, lab_build_info=1 для API revision 011b2e8c144c и lab:api_targets_up:sum=1. Grafana/Alertmanager остались на прежней конфигурации revision 1, поскольку обновление не началось. Их ошибки не считаются новым отказом исправленного профиля.

## Восстановленная машина: сбор метрик и OOM основного контейнера Grafana

Это отдельная машина, не продолжение прежней установки на узле с allocatable 7612Mi. Владелец восстановил основной стенд и собрал образы чистой ревизии `d47851ccb0b77f5a290e2935db2e608b897ffc33`. Docker сообщил 32 CPU и около 30GiB памяти. На этой машине установлен новый monitoring release; Prometheus PV — `pvc-2d21c301-725d-434e-b87d-a588b1c461ab`, 2Gi, а не PV предыдущей среды.

Подтверждено выводом владельца 29 октября 2026 года:

- после ожидания StatefulSet Prometheus оба releases имеют revision 1/deployed, Prometheus готов, Alertmanager отвечает OK;
- targets `20261029T081442.024764900Z-targets`: 14 активных endpoints UP, last_error пуст;
- запросы 08:14:42–08:14:44 UTC: `lab_build_info=1`, revision `d47851ccb0b7`; `lab:api_targets_up:sum=1`; доступная реплика worker равна 1 по kube-state-metrics, что не доказывает прикладные метрики обработки;
- Grafana API `20261029T081907.631788500Z-grafana-status`: database ok, версия 13.2.4; последующий smoke API завершился успешно и удалил проверочный Pod;
- владелец открыл UI Prometheus, Grafana и Alertmanager через loopback. Журнал Grafana перед отказом содержит запрос пользователя lab-admin, но работа с панелями и источником данных ещё не подтверждена.

Первый вызов install на этой машине вернул ошибку готовности Prometheus при уже развёрнутых releases. После ожидания созданного Operator StatefulSet status и запросы прошли; немедленный status после Helm wait пока не гарантирует завершения запуска ресурсов Operator.

При использовании UI Grafana контейнер `grafana` Pod `lab-monitoring-grafana-6f7cfcb986-2jgzc` завершился 08:26:23 UTC: `lastState.terminated.reason=OOMKilled`, exitCode=137, request=128Mi, limit=256Mi. Проброс `20261029T082007.453822300Z-forward-grafana` в 08:26:24 UTC получил connection reset и lost connection to pod. Это последствие завершения процесса, не истечение ограничения времени проброса. Kubernetes перезапустил контейнер в том же Pod; restartCount=1, Ready=true. Повторный API health в 08:28:19 UTC сообщил database ok. Снимок top после перезапуска: 8m CPU, 223Mi памяти; это не измерение пика перед OOM. Остальные компоненты мониторинга в предоставленном снимке не перезапускались.

Журнал предыдущего процесса показывает автоматическую установку дополнительных плагинов, но не устанавливает виновный компонент или утечку памяти. По согласованию с владельцем request увеличен до 256Mi, limit — до 512Mi вместе с бюджетом preflight. Конфигурация плагинов не изменена. Результат повтора и последующий успешный опыт отказа сбора приведены ниже; исходный OOM не удаляется из доказательств.

Повтор: профильные Go/Helm-проверки, preflight/render, commit владельца, затем установка из чистого состояния. После замены Grafana Pod сохранить его UID, фактические requests/limits, restartCount и новый снимок ресурсов. Повторить status/targets/build/recording-rule, smoke и вход в UI; в Grafana Explore выполнить `lab_build_info` через Prometheus. Наблюдать память и число перезапусков не менее 15 минут, включая активную работу UI: предыдущий процесс прожил около 15 минут. Отсутствие новых OOM в этом окне доказывает только устойчивость при данной нагрузке, а не отсутствие утечки или достаточность бюджета для будущих панелей/плагинов. Состояние до повторной установки и исходный OOM сохраняются в доказательствах. Замена Pod может удалить ручные настройки Grafana из временного хранилища; Secret не заменяется, PVC Prometheus удалять нельзя.

### Повторная проверка памяти Grafana — Passed в границах наблюдаемого окна

Владелец предоставил журнал от 29 октября и снимок Grafana Explore. Deployment имеет request=256Mi, limit=512Mi. Новый Pod `lab-monitoring-grafana-544685bcf7-z5568` сохраняет `1/1 Running`, restartCount=0 во всех 31 снимке с 08:58:08.1918497 до 09:13:13.6454513 UTC: 15 минут 5 секунд. Возраст Pod к концу — около 20 минут. Выборка top составляет 229–378Mi, первый снимок 369Mi, последний 283Mi. Это дискретные измерения, не полный максимум cgroup. Число перезапусков остальных компонентов мониторинга также равно нулю.

На снимке Explore через datasource Prometheus получен ряд `lab_build_info` со значением 1. Подтверждён путь Grafana → Prometheus → метрики API; это не проверка будущих панелей или всех datasource. Итоговый `20261029T091357.180387000Z-grafana-status` сообщает database ok, Grafana 13.2.4. Предыдущий вызов status был отклонён до проверки API из-за client kubectl 1.36/server 1.34; после выбора kubectl 1.35.9 повтор прошёл. Эта ошибка PATH не является отказом Grafana.

Повтор подтверждает отсутствие новых перезапусков при предоставленной нагрузке UI в указанном окне с лимитом 512Mi. Он не доказывает отсутствие утечки или достаточность бюджета при будущей нагрузке. SHA команд повторного прогона и Helm revision не входят в присланный журнал; текущий HEAD репозитория при подготовке следующего сценария `20cc8297a26498d2a42cbfdbe091daccfc7f7861` не подставляется вместо отсутствующей provenance прогона. Исходный OOM выше сохраняется.

## Отказ сбора метрик и восстановление — Passed

Цель: отличить отказ клиента сбора Prometheus от отказа приложения. Исходные условия: основной стенд работает, API target UP, `lab_build_info=1`, recording rule=1, smoke успешен. Перед опытом сохранить новый status (включая Helm revisions), git SHA/dirty и фактический ServiceMonitor.

Команда `observability-scrape-fail` меняет только существующий release lab-observability через Helm: флаг `apiScrapeFailure=true` выбирает HTTPS вместо HTTP в ServiceMonitor API. Service, Deployment, probes, PVC, основной monitoring release и Secrets не изменяются. Известные версии локального chart — 0.1.0/0.2.0; отсутствующий, чужой или pending release отклоняется. До upgrade выполняются render и server dry-run; команды/ответы сохраняются в отдельном каталоге `.local-observability`. Успех upgrade не доказывает применение нового scrape-протокола Prometheus.

Ожидаемые признаки после reconciliation и scrape: API target остаётся обнаруженным, scrape_url использует https, health=down, last_error описывает несовместимость TLS/HTTP, точный `api-up` равен 0. Остальные targets остаются UP. `prometheus-targets` намеренно возвращает ненулевой код при DOWN. Исчезновение API target или отказ всех targets не засчитывается вместо этого признака. Smoke API должен пройти во время отказа сбора. Старые samples build могут оставаться видимыми в историческом диапазоне и не доказывают свежий сбор.

Диагностика сопоставляет UTC-время upgrade, ServiceMonitor после него, targets/last_error и результаты PromQL. Operator обновляет конфигурацию Prometheus; Kubernetes не должен перезапускать API ради исправления наблюдаемости. API продолжает HTTP-обслуживание; ответ на TLS-попытку не означает отказ обычного HTTP-трафика.

Восстановление: `observability-scrape-restore` выставляет флаг false через тот же Helm release. Затем требуются http scrape_url, API target UP без last_error, `api-up=1`, свежий build info, recording rule=1 и успешный smoke. При прерывании опыта восстановление выполняется явно этой командой, без удаления stack/PVC; незавершённый опыт не очищается автоматически до фиксации признаков. Остаточный риск — временный пробел метрик API и возможное срабатывание LabAPIMetricsUnavailable при длительном отказе. Receiver local-null не подтверждает доставку уведомлений.

### Выполнение и наблюдавшиеся признаки

Владелец выполнил опыт 29 октября 2026 года в 22:21–22:26 UTC (30 октября 01:21–01:26 Europe/Moscow). Перед ним предоставлены успешные `go test -count=1 ./cmd/labctl` (0.744s), `go test -count=1 -race ./cmd/labctl` (1.658s), `go vet ./cmd/labctl`, Helm lint обоих профилей и `20261029T222101.485306500Z-scrape-render`. HEAD `86e3b88b2048dd7605333de23cb59145c130d15d`, `git status --short` пустой. Это ревизия команд и chart, не образа приложения.

| Этап (UTC) | Артефакт / результат |
|---|---|
| 22:21:22–22:21:25, исходное состояние | status `20261029T222122.748401000Z-status`: lab-monitoring revision 2/deployed, lab-observability revision 2/deployed, Prometheus Ready, Alertmanager OK; targets `20261029T222124.403949200Z-targets`: 14 UP; api-up=1, build=1, затем smoke успешен |
| 22:21:55–22:21:56, включение отказа | `20261029T222155.782591200Z-scrape-fail`: lab-observability revision 3/deployed |
| 22:22:09–22:22:10, переход | Новый https target имеет health=unknown, last_error пуст; запрос api-up ещё возвращает 1. Это не доказательство успешного HTTPS-сбора |
| 22:22:55–22:23:02, отказ | `20261029T222255.816202700Z-targets`: API DOWN, `http: server gave HTTP response to HTTPS client`, остальные 13 targets UP; `20261029T222302.360566500Z-query`: api-up=0; smoke во время отказа успешен |
| 22:24:06–22:24:07, восстановление Helm | `20261029T222406.645839700Z-scrape-restore`: lab-observability revision 4/deployed |
| 22:24:14–22:24:25, переход | Prometheus ещё показывает https/DOWN, api-up=0, build пустой, recording rule=0. Smoke успешен. Финальный status этого вызова сохраняет lab-monitoring revision 2 и lab-observability revision 4, оба deployed; приложение Ready, CronJob suspended/active=0 |
| 22:25:06–22:25:09, обновлённая конфигурация | ServiceMonitor scheme=http, target уже http/unknown, api-up и recording rule ещё 0; build пустой |
| 22:26:04–22:26:07, сбор восстановлен | `20261029T222604.197205100Z-targets`: 14 UP, API `http://10.244.0.162:8080/metrics`, last_error пуст; запросы `20261029T222604.872593100Z-query`, `20261029T222605.571014000Z-query`, `20261029T222607.872318000Z-query`: api-up=1, build=1, recording rule=1 |

API сохранил Pod `lab-api-67ffc84848-m96cm`, адрес 10.244.0.162 и ревизию `d47851ccb0b7` в исходных и восстановленных метриках. Все выполненные smoke завершились Succeeded, проверочные Pod удалены штатной командой. Восстановление не потребовало остановки API, удаления monitoring или PVC. Конкретный момент первого успешного сбора между снимками не измерен: приведённое время является временем наблюдения, а не точной длительностью внутреннего reconciliation.

Причина отказа — намеренная несовместимость HTTPS-клиента сбора с HTTP-сервером. Промежуточные unknown/старые значения после upgrade показывают, почему Helm deployed и ресурс ServiceMonitor не заменяют проверку работающего сбора. Подтверждён цикл `up=1 → 0 → 1`, доступность HTTP API во время отказа, исчезновение instant build info при отсутствии успешного сбора и возвращение samples/recording rule после восстановления. Alert firing/delivery и отсутствие потерь бизнес-событий этим опытом не проверяются.

## Оставшиеся границы

Отказ и восстановление scrape выполнены без остановки основного API. Ограниченный опыт UI после изменения памяти не доказывает отсутствие утечки Grafana. Асинхронная готовность ресурсов Operator после Helm wait остаётся ограничением CLI; при ошибке установки сохраняются объекты, PVC и диагностика.

Первая итерация не доказывает метрики publisher/worker, Kafka lag, PostgreSQL exporter, p95, dashboards, tracing, Loki и доставку оповещений. local-null не подтверждает Routed/received/Resolved. Passed по YAML/render не присваивается.
