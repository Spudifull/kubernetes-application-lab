> Historical result from the private development history. Original identifiers are preserved; this is not a rerun of the public snapshot. Reviewed screenshots are included; raw captures remain private. See [provenance](../provenance.md). Local paths, where present, were redacted.

# Лабораторный сценарий: данные после пересоздания PostgreSQL Pod

- Статус: `Passed` для сохранения контрольной записи после замены Pod на текущем узле
- Ревизия выполнения: `df935dc5bf0823432c1b251c065c9e7965f2af36`, чистое дерево перед сборкой; API `kubernetes-application-lab-api:2af564735c19c657ff42fb7b4050a6a21cc5ae542cbd8eb78384e5657cac07fe`, PostgreSQL `postgres:16.14-bookworm`, imageID `docker.io/library/postgres@sha256:64154d0babcb1741988719e703419af0382b19953706149f9872fbd0f438efa8`
- Среда: Windows PowerShell, одноузловой `kind`, Kubernetes `v1.34.0`, StorageClass `standard` (`rancher.io/local-path`, `Delete`)
- Ресурсы: `StatefulSet/lab-postgres`, Service `lab-postgres`, PVC `data-lab-postgres-0`, PV `pvc-08e126ef-91ad-406a-86d1-db94b3f2acbe`
- Контракт: [PostgreSQL и сохраняемое состояние](../design/postgresql-stateful-storage.md)

## Цель и исходные условия

После успешной миграции и включения API DB-профиля доказать сохранение записи именно при замене процесса Pod. Сохранить имя, UID, время создания Pod и имя PVC/PV до удаления.

## Процедура

1. `task image-build`, `task db-migrate`, `task db-api-deploy`, `task db-status`.
2. `task db-record-put KEY=storage-proof VALUE=before-restart`, затем `task db-record-get KEY=storage-proof`.
3. Зафиксировать Pod UID, PVC `volumeName` и события; выполнить `task db-pod-restart`.
4. Подтвердить новый Pod UID при прежнем PVC/PV, Ready `StatefulSet` и повторить `task db-record-get KEY=storage-proof`.
5. Сохранить журналы PostgreSQL, API и события Kubernetes до и после замены.

## Ожидаемые признаки

StatefulSet создаёт новый `lab-postgres-0`, PVC остаётся `Bound` с прежним PV, API снова читает `before-restart`. Краткий отказ во время замены допустим и должен быть измерен отдельно.

## Наблюдение, диагностика и причина

24 октября 2026 года владелец собрал образ с `REVISION=df935dc5bf08` без `-dirty`, выполнил миграционный Job `lab-db-migration-20261024150445` и обновил DB-профиль API до Helm revision 3. До замены API прочитал ранее записанное значение `storage-proof=before-restart`.

`task db-pod-restart` заменил Pod с UID `7501ae35-59b9-4913-8b22-7320bf0d3322` на `9d5b847c-62d4-4ca6-8009-5cb804fee80f`. Сравнения `pod_uid_changed`, `pvc_uid_unchanged` и `pv_unchanged` дали `True`. UID PVC остался `08e126ef-91ad-406a-86d1-db94b3f2acbe`, PV остался прежним и имел статус `Bound`. Новый Pod с IP `10.244.0.107` достиг `1/1 Ready` без перезапусков контейнера. Kubernetes Events показали остановку старого контейнера и создание нового с локально доступным образом.

В журнале нового процесса в 15:05:05 UTC зафиксированы `Skipping initialization`, штатное завершение базы в 15:05:03 UTC и готовность принимать подключения. Повторное чтение через API вернуло `{"key":"storage-proof","value":"before-restart"}`. Kubernetes восстановил Pod и подключил существующий том; PostgreSQL использовал сохранённый каталог данных, а API прочитал прежнюю запись.

При этом прежние probes создавали ошибки отсутствующей роли `postgres`; их причина и отдельный повтор исправления сохранены в [отчёте о PostgreSQL](postgresql-statefulset.md). Они не помешали чтению контрольной записи. Журнал API и точная продолжительность недоступности во время замены в этом выводе не сняты. Команда чтения использует API server proxy к Service; опыт не является отдельной проверкой HTTP-пути обычного внутрикластерного клиента через ClusterIP.

## Восстановление и граница

После опыта StatefulSet имел одну готовую реплику, PVC остался `Bound`, контрольная запись читалась. Если при повторе новая реплика не стартует, исследовать PVC mount/permissions и журнал до каких-либо действий с томом. Сценарий не удаляет PVC. Успех подтверждает сохранение при замене Pod на этом узле, не сохранение при потере узла, PV или всего kind-кластера. Длительность перерыва в обслуживании не измерена.
