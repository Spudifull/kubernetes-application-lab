> Historical result from the private development history. Original identifiers are preserved; this is not a rerun of the public snapshot. Reviewed screenshots are included; raw captures remain private. See [provenance](../provenance.md). Local paths, where present, were redacted.

# Лабораторный сценарий: Gateway API, HTTP-маршрутизация и TLS

- Статус: `Passed` — HTTP/HTTPS-путь, отказ без лабораторного CA и отрицательные маршруты подтверждены в описанных границах
- Ревизии прогона: образ из commit `92cf68d4321441bccc42a9772e2220d2bc4f8002` сообщает `92cf68d43214` без `-dirty`; исправленный сценарий зафиксирован в commit `32c27a32fca1eaf040a493183b6e7f43dca15ee4`
- Среда: выделенный `kind-kubernetes-application-lab-net`, Kubernetes v1.34.0, Cilium 1.20.3; Envoy Gateway v1.9.2
- Проверяемый контракт: [модель поставки](../design/deployment-model.md) и [стратегия проверки](../engineering/verification-strategy.md)

## Цель

Проверить, что один Gateway controller принимает `GatewayClass`, программирует HTTP и HTTPS listeners, направляет запрос по hostname и точному пути через `HTTPRoute` к `Service/lab-api`, а TLS завершается с проверяемой цепочкой доверия. Отдельно показать различимые ошибки маршрута и сертификата.

## Исходные условия

Основной API ответил на `task network-smoke`; экспериментальные `NetworkPolicy` уже удалены. Контекст ограничен выделенным сетевым кластером. Зафиксировать commit SHA, Helm-ревизии приложения и контроллера, версии Gateway API CRD и Envoy Gateway, состояние Cilium, API Pod, Service и `EndpointSlice`. Для всего опыта использовать только `127.0.0.1` и явный `port-forward`; внешний `LoadBalancer` не нужен. Предварительная проверка обнаружила только `GatewayClass` Envoy Gateway; это не свидетельствует о первоначальной установке с нуля.

## Способ создать нагрузку или отказ

Инфраструктурный Helm release устанавливает CRD и controller, после чего создаются `EnvoyProxy`, `GatewayClass`, `Gateway` с HTTP listener. Chart приложения добавляет `HTTPRoute` только для `api.lab.test` и точного пути `/version`; backend — собственный `Service` на порту 80. Затем отдельным действием создаются локальный CA и сертификат для `api.lab.test`, TLS Secret и HTTPS listener. Отдельные диагностические `HTTPRoute` намеренно ссылаются на отсутствующий backend и отсутствующий listener и не меняют основной маршрут.

## Ожидаемые признаки

`GatewayClass` имеет `Accepted=True`; у `Gateway` — `Accepted=True` и `Programmed=True` после готовности listeners. У основного `HTTPRoute` — `Accepted=True`, `ResolvedRefs=True`. Service плоскости данных имеет тип `ClusterIP`. HTTP и HTTPS-запросы к `api.lab.test/version` дают `200`, ответ `/version` и `X-Lab-Pod` приложения. Неизвестный hostname и несопоставленный путь не получают ответ приложения. HTTPS с явно указанным лабораторным CA проверяет сертификат и имя `api.lab.test`; без этого CA клиент отвергает сертификат, а `curl -k` не применяется.

Для диагностических маршрутов ожидаются `ResolvedRefs=False` у отсутствующего backend и `Accepted=False` у ссылки на отсутствующий listener. Фактический HTTP-ответ при обращении к ним сохраняется отдельно от условий ресурса и журналов контроллера; сам факт ошибки HTTP не устанавливает её причину.

## Наблюдавшиеся признаки

После commit `92cf68d4321441bccc42a9772e2220d2bc4f8002` собран чистый образ. `task network-deploy` развернул его, а `task network-smoke` получил `/version` с ревизией `92cf68d43214` и показал `EndpointSlice` основного Service. `task gateway-controller-install` завершился Helm-ревизией контроллера 2; его Deployment доступен. `task gateway-infra-validate` прошёл серверную проверку, затем `task gateway-infra-start` применил ресурсы. `task gateway-route-enable` перевёл release `lab-api` на ревизию 5.

`task gateway-status` показал `GatewayClass/lab-envoy` с `Accepted=True`, `Gateway/lab-gateway` с `Accepted=True` и `Programmed=True`, а его HTTP listener — с `Accepted=True`, `Programmed=True`, `ResolvedRefs=True`. У `HTTPRoute/lab-api` получены `Accepted=True` и `ResolvedRefs=True`: hostname `api.lab.test`, точный путь `/version`, backend `Service/lab-api:80`. Созданные Envoy Deployment и Pod готовы; Service плоскости данных имеет тип `ClusterIP`. На этом этапе был открыт только порт 80/TCP; HTTPS listener добавлен позднее. Эти условия сами по себе не были доказательством ответа клиента.

Через локальный `task gateway-forward` команда `task gateway-http-test` получила `200` для `api.lab.test/version`, `X-Lab-Pod: lab-api-799899df5f-c7lp8` и ревизию `92cf68d43214`. Неизвестный hostname и иной путь дали `404` без заголовка приложения. `task gateway-negative-test` получил `ResolvedRefs=False/BackendNotFound` и HTTP `500` для отсутствующего backend, `Accepted=False/NoMatchingParent` и HTTP `404` для отсутствующего listener. Журнал Envoy Gateway связал прямой ответ `500` с отсутствующим `Service/lab-api-does-not-exist`. После сохранения статусов диагностические маршруты удалены командой `task gateway-negative-cleanup`; `task network-smoke` снова прошёл.

Исходный `task gateway-tls-test` завершился на Windows Schannel с `curl: (60) schannel: the revocation status is unknown`, не получив HTTP-ответ. Ручной запрос с `--cacert .local-gateway-certs/ca.crt` и `--ssl-revoke-best-effort` затем получил HTTPS `200`, тот же `X-Lab-Pod` и ревизию; `curl` вернул код 0. Между попытками был также код 7 при неустановленном локальном соединении; после восстановления `port-forward` запрос прошёл.

После commit `32c27a32fca1eaf040a493183b6e7f43dca15ee4` пользователь повторил `task gateway-tls-test`: положительный HTTPS-запрос в 18:18:20 UTC дал `200`, `X-Lab-Pod: lab-api-799899df5f-c7lp8` и ревизию `92cf68d43214`; запрос без лабораторного CA дал код выхода 60 с `SEC_E_UNTRUSTED_ROOT`. Команда `task` завершилась успешно. Русский текст сообщения Windows Schannel отобразился с неверной кодировкой, однако код ошибки и ASCII-идентификатор сохранились.

После переноса проверок на Go CLI в commit `923695555153801027f3eb3448fe4ed767967aa9` владелец повторил HTTP, TLS и отрицательные пробы 19 октября 2026 года в 00:25–00:27 UTC. HTTP и HTTPS `/version` дали `200`, тот же `X-Lab-Pod` и ревизию приложения `92cf68d43214`; неизвестный hostname и несопоставленный путь — `404` без заголовка приложения. TLS без лабораторного CA был отвергнут с `unknown_authority`. Новые диагностические маршруты дали `ResolvedRefs=False/BackendNotFound` и HTTP `500` для отсутствующего Service, `Accepted=False/NoMatchingParent` и HTTP `404` для отсутствующего listener. Журнал Envoy Gateway связал прямой ответ `500` с отсутствующим `Service/lab-api-does-not-exist`. Сценарий Go-проб пройден на чистом commit CLI, при этом использовался прежний чистый образ API и уже открытый `kubectl port-forward`: положительная проверка новой команды Go `gateway-forward` отдельно ожидается. Очистка двух новых диагностических маршрутов в присланном выводе отсутствует.

После освобождения локальных портов новая Go-команда `gateway-forward` открыла `18080 -> 10080` и `18443 -> 10443` и обработала три HTTP-соединения на `18080`. Одновременный повтор `gateway-http-test` в 00:35:09 UTC дал `200` для `/version` и два ожидаемых `404`. Через тот же проброс `gateway-tls-test` в 00:36:51 UTC дал HTTPS `200` с `X-Lab-Pod` и ревизией приложения `92cf68d43214`, а без лабораторного CA — ожидаемый `unknown_authority`; журнал проброса показал два соединения на `18443`. После части соединений появились ошибки `wsarecv` об удалённом закрытии локального соединения; причина не установлена, а ожидаемые клиентские ответы получены. Это подтверждает оба пути нового Go-проброса в пределах данного запуска.

После сохранения диагностики владелец вызвал `gateway-negative-cleanup`, и команда завершилась без ошибки. Из-за `--ignore-not-found=true` отсутствие строк об удалении не устанавливает, существовали ли диагностические маршруты непосредственно перед вызовом; независимого запроса на их отсутствие в присланном выводе нет.

## Диагностика

При отклонении проверить `GatewayClass`, `Gateway` listeners, `HTTPRoute.status.parents[].conditions`, Service и Pod плоскости данных, `EndpointSlice` приложения и журналы Envoy Gateway. Если отсутствует `X-Lab-Pod`, отделить ответ Gateway от ответа приложения. Ошибку TLS различать с отказом маршрута: до подтверждения сертификата HTTP-ответа приложения быть не должно. Отрицательные маршруты сохраняются до получения статуса и журналов.

## Причина

Отрицательные ответы вызваны намеренно ошибочными `HTTPRoute`, что подтверждено условиями API, ответами клиента и журналом контроллера. Ошибка исходного TLS-теста относится к проверке отзыва сертификата Windows Schannel: локальный CA не содержит точки распространения CRL. Согласно [документации curl](https://curl.se/docs/manpage.html#--ssl-revoke-best-effort), `--ssl-revoke-best-effort` допускает недоступность сведений об отзыве, не выключая проверку цепочки и имени. Код 7 отдельной попытки означал отсутствие локального соединения, а не отказ сертификата.

## Восстановление

После сбора данных удалить только два диагностических `HTTPRoute` через `gateway-negative-cleanup`. Основной release API, инфраструктурный release, Gateway, Secret и оба кластера сохраняются. Прекратить локальный `port-forward` в его окне; это не изменяет кластер. Повторить `network-smoke` для независимой доступности основного Service. При неожиданном результате сначала сохранить диагностику, не удаляя контроллер или namespace.

## Поведение Kubernetes

Наблюдалось, что Gateway API controller согласовал ресурсы и создал готовую плоскость данных с `ClusterIP` Service. HTTP и HTTPS-запросы через её локальный `port-forward` дошли до API. Для отсутствующего backend Envoy Gateway создал прямой ответ `500`; маршрут к отсутствующему listener не был принят. Cilium обеспечивает сеть Pod и может отдельно ограничивать её политиками.

## Поведение приложения

Go API не меняется. Положительные HTTP и HTTPS-маршруты сохранили `X-Lab-Pod` и ответ `/version`; на отрицательных путях заголовка приложения не было. Приложение не владеет сертификатом и не завершает клиентский TLS.

## Что результат подтверждает

Чистый образ commit `92cf68d` доступен через основной Service и через HTTP/HTTPS-маршрут Gateway к имени `api.lab.test`; положительные ответы содержат `X-Lab-Pod`. Неверный hostname, путь, backend и listener различаются условиями ресурсов и наблюдаемыми HTTP-ответами. Для HTTPS положительный запрос с лабораторным CA прошёл при допущении недоступной проверки отзыва, а запрос без этого CA был отвергнут как недоверенный. Это подтверждает контракт сценария в пределах локального стенда.

## Что результат не подтверждает

Положительный HTTPS-запрос не доказывает проверку отзыва, поскольку использован `--ssl-revoke-best-effort`. Успешный опыт на одном узле не доказывает промышленную доступность, автоматическое обновление сертификата или межузловой путь.

## Остаточный риск

Лабораторный CA и ключи хранятся локально вне Git и действуют 30 дней; их утрата или истечение срока требует явного создания нового Secret и повторной проверки. `--ssl-revoke-best-effort` необходим для локального CA без точки проверки отзыва и не должен автоматически переноситься на сертификаты с доступной инфраструктурой отзыва. Правила NetworkPolicy в другом namespace не должны трактоваться как защита Gateway-пути основного namespace.
