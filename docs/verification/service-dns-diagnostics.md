> Historical result from the private development history. Original identifiers are preserved; this is not a rerun of the public snapshot. Reviewed screenshots are included; raw captures remain private. See [provenance](../provenance.md). Local paths, where present, were redacted.

# Лабораторный сценарий: DNS, Service и EndpointSlice

- Статус: `Passed` — повторный прогон разделил отказ разрешения имени, отказ соединения до HTTP и ответ приложения; очистка и повторный `network-smoke` выполнены
- Ревизия сценария повторного прогона: `fe61a2359d47a2e75e304df06b1d12f712c26de8`; исполняемые файлы не изменялись после commit. Первый опыт выполнялся на ещё не зафиксированной рабочей копии конфигурации, позднее сохранённой в commit `267e1bb4e92ab022d74caa70420d5f27e537193e`
- Образ API в повторном прогоне: `kubernetes-application-lab-api:e3db845c49364367c23a8a7041b396abc059f6a229b077a270b44a3f9939e569`, `/version.revision=92cf68d43214`. В первом опыте использовался образ `kubernetes-application-lab-api:609d15549041fa3893b3b48fabb3a67490495e745be2d6bc786e0e16c007d3be`, `/version.revision=07b537514363-dirty`
- Среда: выделенный одноузловой `kind` с Kubernetes v1.34.0, контекст `kind-kubernetes-application-lab-net`, namespace `kubernetes-application-lab`; Cilium 1.20.3, ядро Docker Desktop `6.6.87.2-microsoft-standard-WSL2`; версии kind и Helm отдельно не сохранены
- Проверяемый контракт: [модель поставки](../design/deployment-model.md) и [стратегия проверки](../engineering/verification-strategy.md)

## Цель

Разделить разрешение имени, выбор Service конечных точек и фактический HTTP-ответ. Проверить полное и короткое DNS-имя `lab-api`, `ClusterIP`, порт `80`, соответствие `Service.spec.selector` меткам API Pod, `targetPort: http` порту контейнера `8080`, условия `EndpointSlice` и заголовок `X-Lab-Pod`.

## Исходные условия

Исходный кластер `kind-kubernetes-application-lab` не менять. На сетевом кластере сначала установить Cilium и подтвердить Ready узла, Cilium, CoreDNS и доступность API server. Затем установить API через тот же Helm chart из записанного локального образа. Сохранить SHA, `git status --short`, ссылку на образ, версии kind/Kubernetes/Helm/Cilium и Helm release.

## Способ создать отказ

`task network-smoke` проверяет штатный путь. `task network-diagnostics-start` создаёт три отдельных диагностических Service: headless Service для прямого обращения к IP API Pod, Service с несовпадающим selector и Service с намеренно NotReady Pod. Он также создаёт ограниченный клиентский Pod. Клиент обращается к полному и короткому имени API Service, адресу API Pod через headless DNS, отсутствующему имени, двум Service без готовых endpoints, неверному порту API Service и отсутствующему HTTP-маршруту. Основной Service не меняется.

Дождаться завершения клиента через `task network-diagnostics-wait` и сохранить вывод `task network-diagnostics-status`; при ошибке ожидания сначала сохранить диагностику, не удалять Pod. Сопоставить вывод с `Service`, Pod и `EndpointSlice` основного API и временных Service. Клиент выводит время, HTTP-заголовки, код ответа, IP и код завершения `curl`.

## Ожидаемые признаки

Оба DNS-имени API разрешаются, `/version` отвечает `200` с `X-Lab-Pod`. Отсутствующее имя даёт ошибку DNS; временные Service разрешаются, но не имеют готовых конечных точек и не доставляют HTTP; неверный порт не даёт ответ приложения. Отсутствующий маршрут даёт именно HTTP `404` от приложения с `X-Lab-Pod`. Конкретный сетевой симптом пустого Service может быть отказом соединения либо истечением срока ожидания; его не следует заранее объявлять HTTP `503`.

## Наблюдавшиеся признаки

В предоставленном выводе за 17 октября 2026 года `task network-cluster-up` создал отдельный кластер. До установки Cilium узел был `NotReady`; после установки Helm release `cilium` revision 1 получил `deployed`, узел стал `Ready`, DaemonSet Cilium — `1/1`, оператор — `1/1`, CoreDNS — `2/2`, API server ответил `ok` на `/readyz`. Helm release API revision 1 получил `deployed`, Deployment — `1/1` Ready. `task network-smoke` вернул `/version` через Service.

Диагностический клиент завершился `Succeeded`. В `18:39:48Z` полное и короткое имена Service дали HTTP `200` с `X-Lab-Pod: lab-api-84f7c5d499-dkdzb`. Обращение через headless Service также дало `200` и `remote_ip=10.244.0.153`, совпадающий с IP выбранного Pod и готовой конечной точкой обоих Service. Основной `Service/lab-api` имел `ClusterIP=10.96.240.119`, порт `80`; готовый `EndpointSlice` содержал `10.244.0.153:8080`, `ready=true`, `serving=true`, `terminating=false`.

Отсутствующее имя в `18:39:50Z` дало `curl: (28) Resolving timed out after 2001 milliseconds`, `http_status=000`; это не ответ NXDOMAIN. Service с несовпадающим selector имел `endpoints: null`, а Service с неготовым Pod — endpoint `10.244.0.33`, `ready=false`, `serving=false`. Оба обращения к ним и обращение к неверному порту `81` дали `curl_exit=7`, `http_status=000`, то есть ответа приложения не было. Неизвестный маршрут через действующий Service дал HTTP `404` с тем же `X-Lab-Pod`; его `remote_ip=10.96.240.119` совпал с `ClusterIP`.

После сохранения результата пользователь удалил три диагностических Service и два временных Pod командой `task network-diagnostics-cleanup`. Повторный `task network-smoke` на узле `Ready` успешно вернул `/version`, а `EndpointSlice` по-прежнему содержал `10.244.0.153:8080`.

В повторном прогоне 18 октября 2026 года начальный `task network-smoke` и серверная проверка диагностических манифестов прошли. `task network-diagnostics-wait` дождался `Succeeded`; `task network-diagnostics-status` показал Ready API Pod `lab-api-799899df5f-c7lp8` без перезапусков. В `19:17:42Z` полное и короткое имена `lab-api` дали HTTP `200` с `X-Lab-Pod` этого Pod; headless Service дал HTTP `200` с `remote_ip=10.244.0.107`, соответствующим готовому `EndpointSlice` (`ready=true`, `serving=true`, `terminating=false`). Основной `Service/lab-api` имел `ClusterIP=10.96.240.119`, порт `80`, а его конечная точка слушала `8080`.

Запрос к отсутствующему абсолютному имени завершился за десятки миллисекунд с `curl_exit=6`, `Could not resolve host` и `http_status=000`, в отличие от прежнего тайм-аута `curl_exit=28`. Это подтверждает отказ разрешения имени, но `curl` не показывает код DNS-ответа, поэтому именно `NXDOMAIN` не установлен. Service с пустым selector имел `endpoints: null`; Service с неготовым Pod имел endpoint `10.244.0.24` с `ready=false`, `serving=false`. Оба обращения и запрос к неверному порту `81` дали `curl_exit=7`, `http_status=000` без ответа приложения. Неизвестный маршрут через основной Service вернул HTTP `404` с `X-Lab-Pod`, а `remote_ip=10.96.240.119` совпал с его `ClusterIP`. После сохранения состояния пользователь удалил клиентский и неготовый Pod и три временных Service; последующий `task network-smoke` вновь получил `/version` и конечную точку `10.244.0.107:8080`.

## Диагностика и причина

Рабочие имена разрешались; отсутствие готовых конечных точек и неверный порт дали отказ соединения до HTTP, тогда как `404` получен от приложения. В первом опыте отсутствующее имя не разрешилось за двухсекундный срок, и причина того тайм-аута не установлена. В повторе запрос к абсолютному имени быстро завершился ошибкой разрешения; это отделяет её от сетевых отказов к существующим Service, но не устанавливает точный код ответа DNS. Оба опыта не дают основания считать CoreDNS в целом неисправным.

## Восстановление

Очистка и повторная проверка Service выполнены в обоих опытах. Команда очистки удалила только три диагностических Service и два временных Pod; API release и оба кластера остались.

## Поведение Kubernetes и приложения

Kubernetes предоставляет DNS, Service и выбранные конечные точки. Приложение отвечает на `/version`, возвращает `X-Lab-Pod` и `404` на неизвестный маршрут. Сетевой отказ до Pod не является HTTP-ответом приложения.

## Граница подтверждённого свойства и остаточный риск

Повтор на зафиксированном сценарии и образе без суффикса `-dirty` подтвердил рабочий путь DNS/Service/Pod, отказ разрешения отсутствующего имени, различие между отсутствием готовых конечных точек, неверным портом и HTTP `404` приложения, а также доступность после очистки. Статус `Passed` относится к этим наблюдаемым свойствам в одноузловом локальном кластере. Вывод `curl` не доказывает конкретный DNS RCODE (`NXDOMAIN`), а этот опыт не доказывает действие `NetworkPolicy`, Gateway или многоузловую маршрутизацию.
