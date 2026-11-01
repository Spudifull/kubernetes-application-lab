> Historical result from the private development history. Original identifiers are preserved; this is not a rerun of the public snapshot. Reviewed screenshots are included; raw captures remain private. See [provenance](../provenance.md). Local paths, where present, were redacted.

# Лабораторный сценарий: Metrics Server в выделенном kind

- Статус: `Passed` — закреплённый release на чистой ревизии предоставил показатели узла и двух Pod через Metrics API и `kubectl top`; чистая установка с нуля не проверялась
- Ревизия проверки: чистый commit `f50a8223fd95a73d4bf6f17813ea47ab4991aafb`; пользователь предоставил пустой вывод `git status --short`, локальная проверка после получения данных также показала чистое дерево
- Предварительный опыт: HEAD `cd781ca05e5da9747291213d71cd55d01f64c56e` с изменённым рабочим деревом; результат сохранён ниже, но этот SHA не идентифицирует установленную тогда конфигурацию
- Среда: выделенный одноузловой `kind` v0.33.0, контекст `kind-kubernetes-application-lab`, узел Kubernetes `v1.34.0`, Helm `v4.3.0+gbec5b06`; версия клиента `kubectl` в предоставленном выводе не указана
- Компонент: отдельный Helm release `metrics-server` в `kube-system`, upstream chart `3.14.0` (приложение `0.9.0`)
- Проверяемый контракт: [`../architecture/operational-architecture.md`](../architecture/operational-architecture.md)

## Цель

Получить воспроизводимый источник ресурсных показателей для последующего опыта с HPA. Разделить готовность `Deployment`, доступность агрегированного API `metrics.k8s.io` и фактические образцы CPU/памяти узла и Pod. Не принимать наличие Metrics Server за доказательство работы HPA.

## Исходные условия

Проверить чистоту Git-дерева, commit SHA и доступность именно выделенного кластера. Сохранить вывод:

```powershell
git status --short
git rev-parse HEAD
kind version
kubectl --context kind-kubernetes-application-lab version
helm version --short
kubectl --context kind-kubernetes-application-lab get nodes -o wide
kubectl --context kind-kubernetes-application-lab get pods -n kubernetes-application-lab -o wide
```

До установки, если Metrics Server ещё отсутствует, сохранить исходное состояние `APIService` и результат запроса метрик:

```powershell
kubectl --context kind-kubernetes-application-lab get apiservice v1beta1.metrics.k8s.io
kubectl --context kind-kubernetes-application-lab top nodes
```

Ошибка двух последних команд при отсутствии компонента ожидаема и сама по себе не означает неисправность приложения. Если компонент уже существует, не удалять его ради воспроизведения этого состояния: сначала записать текущий Helm release, версию и конфигурацию, затем решить, допустимо ли обновление в выделенном кластере.

## Способ подготовки и проверки

Основной исполняемый интерфейс — `Taskfile.yml`. На чистом commit выполнить последовательно:

```powershell
task metrics-server-render
task metrics-server-install
task metrics-server-status
task metrics-server-verify
```

`metrics-server-render` загружает закреплённый chart из официального Helm-репозитория и формирует ресурсы без установки. `metrics-server-install` предварительно проверяет узел выделенного кластера и устанавливает либо обновляет только release `metrics-server` в `kube-system` через явный контекст; он не меняет Helm release приложения. `metrics-server-status` сохраняет состояние release, `Deployment`, `APIService` и последние журналы. `metrics-server-verify` ждёт готовности `Deployment`, запрашивает API узлов и Pod, затем выполняет `kubectl top nodes` и `kubectl top pods --containers` в namespace приложения. Между готовностью Pod Metrics Server и первым образцом возможна задержка; если последний шаг пока не прошёл, сохранить неуспешный вывод и повторить проверку спустя интервал сбора, не меняя сразу конфигурацию.

## Ожидаемые признаки

- Helm release имеет закреплённый chart `metrics-server-3.14.0`; `Deployment/metrics-server` в `kube-system` доступен.
- У `APIService/v1beta1.metrics.k8s.io` условие `Available=True`.
- API `/apis/metrics.k8s.io/v1beta1/nodes` и соответствующий путь `pods` возвращают объекты с временем измерения, окном и значениями CPU/памяти.
- `kubectl top nodes` показывает лабораторный узел, `kubectl top pods --containers -n kubernetes-application-lab` — в том числе работающие контейнеры `lab-api`.
- При временном отсутствии показателей запрос `top` может завершиться ошибкой или не содержать свежий образец; готовность `Deployment` и наличие `APIService` по отдельности этого не исключают. До создания HPA это не приводит к какому-либо изменению числа реплик приложением или HPA.

## Наблюдавшиеся признаки

15 октября 2026 года пользователь выполнил первый прогон на изменённом рабочем дереве. Перед установкой `kubectl top nodes` вернул `Metrics API not available`. `task metrics-server-render` сформировал ресурсы chart `3.14.0`: в `Deployment` присутствовали образ `registry.k8s.io/metrics-server/metrics-server:v0.9.0`, аргумент `--kubelet-insecure-tls`, memory request `200Mi`, limit `300Mi`; `APIService` имел заполненный `caBundle`, а не `insecureSkipTLSVerify: true`.

`task metrics-server-install` увидел Ready-узел `v1.34.0` и создал release в `kube-system`: `STATUS: deployed`, revision `1`, время установки `2026-10-15 09:41:19` по локальному выводу Helm. Через 28 секунд `task metrics-server-status` показал `Deployment` `1/1`, Pod `Running` без перезапусков, но `APIService` ещё имел `Available=False`, `Reason: FailedDiscoveryCheck` и сообщение `connect: connection refused` для ClusterIP `10.96.183.255:443`. Журнал процесса показывает запуск защищённого HTTP-сервера в `06:41:26Z`; в первом выводе отдельной последующей проверки условия `Available=True` не было.

После этого `task metrics-server-verify` успешно дождался `Deployment` и получил `NodeMetricsList` для лабораторного узла с отметкой `06:41:46Z`, окном `10.03s`, CPU `94420937n`, памятью `765648Ki`. `PodMetricsList` содержал оба Pod `lab-api`: `lab-api-6fbdd5499-kgkvn` с CPU `330254n`, памятью `4896Ki` и `lab-api-6fbdd5499-kljh8` с CPU `321673n`, памятью `4976Ki`. Их окна измерения составляли `17.647s` и `14.123s`. `kubectl top nodes` показал `95m` CPU и `747Mi` памяти; `kubectl top pods --containers` показал оба контейнера `api` примерно с `1m` CPU и `4Mi` памяти. Округлённые значения `top` не следует считать теми же точными отсчётами, что значения предыдущих ответов API.

После фиксации изменений пользователь повторил `task metrics-server-install` на чистом commit `f50a8223fd95a73d4bf6f17813ea47ab4991aafb`. Команда обновила существующий release до revision `2` в `09:48:46` по местному времени Helm и завершилась с `STATUS: deployed`. Это повторное обновление, а не новая установка с нуля; Pod Metrics Server остался прежним (`metrics-server-5bcd897dd8-cqw5r`). `task metrics-server-verify` подтвердил текущую готовность `Deployment`, затем получил `NodeMetricsList` с CPU `105783549n` и памятью `696168Ki` (отметка `06:48:47Z`, окно `10.03s`) и `PodMetricsList` для тех же двух Pod `lab-api`: `308163n`/`3272Ki` и `330316n`/`3404Ki`. `kubectl top nodes` показал `106m` CPU и `679Mi` памяти; `kubectl top pods --containers` показал оба контейнера `api` с `1m` CPU и `3Mi` памяти.

Последующий `task metrics-server-status` показал Helm revision `2`, `Deployment` `1/1`, Pod `Running` без перезапусков и `APIService` `Available=True`, `Reason: Passed`, `Message: all checks passed`. В `describe` время перехода указано как `06:41:52Z`: прежний `FailedDiscoveryCheck` был кратковременным состоянием начального запуска. Чистота Git-дерева подтверждена пустым `git status --short`; версии среды — `kind v0.33.0`, Kubernetes `v1.34.0`, Helm `v4.3.0+gbec5b06`.

## Диагностика

Если `task metrics-server-install` или `task metrics-server-verify` завершается ошибкой, сначала сохранить `task metrics-server-status`, затем проверить события и состояние Pod Metrics Server:

```powershell
kubectl --context kind-kubernetes-application-lab get pods -n kube-system -l app.kubernetes.io/name=metrics-server -o wide
kubectl --context kind-kubernetes-application-lab describe deployment metrics-server -n kube-system
kubectl --context kind-kubernetes-application-lab get events -n kube-system --sort-by=.lastTimestamp
```

Различать недоступность Kubernetes API, неготовый Metrics Server, ошибку агрегированного API, отсутствие данных от kubelet и отсутствие работающих Pod приложения. Проверить, что `top` обращается к тому же контексту, что и Helm. Сохранить конкретные сообщения об ошибках; не ослаблять TLS и не увеличивать ресурсы без установленной причины.

## Причина

До установки Metrics API отсутствовал. После создания release снимок `FailedDiscoveryCheck` указывал на отказ соединения к `Service` во время запуска компонента. Контроллер затем зарегистрировал переход `APIService` в `Available=True` в `06:41:52Z`; причина кратковременного отказа согласуется с запуском сервера позднее первой проверки, но отдельного полного временного ряда соединений нет. Metrics Server получил краткосрочные ресурсные показатели от kubelet и опубликовал их через агрегированный API Kubernetes. Маршрут приложения `/metrics` здесь не участвует.

## Восстановление

Если показатели не появились, сохранить журналы и состояние, затем устранять установленную причину и повторять `task metrics-server-status` и `task metrics-server-verify`. Не удалять выделенный кластер или работающий release автоматически. При необходимости отказаться от опыта удаление release выполняется отдельным осознанным действием после сохранения доказательств.

## Поведение Kubernetes

Kubernetes принял отдельный release в `kube-system`, запустил один Pod Metrics Server и зарегистрировал `APIService`. Первое наблюдение обнаружило временную неготовность агрегированного API; затем условие перешло в `Available=True`, а запросы к ресурсам `nodes` и `pods` вернули показатели. При повторном Helm upgrade на чистом commit Pod не пересоздавался. Metrics Server — инфраструктура кластера; он не входит в chart `lab-api`.

## Поведение приложения

Оба Pod `lab-api` присутствовали в `PodMetricsList` и `kubectl top`. Этот опыт не создавал нагрузку или HPA, не изменял ресурсы и число реплик приложения и не проверял его HTTP-доступность. Маршрут `/metrics` приложения не является источником данных для Metrics Server.

## Что результат подтверждает

Чистый повтор подтверждает, что в указанном выделенном кластере Metrics Server `v0.9.0` из закреплённого chart `3.14.0` сохранил готовность после повторного Helm upgrade и предоставил ресурсные образцы через Metrics API и `kubectl top` для одного узла и двух Pod приложения в момент проверки. У `APIService` зафиксировано `Available=True`.

## Что результат не подтверждает

Чистую установку с нуля, работу или устойчивость HPA, точность показателей для профилирования, непрерывность доставки метрик, поведение при отказе Metrics Server и пригодность настроек TLS для промышленного кластера. Снимок `top` не заменяет счётчики CPU throttling из отдельного опыта.

## Остаточный риск

Профиль `cluster/kind/metrics-server-values.yaml` использует `--kubelet-insecure-tls` только потому, что сертификат kubelet в этой локальной конфигурации `kind` не даёт пригодной цепочки доверия для Metrics Server. Это ослабление соединения Metrics Server → kubelet, не рекомендация для других кластеров. Для API server → Metrics Server проверка сертификата сохранена: chart генерирует сертификат и CA bundle. Memory request `200Mi` и limit `300Mi` — начальная гипотеза для лабораторного узла, не результат измерения. После фиксации отчёта в Git потребуется новый commit документации, который сам не меняет установленную конфигурацию. Поведение будущего HPA при временном отсутствии метрик не проверялось.

## Источники закреплённого компонента

- [Официальный chart версии 3.14.0](https://github.com/kubernetes-sigs/metrics-server/blob/metrics-server-helm-chart-3.14.0/charts/metrics-server/Chart.yaml) и [его values](https://github.com/kubernetes-sigs/metrics-server/blob/metrics-server-helm-chart-3.14.0/charts/metrics-server/values.yaml).
- [Рекомендация chart по TLS для APIService](https://github.com/kubernetes-sigs/metrics-server/blob/metrics-server-helm-chart-3.14.0/charts/metrics-server/README.md).
- [Ограничения и совместимость Metrics Server](https://github.com/kubernetes-sigs/metrics-server/blob/master/README.md).
