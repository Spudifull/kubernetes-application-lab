> Historical result from the private development history. Original identifiers are preserved; this is not a rerun of the public snapshot. Reviewed screenshots are included; raw captures remain private. See [provenance](../provenance.md). Local paths, where present, were redacted.

# Лабораторный сценарий: PostgreSQL StatefulSet

- Статус: `Passed` для запуска, прав на PGDATA, обновления release с исправленными проверками и отдельного отказа API при отсутствующем Secret
- Базовая ревизия подготовки: `b34a9242af2ef8f901b5daaf458aa4e0ba7d9d63`; исправление проверок зафиксировано в `a57b85b73593cbfe8fb365f700f54a3c37bd3e9e`, commit создан 24 октября 2026 года в 15:12:15 UTC, повтор обновления начат в 15:12:34 UTC
- Образы: `postgres:16.14-bookworm`, наблюдавшийся imageID `docker.io/library/postgres@sha256:64154d0babcb1741988719e703419af0382b19953706149f9872fbd0f438efa8`; API из чистой ревизии `df935dc5bf0823432c1b251c065c9e7965f2af36`, ссылка `kubernetes-application-lab-api:2af564735c19c657ff42fb7b4050a6a21cc5ae542cbd8eb78384e5657cac07fe`
- Среда: Windows PowerShell, основной одноузловой `kind`, Kubernetes `v1.34.0`; StorageClass `standard`, `rancher.io/local-path`, `WaitForFirstConsumer`, `Delete`
- Ресурсы: `StatefulSet/lab-postgres`, Service `lab-postgres` и `lab-postgres-headless`, PVC `data-lab-postgres-0`
- Контракт: [PostgreSQL и сохраняемое состояние](../design/postgresql-stateful-storage.md)

## Цель и исходные условия

Подтвердить создание единственной Ready-реплики, связывание PVC с PV, монтирование тома и запуск под UID 999. В кластере существует namespace `kubernetes-application-lab`; Secret заранее не хранится в Git. Исходное наличие ресурсов и состояние других release записать до опыта.

## Процедура

1. Зафиксировать `git rev-parse HEAD`, `git status --short`, `kubectl --context kind-kubernetes-application-lab version`, `task db-preflight`.
2. Выполнить `task db-helm-lint`, `task db-helm-render`; проверить отсутствие Secret в шаблоне.
3. При отсутствии Secret выполнить `task db-secret-create`, затем `task db-deploy` и `task db-status`.
4. Сохранить `kubectl --context kind-kubernetes-application-lab get pvc data-lab-postgres-0 -n kubernetes-application-lab -o yaml`, имя связанного PV, `kubectl ... describe pod lab-postgres-0`, `kubectl ... logs pod/lab-postgres-0 --timestamps` и `imageID`. Проверить UID и права монтирования через `kubectl ... exec pod/lab-postgres-0 -n kubernetes-application-lab -- id` и `... -- ls -ld /var/lib/postgresql/data /var/lib/postgresql/data/pgdata`.

## Ожидаемые признаки

PVC и PV `Bound`, Pod `1/1 Ready`, Service имеет конечную точку, каталог PGDATA доступен UID/GID процесса. Отсутствующий Secret до создания даёт отличимую диагностику и после создания исправляется без удаления PVC.

## Наблюдение, диагностика и причина

24 октября 2026 года владелец выполнил первоначальную установку PostgreSQL, затем собрал API из чистой ревизии `df935dc5bf0823432c1b251c065c9e7965f2af36` и заменил PostgreSQL Pod. В присланном выводе StatefulSet достиг `1/1 Ready`, PVC `data-lab-postgres-0` остался `Bound` с PV `pvc-08e126ef-91ad-406a-86d1-db94b3f2acbe`. Процесс работал с UID/GID 999, каталог PGDATA принадлежал `postgres` и имел права `0700`. После замены UID Pod изменился, а API снова прочитал `storage-proof=before-restart`.

Одновременно журнал показал повторяющиеся `FATAL: role "postgres" does not exist` с интервалом readinessProbe. Обе проверки запускали `pg_isready` без пользователя и базы; клиент выбирал пользователя `postgres`, хотя Secret задавал роль `lab`. Согласно [документации PostgreSQL](https://www.postgresql.org/docs/16/app-pg-isready.html), `pg_isready` может подтвердить доступность сервера при неверных параметрах подключения и оставить ошибку в журнале. Поэтому `Ready` не противоречил этим сообщениям и не доказывал успешную аутентификацию.

После commit исправления владелец повторил `task db-deploy`: PostgreSQL release перешёл в deployed revision 2, новый Pod с IP `10.244.0.108` достиг `1/1 Ready` без перезапусков. PVC остался связан с прежним PV. `printenv PGUSER PGDATABASE` вернул `lab` для обеих переменных. После явного ожидания 16 секунд журнал нового Pod содержал запуск в 15:12:36 UTC, пропуск инициализации существующего каталога и готовность принимать подключения; ошибок отсутствующей роли в этом выводе не было. Чтение через API снова вернуло `storage-proof=before-restart`. Это подтверждает исправление на показанном интервале, а не бессрочное отсутствие любых ошибок подключения.

Сообщение `chmod` для `/var/run/postgresql` осталось после исправления probes; запуск оно не остановило, сервер создал Unix socket и начал принимать подключения. Корень смонтированного тома имел права `0777`, каталог PGDATA — `0700` и владельца `postgres`. Первоначальная установка PostgreSQL выполнялась при изменённом README; последующие сборка API и проверка замены Pod относятся к чистой ревизии `df935dc5bf08`, обновление probes — к зафиксированному chart `a57b85b`. Эти результаты не означают повторную чистую установку исправленного chart на пустом томе. Доступность схемы и учётных данных подтверждена операцией API отдельно от `pg_isready`.

## Финальная регрессия базового профиля API

24 октября 2026 года в 16:21:29 UTC владелец выполнил `task deploy` без DB-профиля. Helm release `lab-api` перешёл в deployed revision 4 с образом `kubernetes-application-lab-api:1f35ec63d00add6a7c548305b83a3a78a98d89b90ec083018655d5db04a4d00f`, собранным из чистой ревизии `803c1bc2363cc9a7a1fa2170308de46d190eb8e1`. Новый Pod `lab-api-598f95898f-2gnjs` достиг `1/1 Ready` без перезапусков; прежний DB-enabled Pod в начальном снимке ещё завершался.

Затем `task db-failure-start` уменьшил PostgreSQL до нуля; ожидание `.status.replicas=0` завершилось успешно. При PostgreSQL `0/0` внутрикластерный `task smoke` получил `/version` с ревизией `803c1bc2363c`, а запрос `/readyz` через API server proxy к Service вернул `ready`. Deployment API оставался `1/1 Available`, PVC — `Bound` с прежним PV. `smoke` использует обычный DNS/Service-путь к `/version`; отдельный запрос готовности выполнен через proxy. Совокупность результатов подтверждает доступность нового базового API без работающего PostgreSQL.

В wide-снимке EndpointSlice ещё присутствовали два адреса, включая адрес старого Pod; условия отдельных конечных точек не сняты. Поэтому этот снимок не доказывает готовность обеих конечных точек или завершение сходимости сетевых правил. Ответ `/version` отдельно идентифицировал новый образ.

Блок `finally` восстановил одну реплику PostgreSQL. Итоговый `task db-status` показал `1/1 Ready`, новый `lab-postgres-0` с IP `10.244.0.116`, нулём перезапусков и прежний `Bound` PVC. Временный smoke Pod удалён. Основной API остался в базовом профиле; возвращение к операциям с БД требует `task db-api-deploy`. Финальная регрессия базового профиля имеет статус `Passed` в этих границах.

## Дополнительный опыт: отсутствующий Secret API

25 октября 2026 года в 02:22:28 по Москве (24 октября в 23:22:28 UTC) владелец установил отдельный release `lab-api-badcred` revision 1 со ссылкой на отсутствующий `lab-postgres-missing-for-probe`. До опыта release, тестовая ConfigMap и Secret отсутствовали, а запрос каталога PostgreSQL подтвердил отсутствие базы `lab_schema_probe`. Владелец создал только эту пустую базу и ConfigMap `lab-schema-probe-config`; основная база `lab` не менялась.

Pod `lab-api-badcred-5f49dbf675-q66kq` получил IP `10.244.0.117`, но контейнер остался `Waiting/CreateContainerConfigError`, `restartCount=0`. `describe` показал отсутствие Container ID и Image ID и событие `Error: secret "lab-postgres-missing-for-probe" not found`. Поэтому отказ относится к подготовке окружения kubelet, до запуска процесса API. Статус Helm `deployed` без `--wait` не означал готовность Pod.

В 02:23:16 по Москве владелец обновил только ссылку экспериментального release на существующий `lab-postgres-credentials`; release перешёл в revision 2. Новый Pod `lab-api-badcred-79b9d5885c-dbpxl` запустил образ ревизии `803c1bc2363c`, подтвердив восстановление после исправления ссылки на Secret. Его последующая неготовность имела отдельную причину `schema_missing`, поскольку тестовая база ещё не содержала таблицы; дальнейшее восстановление и очистка описаны в [отчёте о миграциях](database-migrations.md#дополнительный-опыт-api-без-таблицы). Значения учётных данных не печатались, рабочий Secret не пересоздавался.

Chart и Go-код этого опыта соответствуют зафиксированной реализации `803c1bc2363cc9a7a1fa2170308de46d190eb8e1`; при разборе HEAD был `902a8d16eeb918fcdf1ff9d2fdc473181eb81354`, отличия рабочего дерева относились к документации. Описанный опыт проверяет отсутствие Secret у API, а не предварительную проверку Secret в команде развёртывания PostgreSQL.

## Восстановление и граница

При ошибке сохранить события и журнал до повторного `task db-deploy`. Штатная очистка не удаляет PVC. Результат не подтверждает сохранность данных после потери узла kind или исполнение NetworkPolicy штатным kindnet. Пригодность резервной копии проверена отдельным сценарием. Предварительный отказ `task db-deploy` при отсутствии Secret PostgreSQL отдельно не выполнялся; фактический отказ подготовки контейнера API из-за отсутствующего Secret подтверждён дополнительным опытом выше.
