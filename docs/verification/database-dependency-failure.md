> Historical result from the private development history. Original identifiers are preserved; this is not a rerun of the public snapshot. Reviewed screenshots are included; raw captures remain private. See [provenance](../provenance.md). Local paths, where present, were redacted.

# Лабораторный сценарий: отказ зависимости и неверные учётные данные

- Статус: `Passed` для временной недоступности БД, восстановления без перезапуска API и изолированного отказа с неверным паролем; удаление экспериментального Pod подтверждено последующим снимком
- Ревизия команд и chart: `a57b85b73593cbfe8fb365f700f54a3c37bd3e9e`; API продолжал использовать ранее собранный чистый образ ревизии `df935dc5bf0823432c1b251c065c9e7965f2af36`, ссылка `kubernetes-application-lab-api:2af564735c19c657ff42fb7b4050a6a21cc5ae542cbd8eb78384e5657cac07fe`, runtime imageID `sha256:f67298fa8ec2f04e49ed71054c32bafe8ab1904b30b51a8e3a525a72c5d08013`
- PostgreSQL: `postgres:16.14-bookworm`; imageID до опыта сохранён в [отчёте запуска](postgresql-statefulset.md), отдельный imageID после восстановления в этом выводе не снят
- Среда: Windows PowerShell, одноузловой `kind`, Kubernetes `v1.34.0`, StorageClass `standard`; ресурсы: API Deployment/Service, PostgreSQL StatefulSet/Service/PVC, Secret
- Контракт: [PostgreSQL и сохраняемое состояние](../design/postgresql-stateful-storage.md)

## Цель и исходные условия

На DB-профиле проверить ограниченную реакцию на недоступность PostgreSQL и раздельность `/readyz` и `/livez`. Контрольная запись уже существует. Перед отказом записать состояние обоих workload и число рестартов API.

## Процедура: временная недоступность

1. Записать исходный ответ `task db-record-get KEY=storage-proof`, Ready API и PostgreSQL.
2. Выполнить `task db-failure-start`: StatefulSet масштабируется до нуля, PVC сохраняется.
3. Снять время и ответ запроса напрямую к API Pod, поскольку Service может не иметь Ready endpoint; проверить `/readyz` = `503`, `/livez` = `200`, время отказа операции не более двух секунд плюс накладные расходы. Записать Pod status/restarts, `EndpointSlice`, события и логи API.
4. Обязательно выполнить `task db-failure-restore`, дождаться Ready PostgreSQL, проверить `task db-record-get KEY=storage-proof` и число рестартов API.

## Процедура: неверные учётные данные

Выполнить `task db-bad-credentials-start`: команда создаёт отдельный Secret и API release `lab-api-badcred`, не меняя рабочий Secret. Затем `task db-bad-credentials-status` должен показать NotReady и причину `authentication_failed` в журнале, без значения пароля. После сохранения доказательств выполнить `task db-bad-credentials-cleanup` и снова прочитать запись из рабочего API. Не менять рабочий Secret ради этого опыта.

## Ожидаемые признаки

При общей недоступности БД API остаётся живым без restart loop, но становится NotReady; запрос завершается ограниченной ошибкой. После восстановления чтение исходной записи снова успешно. Неверные учётные данные должны давать отдельную причину в экспериментальном профиле.

## Наблюдение, диагностика и причина

24 октября 2026 года владелец выполнил временный отказ зависимости. До воздействия `/version` через прямой port-forward к API вернул `df935dc5bf08`, `/readyz` и `/livez` — `200`; контрольная запись читалась. Pod `lab-api-7b7bf6f857-kw5mj` имел UID `62c5a8df-4d1c-4ddb-bcaf-1c12b385b121`, IP `10.244.0.106` и `restartCount=0`. HEAD команд был `a57b85b`; рабочее дерево к этому моменту содержало изменения документации и `cmd/lab-client/main.go`, но развёрнутый образ API не пересобирался, а исходники выполняемой команды `cmd/labctl` не менялись.

`task db-failure-start` уменьшил StatefulSet до нуля; ожидание `.status.replicas=0` завершилось успешно. Клиент обращался напрямую к выбранному API Pod через `127.0.0.1:18081`, поэтому потеря готовых конечных точек Service не мешала измерить ответ обработчика. Получены следующие значения времени HTTP-запросов, включая накладные расходы туннеля:

| Начало серии, UTC | `/readyz` | `/livez` | `/records/storage-proof` |
|---|---|---|---|
| 15:42:11.3176259 | `503`, 505,162 мс | `200`, 4,137 мс | `503`, 5,161 мс |
| 15:42:21.8886057 | `503`, 5,134 мс | `200`, 4,758 мс | `503`, 5,265 мс |
| 15:42:31.9411380 | `503`, 4,890 мс | `200`, 4,428 мс | `503`, 5,096 мс |
| 15:42:41.9910040 | `503`, 5,592 мс | `200`, 3,892 мс | `503`, 5,289 мс |

Серверный журнал с 15:42:10.559 UTC фиксировал `database_readiness_rejected` с `reason=database_unavailable`; каждому клиентскому чтению соответствовали `database_read_failed` и ответ `503`. Во время отказа API оставался `Running`, но стал `0/1 Ready` без перезапусков. EndpointSlice `lab-api-pqxm2` содержал адрес того же Pod с `ready=false`, `serving=false`, `terminating=false`. Снимок условий не измеряет сходимость сетевых правил на узлах. PVC `data-lab-postgres-0` оставался `Bound` с прежним PV `pvc-08e126ef-91ad-406a-86d1-db94b3f2acbe`. Events PostgreSQL включали ошибку выполнения readinessProbe в уже завершившемся контейнере во время уменьшения числа реплик; это наблюдение не означает перезапуск API.

Блок `finally` выполнил `task db-failure-restore`, StatefulSet дождался готовности одной реплики. Затем исходный API Pod снова достиг `Ready`, в 15:43:17 UTC его `/readyz` вернул `200` с тем же `X-Lab-Pod`; чтение через API server proxy вернуло `storage-proof=before-restart`. Сравнение UID до и после дало `api_uid_unchanged=True`, `restartCount` остался равен нулю. Приложение восстановило доступ к зависимости без замены своего Pod или контейнера.

Временная недоступность БД в данном опыте проверена. Быстрый отказ чтения не измеряет наихудший двухсекундный тайм-аут при молчаливой потере пакетов; точное время начала и окончания полной недоступности из отдельных снимков не определяется.

Затем владелец проверил неверные учётные данные. В 15:48:06 UTC 24 октября `task db-bad-credentials-start` установил отдельный release `lab-api-badcred` revision 1 с ошибочным Secret и прежним чистым образом API `df935dc5bf08`. Pod `lab-api-badcred-554cfcb87f-2m2lr` с IP `10.244.0.110` оставался `0/1 Running` с нулём перезапусков. С 15:48:08 UTC его журнал фиксировал `database_readiness_rejected` с `reason=authentication_failed`; значения пароля в присланном выводе не было.

Через отдельный port-forward `127.0.0.1:18082` в 15:48:53 UTC владелец получил `/livez: 200`, `/readyz: 503` и `/records/storage-proof: 503`. Во всех ответах `X-Lab-Pod` указывал экспериментальный Pod. Журнал связал отказ готовности с аутентификацией и зафиксировал отказ чтения. В то же время рабочий API успешно прочитал `storage-proof=before-restart`. Таким образом, ошибочный пароль не нарушил доступ рабочего release; причина отказа отличалась от `database_unavailable` при остановленной БД.

После остановки туннеля `task db-bad-credentials-cleanup` удалил release и Secret `lab-postgres-wrong-credentials`. Последующие запросы Deployment, Service и Secret с `--ignore-not-found` не вернули объектов; экспериментальный Pod в первом снимке ещё был `Terminating` с нулём перезапусков. Перед следующим опытом на commit `803c1bc2363cc9a7a1fa2170308de46d190eb8e1` владелец повторил запрос Pod по метке `app.kubernetes.io/instance=lab-api-badcred` и получил `No resources found`, подтвердив завершение удаления. Рабочий API после очистки снова вернул прежнюю запись.

## Восстановление и граница

Восстановить реплику командой `task db-failure-restore`; PVC не удалять. Сетевой путь через Service в состоянии NotReady не измеряет тайм-аут обработчика — для него нужен прямой путь к Pod. Даже успешный опыт не доказывает устойчивость при длительной перегрузке соединений или отказе узла.
