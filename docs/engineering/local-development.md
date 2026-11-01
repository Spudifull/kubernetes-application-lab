# Локальная разработка

Milestones 1–8 завершены в границах локальных [verification-отчётов](../verification/README.md). Метрики приложения, зависимости, журналы, трассировка и оповещения прошли отдельные runtime-проверки; [итоговая приёмка Milestone 8](../verification/grafana-dashboards.md#итоговая-приёмка-milestone-8) фиксирует восстановленное состояние стенда и ограничения. Работающие приложения не обновляются автоматически.

**Состояние: Current для исполняемого интерфейса Helm и подтверждённых лабораторных сценариев. Базовый объём Milestone 7 закрыт отдельными отчётами Kafka/outbox/worker, retries/DLQ, redelivery, broker recovery и CronJob. Эти результаты не означают production-ready или HA.**

## Требования

Для полного цикла нужны Go 1.26, Docker Engine, `kind`, `kubectl`, Helm и Task. Docker Engine должен использовать контейнеры Linux. `task cluster-up` закрепляет образ узла Kubernetes v1.34.0 по digest.

Сложные задачи `Taskfile.yml` вызывают `cmd/labctl/` через `go run`. CLI использует стандартную библиотеку Go и установленный на хосте `kubectl`, `helm`, `kind` и Docker; Windows PowerShell не является обязательным условием. Прежние `.ps1` удалены после переноса публичных задач на Go. Для действий, меняющих кластер, CLI проверяет текущий контекст; перед запуском выберите нужный `kind-kubernetes-application-lab` или `kind-kubernetes-application-lab-net`. Выполненные проверки и их границы перечислены в [отчёте о переносимости](../verification/cross-platform-tooling.md).

## Milestone 7: до установки Kafka

В основном контексте выполнить `task kafka-preflight` и сохранить вывод. Это только сбор CPU/памяти, занятых requests/limits, StorageClass и состояния API/PostgreSQL; Kafka не устанавливается. Preflight владельца позволил выбрать бюджет одной broker/controller-ноды, не меняя API/PostgreSQL. Kubectl должен быть в пределах одной minor-версии API server; владелец переключился на клиент `1.35.9` для сервера `1.34`, устранив прежний skew. Инфраструктура и smoke уже проверены; дальнейшие команды здесь служат воспроизведению, а не требованию переустановки. Порядок проверки и команды — [Kafka lab](kafka-lab.md); контракт — [Kafka и обработка заданий](../design/kafka-event-processing.md), текущие доказательства — в [bootstrap-отчёте](../verification/kafka-bootstrap.md).

## Быстрый путь

`Taskfile.yml` является единым исполняемым входом. Основной порядок:

```text
task test
→ task image-build
→ task cluster-up
→ task helm-lint
→ task helm-render
→ task deploy
→ task helm-status
→ task status
→ task smoke
```

`task cluster-up` пропускает создание, если контекст выделенного кластера уже доступен. Команды `kubectl` и Helm в `Taskfile.yml` явно используют контекст `kind-kubernetes-application-lab`. Для проверки чистой установки нужен новый выделенный кластер, а не ранее созданные вручную ресурсы приложения.

`task image-build` передаёт в приложение короткий Git SHA. Если `git status --porcelain` сообщает изменения или неотслеживаемые файлы, к revision добавляется `-dirty`. Дата сборки остаётся отдельным полем и не участвует в идентификации исходного состояния.

Каждая отличимая сборка получает тег по полному ID образа. Ссылка записывается в игнорируемый Git файл `.local-image-ref`; `task deploy` читает её и проверяет наличие образа в Docker. Файл относится к последней успешной сборке: после неудачной сборки он может продолжать указывать на предыдущий образ. Значение `/version.revision` описывает исходное дерево, а тег Helm — собранный образ.

`task deploy` проверяет и применяет `Namespace`, проверяет chart, загружает записанный образ в `kind` и выполняет `helm upgrade --install` с ожиданием готовности. Успешное повторение команды без новой сборки не меняет шаблон Pod. Новая сборка передаёт другой тег в Helm и создаёт новое состояние `Deployment`.

`task helm-status` показывает текущее состояние release, `task helm-history` — историю его ревизий. `task undeploy` удаляет только release. `Namespace` и кластер удаляются только отдельным явным действием.

## Переход со старых манифестов

Helm не присваивает себе существующие `Deployment/lab-api` и `Service/lab-api`, созданные через `kubectl apply`. В выделенной лабораторной среде перед первым Helm install явно удалите прежние ресурсы приложения или пересоздайте кластер `kind`. Не выполняйте этот шаг в общем кластере без проверки владельца ресурсов.

Для проверки обновления после первого `task deploy` и `task smoke` сохраните ссылку на образ, номер ревизии release и имя Pod. Повторите `task image-build`, убедитесь, что `.local-image-ref` изменился, затем выполните `task deploy`, `task helm-history`, `task status` и `task smoke`. Сравните фактический `image` нового Pod с записанной ссылкой. Одинаковый исходный Git SHA допустим: дата сборки и ID образа различают две сборки.

## Проверка Service

`task smoke` вызывает `smoke-in-cluster`: временный клиентский Pod выполняет HTTP-запрос через DNS-имя `lab-api.kubernetes-application-lab.svc`. Успех этой команды проверяет внутрикластерный путь через DNS, `ClusterIP Service`, готовый `EndpointSlice` и Pod приложения.

`task smoke-api-proxy` выполняет отдельную диагностическую проверку через Kubernetes API Service proxy. Она полезна для локализации сетевой ошибки, но не заменяет `task smoke`.

Для Milestone 5 подготовлен отдельный контекст `kind-kubernetes-application-lab-net`. `task network-cluster-up` создаёт одноузловой кластер без штатного CNI; состояние узла `NotReady` до установки Cilium ожидаемо. `task network-cni-install` устанавливает закреплённый Cilium только в этот контекст. Затем `task network-cni-status` проверяет узел, компоненты и API server. После подтверждения готовности `task network-deploy` загружает записанный образ и устанавливает тот же chart, а `task network-smoke` выполняет запрос через Service. Эти действия не удаляют и не пересоздают исходный кластер.

Отрицательные сетевые проверки выполняются только после успешного `network-smoke`: `task network-diagnostics-start` создаёт ограниченные ресурсы, `task network-diagnostics-wait` дожидается клиента, `task network-diagnostics-status` сохраняет данные. При ошибке ожидания сначала получить состояние и журнал; после сохранения результата явно выполнить `task network-diagnostics-cleanup` и повторить `task network-smoke`. У временного Pod с неготовой проверкой есть ограничение времени, поэтому не откладывать запуск клиента после подготовки ресурсов. Смысл каждого отказа и граница доказательства описаны в [отчёте](../verification/service-dns-diagnostics.md).

Для проверки `NetworkPolicy` основной namespace оставляется без правил. Отдельный `task network-policy-deploy` устанавливает тот же chart в namespace `kubernetes-application-lab-policy`; затем `task network-policy-validate`, `task network-policy-clients-start` и без долгой паузы `task network-policy-test` выполняют последовательность deny/allow. Клиентские Pod ограничены по времени. Сохранить полный вывод и `task network-policy-status` до `task network-policy-cleanup`. При ошибке теста не очищать ресурсы до диагностики. После очистки проверить основной API через `task network-smoke`. Сценарий не требует второго chart или изменения CNI исходного кластера.

Для опыта с Gateway API в том же выделенном сетевом кластере сначала установить контроллер и CRD через `gateway-controller-install`, затем проверить и применить инфраструктурные ресурсы через `gateway-infra-validate` и `gateway-infra-start`. Не применять `gateway-infra.yaml` повторно после включения HTTPS: он описывает исходное состояние только с HTTP listener. Маршрут приложения формируется `gateway-route-render` и включается в существующий release через `gateway-route-enable` без повторной сборки API. Создать сертификаты через `gateway-cert-create`, отдельным действием создать Secret и затем включить HTTPS listener; существующий каталог ключей команда не перезаписывает. В другом окне оставить `gateway-forward`, после чего выполнять HTTP, TLS и отрицательные проверки. `gateway-status` и `gateway-negative-status` сохраняют условия и журналы до `gateway-negative-cleanup`. Все задачи находятся в `Taskfile.yml`; значения hostname, портов и ограничения доверия описаны в [контракте Gateway](../design/deployment-model.md), признаки успеха — в [сценарии](../verification/gateway-routing-tls.md). Приватные ключи и CA не добавлять в Git и не использовать для других сред.

Для проверки RBAC в ограниченном namespace сначала убедиться, что второй release `lab-api` существует и прежние экспериментальные `NetworkPolicy` не мешают клиенту. `task rbac-validate` проверяет конфигурацию на стороне API server, `task rbac-start` создаёт отдельную идентичность и временный Pod, затем `task rbac-test` сравнивает разрешения с фактическими ответами. До удаления клиента сохранить `task rbac-status`; при ошибке оставить Pod для диагностики. `task rbac-cleanup` удаляет только временный Pod. Назначение разрешений и границы доказательства описаны в [сценарии RBAC](../verification/rbac-access-control.md).

Для Pod Security Admission использовать `task pod-security-test` в том же ограниченном namespace. Сценарий не создаёт Pod с `hostPID: true`: он выполняет только серверный dry-run и проверяет причину отказа. Допустимый временный Pod остаётся для `task pod-security-status`; после сохранения вывода выполнить `task pod-security-cleanup` и `task network-smoke`. При неожиданном ответе сначала сохранить состояние, не применять отрицательный манифест вручную. Границы опыта — в [сценарии Pod Security Admission](../verification/pod-security-admission.md).

`task verify-recovery` удаляет единственный Pod, ждёт готовности замены и повторяет внутрикластерную проверку. Этот сценарий допускает краткий перерыв и не проверяет высокую доступность.

Отложенное удаление временного Pod для `task smoke` регистрируется до создания клиента. Ошибка запроса завершает задачу ошибкой; клиентский Pod при этом удаляется. Для диагностики используйте вывод задачи, события и журналы приложения.

## Опыты с обновлением

Базовая команда `task deploy` сохраняет одну реплику и выключает `/work`. Для лабораторного профиля выполнить `task helm-lint-rollout` и `task helm-render-rollout`, затем `task rollout-client-build` и `task deploy-rollout`. Последняя команда использует тот же записанный образ API, но задаёт две реплики, `maxSurge: 1`, `maxUnavailable: 0` и включает `/work`.

`task rollout-load-start` создаёт ограниченный по времени клиентский Pod; он обращается к `Service` примерно раз в 100 мс в течение трёх минут. В отдельном окне `task rollout-load` показывает события, итоговые счётчики и `run_id`. После сохранения результатов выполнить `task rollout-load-cleanup`. Следующий опыт требует нового запуска клиента. Во время работы клиента новая сборка API и `task deploy-rollout` выполняют обычное обновление Helm; состояния контроллера показывает `task rollout-status`.

Для диагностики обновления `task rollout-observe` раздельно выводит снабжённые временем снимки Pod и условий `EndpointSlice` в течение ограниченного периода. После сборки нового образа, непосредственно перед обновлением, `task rollout-probes-start` запускает прямые проверки `/readyz` у двух старых Pod; их результаты сохраняет `task rollout-probes-status`. Запустить наблюдение до обновления и сохранить вывод вместе с журналами старых Pod и клиента. Снимки не равнозначны фактическим ответам через `Service`; клиент записывает в каждом событии HTTP-ошибки ответный заголовок `X-Lab-Pod`.

Для принятого длительного запроса `task inflight-work` запускается в терминале после подготовки клиентского образа. Он проверяет журнал `work_started` перед удалением выбранного Ready Pod и сохраняет клиентский Pod для диагностики. Неработоспособный образ создаётся только явной командой `task rollout-fail`; её ошибка ожидаема. После записи диагностики вернуть конкретную работоспособную ревизию командой `task rollout-rollback ROLLBACK_REVISION=N`. Обычный `task deploy` после отката не выполнять до анализа: он вновь применит последнюю локальную ссылку из `.local-image-ref`.

## Опыт с HPA

Для HPA подготовлен отдельный профиль chart. После создания чистого commit проверить `task helm-lint-hpa`, `task helm-render-hpa` и `task hpa-load-validate`, затем собрать из этой ревизии образы API и клиента. Убедиться, что `task metrics-server-verify` получает показатели, и только после этого выполнить `task deploy-hpa`: команда ждёт две Ready-реплики. Возможный краткий переход к одной реплике при первом отказе Helm от фиксированного `spec.replicas` нужно записать как отдельное наблюдение.

Перед запуском пятиминутной нагрузки в другом окне начать `task hpa-observe`. Затем выполнить `task hpa-load-start` и `task hpa-load-wait`. Сразу после её окончания `task rollout-load-start` поддерживает лёгкие запросы через `Service` на протяжении окна scale-down. Вывод `task hpa-load-status`, `task rollout-load-status` и `task hpa-status` сохраняется до `task hpa-load-cleanup` и `task rollout-load-cleanup`. Только после записи результата можно вернуть фиксированный профиль через `task deploy-rollout`. Подробные критерии, время наблюдения и границы находятся в [`../verification/hpa-autoscaling.md`](../verification/hpa-autoscaling.md).

## Проверка ограничения размещения

После других опытов с ресурсами проверить отдельный одноузловой сценарий: `task scheduling-validate`, затем `task scheduling-start` и `task scheduling-wait`. Сохранить `task scheduling-status` и `task smoke` до `task scheduling-cleanup`. Клиентский Pod намеренно запрашивает CPU больше `allocatable` узла, но не должен запускаться и не создаёт фактическую CPU-нагрузку. `Pending` без события `FailedScheduling` с причиной нехватки CPU не считать успехом; подробная диагностика и границы результата находятся в [`../verification/scheduling-capacity.md`](../verification/scheduling-capacity.md).

## Диагностика

В выделенном `kind` Metrics Server установлен отдельным Helm release `metrics-server` в `kube-system`; после повторного обновления из чистой ревизии Metrics API предоставил показатели. Для воспроизведения на другом выделенном кластере проверьте контекст и версию Kubernetes, сохраните исходную ошибку `kubectl top` при отсутствии Metrics API, затем выполните `task metrics-server-render`, `task metrics-server-install`, `task metrics-server-status` и `task metrics-server-verify`. Команды установки и проверки в `Taskfile.yml` используют только контекст `kind-kubernetes-application-lab`. Если Metrics Server уже установлен, исходное отсутствие метрик воспроизводить удалением работающего release не нужно. Полный порядок, результат и границы находятся в [`../verification/metrics-server.md`](../verification/metrics-server.md).

Профиль [`../../cluster/kind/metrics-server-values.yaml`](../../cluster/kind/metrics-server-values.yaml) отключает проверку сертификата только на соединении Metrics Server → kubelet в этой учебной среде. На соединении Kubernetes API server → Metrics Server используется сгенерированный chart сертификат с проверкой по CA. Исключение для kubelet нельзя переносить в общий или промышленный кластер: там нужен доверенный сертификат kubelet. Успех установки или готовность Deployment сами по себе не подтверждают наличие свежих показателей узлов и Pod.

Для подготовленного CPU-опыта сначала проверить профильные Helm-манифесты, собрать образы API и клиента из чистой ревизии, затем поочерёдно развернуть профили высокого и низкого CPU limit. Перед запуском и после завершения каждого ограниченного клиентского Pod сохранить вывод `task resources-cpu-cgroup`, имя API Pod и Helm revision. Клиентский Pod удалять через `task resources-cpu-cleanup` только после сохранения его журнала; перед повторным запуском с другим профилем одноимённый Pod должен отсутствовать. Возврат к обычному профилю выполняется явной командой `task deploy` либо `task deploy-rollout`. Порядок и границы доказательства находятся в [`../verification/cpu-throttling.md`](../verification/cpu-throttling.md).

Опыт с памятью использует отдельный временный Pod и уже существующий клиентский образ. После сборки клиента из чистой ревизии `task resources-oom-start` создаёт Pod, `task resources-oom-wait` ждёт его перехода в `Failed`, а `task resources-oom-status` сохраняет точную причину завершения контейнера, код выхода, лимиты, события и журнал. Переход в `Failed` сам по себе не доказывает `OOMKilled`; проверить поле `status.containerStatuses[].state.terminated.reason`. Только после сохранения вывода выполнить `task resources-oom-cleanup`, затем проверить основной release через `task rollout-status` и `task smoke`. Подробная граница — в [`../verification/oomkilled.md`](../verification/oomkilled.md).

`task status` показывает состояние `Deployment`, Pod, `Service`, описание `Deployment` и соответствующий `EndpointSlice`.

При сбое сначала фиксируются состояние контроллера и Pod, события, журналы, готовые конечные точки и сетевой путь. `CrashLoopBackOff`, `Pending` и `NotReady` являются симптомами, а не причинами.

## Состояние проверки

Для третьей итерации Kafka добавлены отдельный worker и задачи `worker-*`. Порядок воспроизведения с одной/двумя репликами и SIGTERM находится в [инструкции](kafka-worker-lab.md). [Отчёт Passed](../verification/kafka-worker.md) фиксирует чистый образ `858f2ee181e4`, SQL-эффект до offsets, дубликат, batch, завершение принятой работы и финальную очистку. Retry/DLQ и аварийная redelivery остаются отдельной работой.

Пользователь сообщил об успешной проверке этапа 1.1 на изменённом дереве `f9e7b663af7a-dirty` 8 октября 2026 года. Она подтверждает предварительный сетевой путь и восстановление Pod через прежние YAML-манифесты, но не установку Helm на чистой ревизии. Статус каждого сценария в `docs/verification/` определяется его собственным отчётом и достаточностью зафиксированного выполнения.
