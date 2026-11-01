# Kafka: остановка единственного broker/controller

Состояние — [Passed](../verification/kafka-broker-outage.md) для зафиксированного выполнения. Проверяемый контракт — [Kafka и обработка заданий](../design/kafka-event-processing.md). Работать только в основном kind. Kafka имеет одну совмещённую broker/controller-ноду: во время опыта публикация и потребление недоступны. Здесь выполняется управляемая остановка процесса и последующее восстановление, а не внезапное выключение узла или отказ диска.

## 1. Проверки перед отказом

```powershell
go test -race ./cmd/labctl
go vet ./cmd/labctl
```

После успешных проверок владелец фиксирует код сценария и сохраняет `git status --porcelain`, `git rev-parse HEAD`. Рабочее дерево перед каноническим прогоном должно быть чистым. Приложение не менялось: новые образы API/publisher/worker и миграции не нужны. Сохранить их работающие версии отдельно от SHA сценария.

В каждом новом окне PowerShell:

```powershell
$env:Path = "$(Resolve-Path .cache\kubectl\v1.35.9);$env:Path"
kubectl config use-context kind-kubernetes-application-lab
kubectl version
```

Сохранить исходное состояние:

```powershell
task kafka-status
task publisher-status
task worker-status
task worker-group-status
task db-status
task outbox-status
task worker-reliability-status
task kafka-smoke-write
```

Нужны Ready broker, Operator, API/publisher/worker/PostgreSQL, Bound PVC, отсутствие pending outbox, Stable группа и отсутствие необработанных записей. Если есть незавершённая работа или лабораторный неверный bootstrap, сначала восстановить её. Не удалять задания или сбрасывать offsets. Smoke write сохраняет новый marker в отдельном topic и locator; до чтения после восстановления не выполнять другой smoke-write/успешный roundtrip. Опыт закончить до часовой retention marker, иначе невозможность его чтения не доказывает потерю тома.

Если API port-forward ещё не работает, открыть отдельное окно:

```powershell
kubectl --context kind-kubernetes-application-lab port-forward deployment/lab-api 18081:8080 -n kubernetes-application-lab --address 127.0.0.1
```

## 2. Начало отказа

```powershell
task kafka-broker-outage-start
task kafka-broker-outage-status
```

Команда проверяет известный одноузловой состав Kafka, единственный cluster в namespace Operator, владельца broker Pod, Ready broker, Bound PVC и идентификаторы ресурсов. Затем сохраняет `.local-kafka/broker-outage.json` **до мутаций**, переводит Deployment Cluster Operator 1 → 0, ждёт исчезновения его Pod, удаляет только broker Pod и проверяет его отсутствие. StrimziPodSet, Kafka/KafkaNodePool, Entity Operator, topics, offsets, Secret и PVC не редактируются и не удаляются. Operator остановлен, чтобы его PodSet controller не восстановил брокер до измерения отказа.

Фактический сигнал отказа — broker Pod отсутствует, а не Kafka CR Ready: без Operator status CR может оставаться устаревшим. EndpointSlice тоже может некоторое время сохранять прежние endpoints; оценивать readiness endpoint и клиентский запрос отдельно.

Активный recovery snapshot не перезаписывается. При ошибке после его сохранения **сначала восстановить Operator командой restore**, даже если удаление брокера ещё не произошло. Повтор start не предназначен для продолжения частично выполненного отказа. На время активного отказа не выполнять Helm upgrade Operator, Kafka bootstrap/restart, смену topology или удаление CR/PVC.

## 3. Приём заявки и диагностика недоступности

В основном окне:

```powershell
curl.exe --noproxy "*" --max-time 5 -i http://127.0.0.1:18081/livez
curl.exe --noproxy "*" --max-time 5 -i http://127.0.0.1:18081/readyz
task job-enqueue
task job-status
task outbox-status
```

Ожидаются API livez/readyz 200 и enqueue 202, новая accepted-заявка, outbox_rows=1, effects=0, published_at/source partition/offset отсутствуют. Это работает, потому что приём использует PostgreSQL и не требует Kafka. Новый enqueue до завершения опыта не выполнять: locator должен продолжать указывать на исходное задание.

Открыть журнал publisher и дождаться `outbox_publish_failed` для нового event ID:

```powershell
kubectl --context kind-kubernetes-application-lab logs -f deployment/lab-publisher -n kubernetes-application-lab --timestamps |
    Tee-Object .local-kafka/broker-outage-publisher.log
```

После получения нужной ошибки Ctrl+C останавливает только чтение журнала. Затем сохранить:

```powershell
task outbox-status
task publisher-status
task worker-status
task db-status
task kafka-broker-outage-status
kubectl --context kind-kubernetes-application-lab logs deployment/lab-worker -n kubernetes-application-lab -c worker --timestamps |
    Tee-Object .local-kafka/broker-outage-worker.log
```

Нужны pending outbox с attempts/error_code/next_retry_at, отсутствие publisher ack, отсутствие новых эффектов, неготовность зависимых процессов при сохранении их Pod и числа перезапусков. Если число перезапусков изменилось, сохранить причину и не объяснять её без журнала. Entity Operator — отдельный компонент; его поведение не выводится из probes приложения. Не запускать group-status при отсутствующем брокере: он не может измерить группу через недоступный Kafka API.

Для подтверждения клиентского отказа можно отдельно выполнить `task kafka-smoke`: ошибка здесь ожидаема, её журнал нужно сохранить. Не принимать любую ошибку probe за доказательство отказа брокера: сравнить с успешным write до отказа и отсутствием broker Pod. Неудачный smoke не заменяет сохранённый locator; успешный roundtrip заменяет, поэтому при неожиданном успехе остановить опыт и исследовать, появился ли broker. Сохранённый probe Pod оставляется до финальной очистки.

## 4. Восстановление

После сохранения исходного pending-состояния:

```powershell
task kafka-broker-outage-restore
task kafka-broker-outage-status
task kafka-status
task kafka-smoke-read
```

Restore сверяет UID Kafka, KafkaNodePool, StrimziPodSet, Operator Deployment, PVC UID/PV и node ID до восстановления. Затем через тот же kubectl scale возвращает Operator к одной реплике, ждёт rollout и Ready broker, снова сверяет идентификаторы. У нового broker Pod должен быть другой UID при прежних PVC/PV/node ID. Поле broker_replaced=false означает, что брокер не заменялся (например, start остановился раньше его удаления); это восстановление частичного опыта, не доказательство полного отказа.

В выполненном опыте Operator стал Ready раньше появления broker Pod. Если ожидание истекло или команда прервана, сохранить snapshot, прочитать журнал Operator, проверить фактические Pod и события; не считать старый Kafka CR Ready признаком восстановления. Повтор restore продолжает восстановление с сохранёнными идентификаторами. Не удалять storage и не создавать broker Pod вручную. Причина наблюдавшейся задержки не установлена; длительность ожидания CLI не является гарантией времени восстановления.

После успеха recovery state переносится в `.local-kafka/broker-outage-restored-<timestamp>.json`. Подтверждение Ready не доказывает доставку и хранение: smoke-read должен прочитать marker по исходному offset, не публикуя замену.

Без перезапуска API/publisher/worker дождаться публикации и обработки исходной заявки:

```powershell
task job-status
task outbox-status
task worker-reliability-status
task publisher-status
task worker-status
task worker-group-status
kubectl --context kind-kubernetes-application-lab logs deployment/lab-publisher -n kubernetes-application-lab --timestamps |
    Tee-Object .local-kafka/broker-restored-publisher.log
kubectl --context kind-kubernetes-application-lab logs deployment/lab-worker -n kubernetes-application-lab -c worker --timestamps |
    Tee-Object .local-kafka/broker-restored-worker.log
```

Повторить снимки после завершения: та же job/event ID получает publisher ack и published_at, затем succeeded с одним эффектом и result; outbox_rows=1 и pending=0. Committed offset достигает source offset+1, группа Stable, измеренный lag обработанных partitions равен 0. Прежние offsets не сбрасываются. Сравнить Pod UID и restarts приложений с исходными; если менялись, отдельно ограничить утверждение о восстановлении без перезапуска.

## 5. Финальная проверка и очистка

```powershell
task db-status
task smoke
task kafka-smoke-cleanup
task worker-diagnostics-cleanup
```

Сохранить результаты перед очисткой диагностических Pod. Не удалять Kafka topics, группу, задания, outbox, квитанции DLQ, Secret и PVC. Закрыть ненужные port-forward через Ctrl+C. Recovery snapshots сохраняются локально для осмотра; существенные UID, offsets, времена, SHA и образы перенести в отчёт. Если interrupted-опыт ещё активен, восстановить его прежде, чем закрывать работу.

## Аварийный возврат при утрате локального snapshot

Если restore не может прочитать или валидировать snapshot, сохранить ошибку и состояние. Для возврата только известного Operator в этом лабораторном контексте:

```powershell
kubectl --context kind-kubernetes-application-lab scale deployment/strimzi-cluster-operator -n kubernetes-application-lab-kafka --replicas=1
kubectl --context kind-kubernetes-application-lab rollout status deployment/strimzi-cluster-operator -n kubernetes-application-lab-kafka --timeout=180s
task kafka-broker-outage-status
```

Не удалять PVC и не пересоздавать кластер. Без исходных идентификаторов восстановление доступности не доказывает сохранность прежнего PVC/PV/node ID. До дальнейших опытов разобраться с активным recovery file; не удалять его автоматически и не считать такой обход Passed.
