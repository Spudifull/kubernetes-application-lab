# Verification — многонодовый Kubernetes

Фактический запуск Actions и его evidence ожидаются. Базовый SHA разработки
6aa94a966d2b84c32a601d7c5bfa50b0c3d9ff34. Конфигурация закрепляет kind 0.33.0,
Kubernetes 1.34.0 и Calico 3.33.0 для отдельной сетевой группы.

| Навык | Сценарий | Автоматическая проверка | Evidence | Статус |
|---|---|---|---|---|
| Bootstrap | Три узла и Helm | Ready, две реплики на разных worker, HTTP и revision | bootstrap-*.json | Prepared |
| Scheduler | Node selector и spread | Фактический nodeName | selector-*.json, bootstrap-pods.json | Prepared |
| Affinity | Required и preferred | Placement, Pending, восстановление | affinity-*.json | Prepared |
| Taints | NoSchedule и toleration | Pending и placement | taint-*.json | Prepared |
| Maintenance | Cordon, drain, uncordon | Отказ eviction, replacement, HTTP | drain.txt, pdb-eviction.txt, drained-*.json | Prepared |
| Recovery | Потеря worker | Node Ready Unknown, новый UID, та же generation, возврат Ready | *outage*.json | Prepared |
| NetworkPolicy | Calico, deny и allow | TCP timeout/refused, HTTP 200, восстановление | default-deny.json, allowed-client.json, blocked-client.json, restored.json | Prepared |

Доступность HTTP при drain измеряется выборочно. Успешный запуск может
подтвердить отказ worker-контейнера на общем runner. HA, физический отказ,
сохранность данных и lifecycle независимых VM требуют отдельных проверок.
