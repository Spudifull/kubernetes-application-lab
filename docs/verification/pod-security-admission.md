> Historical result from the private development history. Original identifiers are preserved; this is not a rerun of the public snapshot. Reviewed screenshots are included; raw captures remain private. See [provenance](../provenance.md). Local paths, where present, were redacted.

# Лабораторный сценарий: Pod Security Admission

- Статус: `Passed` — повтор после commit подтвердил отказ Pod Security Admission, допуск ограниченного Pod, очистку и доступность основного API
- Ревизия сценария успешного повтора: `fe61a2359d47a2e75e304df06b1d12f712c26de8` (commit 18 октября 2026 года в 22:12:37 МСК; журнал допустимого Pod — 22:13:49 МСК). Первый прогон: `4e4854aab2b7d652f4164ffb3bcd2e4b948b64bd`; предварительный повтор до commit — тот же базовый commit с изменениями сценария `hostPID`. Образ основного API в проверке доступности: `92cf68d43214`
- Среда: выделенный `kind-kubernetes-application-lab-net`, Kubernetes v1.34.0, namespace `kubernetes-application-lab-policy`
- Проверяемый контракт: [модель поставки](../design/deployment-model.md) и [стратегия проверки](../engineering/verification-strategy.md)

## Цель

Проверить, что `pod-security.kubernetes.io/enforce=restricted` действительно отклоняет Pod с `spec.hostPID: true`, но допускает отдельный Pod с ограниченными настройками безопасности. Одновременно сверить фактический шаблон работающего API с его контрактом безопасности, не меняя Helm release.

## Исходные условия

Выделенный сетевой кластер и namespace существуют, `Deployment/lab-api` имеет готовый Pod. Сценарий проверяет точное значение метки `enforce` до отрицательного запроса и настройки шаблона API: запуск без root, `runAsNonRoot`, запрет повышения привилегий, сброс capabilities, `seccompProfile`, корневую файловую систему только для чтения и отключённое автоматическое монтирование токена.

## Способ создать нагрузку или отказ

`task pod-security-test` отправляет `scripts/kubernetes/pod-security-hostpid.yaml` только через `create --dry-run=server`; создание небезопасного Pod не выполняется. Его существенное отличие от допустимого примера — `hostPID: true`. Сценарий требует отказа с сообщением `violates PodSecurity "restricted`, указанием `host namespaces` и `hostPID`, затем отдельно проверяет отсутствие этого Pod. Другой отказ API, ошибка соединения или успешное создание не принимаются за доказательство политики.

После этого тот же сценарий выполняет серверную проверку `scripts/kubernetes/pod-security-allowed.yaml`, создаёт короткоживущий допустимый Pod, ждёт `Succeeded` и читает журнал. Этот Pod остаётся для `task pod-security-status`; `task pod-security-cleanup` удаляет только его. Основной API и namespace не изменяются.

## Ожидаемые признаки

Метка namespace равна `restricted`; работающий API Pod готов. Серверный dry-run недопустимого Pod завершается отказом Pod Security Admission, а объекта `Pod/lab-pss-hostpid` нет. Допустимый Pod проходит серверную проверку, завершается `Succeeded` без перезапусков, затем удаляется явным действием. Журнал допустимого Pod показывает выполнение `curl --version`, не сетевой доступ к API.

## Наблюдавшиеся признаки

В первом запуске `task pod-security-test` подтвердил `enforce=restricted`, Ready-состояние API Pod `lab-api-b7569776-95q6h` и его настройки безопасности. Однако сервер вернул ошибку допустимости Pod: нельзя одновременно задать `allowPrivilegeEscalation: false` и `privileged: true`. Скрипт правильно отверг этот ответ как недоказательный и не создал допустимый Pod. `task pod-security-status` показал `Deployment/lab-api` в состоянии `1/1` и отсутствие обоих экспериментальных Pod. `task pod-security-cleanup` не нашёл объект для удаления. Последующий `task network-smoke` завершился успешно: клиент получил `/version` через `Service`, `EndpointSlice` содержал адрес API Pod. Этот первый отказ не был отказом Pod Security Admission.

В предварительном повторе 18 октября 2026 года `task pod-security-test` подтвердил `enforce=restricted`, готовность API Pod `lab-api-b7569776-95q6h` и настройки безопасности шаблона и Pod. Серверный dry-run `Pod/lab-pss-hostpid` завершился кодом `1` и сообщением `Forbidden: violates PodSecurity "restricted:latest": host namespaces (hostPID=true)`. Затем серверный dry-run допустимого Pod прошёл, объект был создан и достиг `Succeeded`; журнал показал выполнение `curl --version`. `task pod-security-status` показал `Deployment/lab-api` `1/1`, допустимый Pod в `Completed` без перезапусков и отсутствие недопустимого Pod. `task pod-security-cleanup` удалил допустимый Pod. Последующий `task network-smoke` завершился успешно: клиент получил `/version` с ревизией образа `92cf68d43214` через `Service`, а `EndpointSlice` содержал адрес API Pod. Исправленный сценарий выполнялся до commit, поэтому это наблюдение не закрывает требование проверки на чистой ревизии.

После commit `fe61a235` пользователь повторил те же четыре команды. `task pod-security-test` снова подтвердил `enforce=restricted`, Ready-состояние API Pod и его настройки безопасности. Серверный dry-run недопустимого Pod завершился кодом `1` с точной причиной `violates PodSecurity "restricted:latest": host namespaces (hostPID=true)`. Допустимый Pod прошёл серверную проверку, был создан и завершился; его журнал в 22:13:49 МСК показал выполнение `curl --version`. `task pod-security-status` зафиксировал `Deployment/lab-api` `1/1`, допустимый Pod `Completed` с `RESTARTS=0` и отсутствие `Pod/lab-pss-hostpid`. `task pod-security-cleanup` удалил допустимый Pod. `task network-smoke` в кластере Kubernetes v1.34.0 получил `/version` через `Service` с ревизией API `92cf68d43214`; `EndpointSlice` содержал адрес API Pod. На момент этого прогона исполняемый сценарий и манифесты соответствовали commit `fe61a235`; отдельные правки документации не меняли проверяемую конфигурацию.

## Диагностика

При неожиданном ответе сохранить вывод `task pod-security-test` и `task pod-security-status`. Разделить отказ Pod Security Admission, ошибку схемы манифеста, недоступность API server, отсутствие Ready API Pod и ошибку выполнения допустимого контейнера. Если допустимый Pod создан, оставить его до сбора состояния и событий. Не пытаться создать отрицательный Pod без `--dry-run=server`.

## Причина

Причина остановки первого запуска — противоречивые поля отрицательного манифеста, которые Kubernetes отверг до Pod Security Admission. Поэтому первоначальный отказ не подтверждает применение политики `restricted`. В исправленном манифесте единственное намеренное нарушение — доступ к host PID namespace. Повторный ответ сервера уже указал именно на отказ Pod Security Admission, а не на ошибку допустимости Pod, загрузки образа или процесса внутри контейнера.

## Восстановление

После сбора данных удалить допустимый экспериментальный Pod через `task pod-security-cleanup` и проверить основной API через `task network-smoke`. Небезопасный Pod не создаётся и в удалении не нуждается. При ошибке сохранить состояние до очистки.

## Поведение Kubernetes

Наблюдались проверка допустимости Pod до admission в первом опыте, метка namespace `enforce=restricted` и сохранённая готовность API. В повторе Pod Security Admission отклонил серверный dry-run с `hostPID=true` и допустил отдельный ограниченный Pod. Отрицательный Pod не создавался; `warn` и `audit` не заменяют режим `enforce`.

## Поведение приложения

Go API в отрицательном запросе не участвует. Его готовность и настройки Pod служат контрольным состоянием; допустимый клиент выполняет локальную команду без обращения к приложению.

## Что результат подтверждает

Первый запуск подтверждает только то, что сценарий отличил ошибку допустимости Pod от ожидаемого отказа Pod Security Admission. Предварительный повтор и повтор после commit подтверждают фактический отказ PSA для `hostPID=true`, допуск и успешное завершение отдельного ограниченного Pod, отсутствие недопустимого Pod и сохранённую доступность основного API через `Service`. Статус `Passed` относится к повтору на сценарии commit `fe61a235` в указанном локальном кластере.

## Что результат не подтверждает

Успешный повтор подтверждает только проверенное ограничение `hostPID` и допуск указанного Pod в данном namespace. Он не доказывает все правила профиля `restricted`, настройки других namespace или защиту от привилегий, выданных вне Pod Security Admission. Серверный dry-run не доказывает, что недопустимый Pod был запущен: это намеренно исключено.

## Остаточный риск

Серверный dry-run требует доступности API server и работы admission. Допустимый Pod использует уже применявшийся в стенде образ `curlimages/curl:8.16.0`; отсутствие образа или ошибка его запуска даст отдельный отказ и не должна смешиваться с результатом политики. Проверка ограничена одним правилом профиля `restricted` и одним выделенным namespace; её результат не переносится автоматически на другие конфигурации или кластеры.
