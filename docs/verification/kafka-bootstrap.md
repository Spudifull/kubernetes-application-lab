> Historical result from the private development history. Original identifiers are preserved; this is not a rerun of the public snapshot. Reviewed screenshots are included; raw captures remain private. See [provenance](../provenance.md). Local paths, where present, were redacted.

# Лабораторный сценарий: Kafka KRaft bootstrap

- Статус: `Passed` для установки, mTLS roundtrip и чтения прежней записи после штатной замены единственного broker Pod с тем же томом. Первый неуспешный smoke сохранён ниже.
- Base SHA брифа: `e35bc3900c5ed72fc0bdeef5320e8692d576f8f3`.
- HEAD начала подготовки: `a5f08ae0e6d280f6eaed407683cacadf3584f224`; установка выполнена после commit `37428aa1c6225b76b5bb31b9385f608e410d6410`, успешный исправленный повтор — после чистого commit `c5c52ee3118efea0458ce2b0a3d8a0acd22e25fb`.
- Среда: Windows PowerShell, основной kind-кластер, Kubernetes `v1.34.0`, kubectl `v1.35.9`, Strimzi `1.3.0`, Kafka `4.2.2`; namespace `kubernetes-application-lab-kafka`.
- Образ успешного probe: `kubernetes-application-lab-kafka-probe:8438a073c7e8c29b8bfaeb6e5451c91940285b706225b15e32234c1b39d34676`; runtime imageID probe отдельно не снят.
- Проверяемый контракт: [Kafka и обработка заданий](../design/kafka-event-processing.md).

## Цель и исходные условия

Сначала подтвердить достаточность ресурсов для Strimzi `1.3.0`, Kafka `4.2.2` и существующих PostgreSQL/API. Затем проверить KRaft, topics и реальную запись/чтение сообщения по TLS. Одноузловая Kafka не является HA.

## Процедура и ожидаемые признаки

1. В основном контексте выполнить `task kafka-preflight`. Сохранить вывод вместе с SHA. Команда не устанавливает компоненты, не печатает Secret и не утверждает достаточность ресурсов. Проверить node pressure, requests/limits, Docker memory, число кластеров, StorageClass и готовность API/PostgreSQL. При недостатке ресурсов согласовать изменение, не уменьшать requests действующих компонентов автоматически. Первый такой снимок предоставлен владельцем, см. ниже.
2. Подготовлены закреплённый Operator и CR Kafka/NodePool/Topic/User, бюджеты и явная retention. По [руководству](../engineering/kafka-lab.md) выполнить проверки Go, render Operator, сборку probe, затем после commit — установку Operator и bootstrap с server-side dry-run. Готовность этих файлов не является результатом установки.
3. При установке сохранить версии, образы/imageID, Kafka CR status, node ID, Pod UID, PVC/PV. Kafka CR и оба topic должны стать Ready; основной topic должен иметь три partitions и replication factor 1. Проверить ACL и отсутствие внешнего listener.
4. Ограниченный TLS-клиент должен отправить сообщение, дождаться ack и прочитать его, сохранив topic/partition/offset. CR Ready и открытый TCP-порт не заменяют этот опыт.
5. После сохранения сообщения выполнить штатную замену broker Pod с прежним PVC, проверить node ID, новый Pod UID и чтение сообщения. Это не опыт потери диска.

## Наблюдения и диагностика

Официальная матрица версий проверена при подготовке: выбранные Strimzi/Kafka совместимы с Kubernetes 1.34. Владелец выполнил новую команду preflight 25 октября 2026 года в 08:53:13–08:53:14 UTC на изменённом рабочем дереве. Отдельный SHA в этом выводе не показан; это предварительный снимок среды, не чистый канонический прогон Kafka.

Получены два существующих kind-кластера — основной и сетевой. Docker сообщил 32 CPU и `32403210240` байт памяти; основной узел имеет allocatable 32 CPU и `31643760Ki` памяти. Занятые requests — `1175m/778Mi`, limits — `850m/1266Mi`; `kubectl top` показал `115m/891Mi`. Все pressure conditions False, узел Ready. API и PostgreSQL имеют по одной готовой реплике, PostgreSQL PVC `data-lab-postgres-0` Bound на прежнем PV `pvc-08e126ef-91ad-406a-86d1-db94b3f2acbe`. Текущий API использует образ `1f35ec63...`; PostgreSQL — `16.14-bookworm`. Снимок содержит старые завершённые/Error диагностические Pod; они не удалялись подготовкой Kafka.

StorageClass `standard` по-прежнему использует `rancher.io/local-path`, WaitForFirstConsumer и reclaim policy Delete. Для Kafka выбран отдельный PVC 5Gi с `deleteClaim: false`, без изменения существующих томов. Общие requests новых broker/Operator/entity-компонентов `900m/1920Mi` помещаются в наблюдавшийся запас; потребление JVM, временный init, второй кластер и динамическая нагрузка требуют наблюдения после установки, поэтому это не гарантия отсутствия OOM/pressure.

Обнаружен kubectl `v1.36.1` при server `v1.34.0`; сам kubectl вывел предупреждение о неподдерживаемом skew. До установки нужен клиент в пределах одной minor-версии сервера. Go-команды установки проверяют это условие и отказывают до изменения namespace/CR при таком skew.

Агент выполнил только загрузку закреплённой Go-зависимости и чтение registry manifests, без установки в Kubernetes. `docker buildx imagetools inspect` разрешил официальные теги в multi-platform digest:

- Operator `quay.io/strimzi/operator:1.3.0` → `sha256:4deb564fa329afa4046c550a6f8f1a6d00be3b9297e556969acabe6647b6676f`;
- Kafka `quay.io/strimzi/kafka:1.3.0-kafka-4.2.2` → `sha256:b9b9e37491ce17c1b2722b3668661de91430d9437acf18c5f5bafaef38138652`.

Эти digest записаны в подготовленные файлы; registry manifest сам по себе не является runtime imageID. Franz-go `v1.22.2` и зависимости добавлены через Go modules. Агент Go-тесты и сборку probe не выполнял. На момент подготовки TLS ack/read и сохранение записи при замене Pod ещё не были подтверждены; последующий успешный прогон владельца описан ниже.

### Установка и первый неуспешный smoke

25 октября владелец показал чистый Git SHA `37428aa1c6225b76b5bb31b9385f608e410d6410`, клиент kubectl `1.35.9` и server `1.34.0`. Установка Operator создала Helm revision 1, bootstrap дождался Ready Kafka `lab-kafka`, всех трёх topics и трёх KafkaUser. Наблюдение показало broker `1/1 Running` и entity operator `2/2 Running` без перезапусков. Cluster ID — `Qzl4Kl9CQLSUHicuEvuaLA`, node ID — `0`; broker Pod UID `c9bc7437-0294-4258-ae43-7c82cde9df0f`. Это подтверждает установку, но не клиентский обмен сообщением.

Smoke с образом `kubernetes-application-lab-kafka-probe:34370fb4555c0da4c5166f50e983efab5b66e0158d097b4e2833e21280216a92` создал Pod `lab-kafka-smoke-1792920133750161900`, который завершился `Error`. Журнал: `kafka-probe: invalid autocommit options specified when a group was not specified`. В общем конструкторе стояла `DisableAutoCommit()` при отсутствии consumer group. Franz-go отклонил конфигурацию до сетевого подключения; публикация, проверка TLS и broker ack этим запуском не выполнены. Последующее ожидание `Succeeded` истекло через 90 секунд — это вторичная ошибка сценария, не свидетельство отказа broker.

Повторная сборка без изменения Go-кода записала образ `34d27e606db8d5bbf979b0f95c2f95877bdc428b7ed6d72faecd6996cf55f725`, но не исправила конфигурацию. По запросу владельца групповая опция удалена из общих настроек; group consumer должен задавать её явно вместе с группой. Добавлены network-free регрессионные проверки фактического набора опций через `kgo.ValidateOpts` для producer, прямого consumer и group consumer с ручным commit. Эти новые тесты агент не запускал. Исправление требует нового commit владельца, пересборки probe и успешного smoke; исходный отказ сохранён независимо от результата повтора. Broker, topics, Secrets и PVC для исправления менять не нужно.

### Успешный чистый повтор после исправления

25 октября владелец предоставил успешные `go test ./...`, `go test -race ./...` и `go vet ./...`, затем пустой `git status --short` и SHA `c5c52ee3118efea0458ce2b0a3d8a0acd22e25fb`. Пакеты `cmd/kafka-probe` и `internal/kafka` показали новые результаты обычных и race-тестов; часть остальных пакетов использовала кэш. `cmd/api` и `internal/database` по-прежнему не имеют тестовых файлов. Агент эти проверки не запускал. Сборка probe после фиксации исходников записала образ `8438a073...`, использованный всеми тремя успешными клиентскими Pod.

| Шаг | Время по Москве, 25 октября | Наблюдение |
|---|---|---|
| Roundtrip | 12:29:37.727–12:29:37.751 | `kafka_smoke_written` и `kafka_smoke_read`, key `smoke-1792920576656055300`, topic `lab.smoke.v1`, partition `0`, offset `0` |
| Marker до замены | 12:30:18.139 | Broker подтвердил запись key `smoke-1792920617394070700`, partition `0`, offset `1` |
| Начало замены | 12:30:20.206 | `kafka_broker_restart_before`, прежний Pod UID и идентичность тома сохранены |
| Готовность новой реплики | 12:30:44.487 | `kafka_broker_restart_completed`, новый UID при прежних node ID/PVC/PV |
| Чтение после замены | 12:30:46.863 | `kafka_smoke_read` вернул прежний key на partition `0`, offset `1`, без новой публикации |

Имя broker Pod осталось `lab-kafka-dual-role-0`, UID изменился с `c9bc7437-0294-4258-ae43-7c82cde9df0f` на `99405621-957e-4d82-892b-80f0cffdc2ea`. Node ID остался `0`, cluster ID — `Qzl4Kl9CQLSUHicuEvuaLA`. PVC `data-0-lab-kafka-dual-role-0` сохранил UID `952158d8-ec04-4ad6-a748-8212b33cab2d` и PV `pvc-952158d8-ec04-4ad6-a748-8212b33cab2d`, состояние Bound, ёмкость 5Gi, RWO, StorageClass standard. Новый broker стал `1/1 Running` без перезапусков; наблюдавшийся imageID — `quay.io/strimzi/kafka@sha256:b9b9e37491ce17c1b2722b3668661de91430d9437acf18c5f5bafaef38138652`.

Финальный статус Kafka CR содержит `Ready=True`, `kafkaVersion: 4.2.2`; все три KafkaTopic и три KafkaUser имеют Ready. Основной и DLQ topics имеют по три partitions и replicas 1, smoke — одну partition и replicas 1. Entity Operator готов `2/2`, Cluster Operator — `1/1`. Smoke выполнился с клиентскими сертификатами и проверкой доверия/имени сервера, но отрицательные TLS/ACL-пробы этим результатом не заменяются.

После опыта `task smoke` основного API успешно получил `/version.revision=803c1bc2363c` через Service, временный API smoke Pod удалён. Основной API намеренно не пересобирался в этом опыте: это регрессия прежнего stateless-профиля. `task db-status` показал PostgreSQL `1/1 Running`, restartCount 0, прежний PVC/PV Bound. Это проверка готовности и хранилища PostgreSQL, не повторное SQL-чтение контрольных записей и не интеграция Kafka с БД.

При отказе сохранять условия CR, Events, журналы Operator и broker, состояния PVC/PV и клиентскую ошибку; различать scheduler failure, нехватку памяти, недоступность диска, TLS и ACL. Не удалять PVC или Secret для устранения ошибки.

## Восстановление и границы

При подготовке агент кластеры не изменял; установку и замену Pod выполнил владелец. После штатной замены Kafka вернулась в готовое состояние, marker сохранился. Три успешных диагностических Pod и прежний неуспешный Pod остались для доказательства; cleanup в последнем выводе не выполнялся. После сохранения журналов допускается `task kafka-smoke-cleanup`, удаляющий только диагностические Pod. Namespace, основной Secret, PVC PostgreSQL/Kafka, topics с рабочими данными и kind-кластеры не входят в автоматический cleanup.

Kubernetes/Strimzi восстановили Pod с устойчивой идентичностью и прежним томом; Kafka сохранила доступную запись на том же offset. Доказан один локальный штатный цикл, не потеря узла/диска, HA, авария broker, отсутствие потерь под нагрузкой или backup. Probe не вступал в consumer group: offset записи не является committed offset worker. Outbox, идемпотентный эффект PostgreSQL, rebalance, retry/DLQ, worker crash/SIGTERM, CronJob и исполнение NetworkPolicy на kindnet требуют отдельных реализаций и опытов. Весь Milestone 7 ещё не закрыт.
