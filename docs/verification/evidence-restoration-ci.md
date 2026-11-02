# Восстановление evidence и подготовка CI

## Изменённые файлы

Полный перечень рабочего изменения (`M` — изменён, `D` — удалён, `??` — добавлен, ещё не закоммичен):

```text
M AGENTS.md
M CONTRIBUTING.md
D MANIFEST.md
M README.md
D docs/engineering/contributing-rules.md
M docs/process/documentation-governance.md
M docs/provenance.md
D docs/publication.md
M docs/roadmap.md
M docs/skills.md
M docs/verification/README.md
M docs/verification/alerting.md
M docs/verification/api-unavailable-alert.md
M docs/verification/application-logging.md
M docs/verification/application-tracing.md
M docs/verification/collector-outage.md
M docs/verification/dlq-alert.md
M docs/verification/grafana-dashboards.md
M docs/verification/postgres-unavailable-alert.md
M docs/verification/worker-crash-observability.md
M docs/verification/worker-lag-alerts.md
?? .github/workflows/ci.yml
?? .gitleaks.toml
?? .gitleaksignore
?? cmd/labctl/evidence_test.go
?? docs/design/ci-cd-gitops.md
?? docs/engineering/ci-cd-gitops-lab.md
?? docs/verification/evidence-restoration-ci.md
?? docs/verification/evidence/alerting-outbox-cycle-2026-11-01.json
?? docs/verification/evidence/alerting-publisher-recovery-2026-11-01.json
?? docs/verification/evidence/alerting-publisher-trace-2026-11-01.json
?? docs/verification/evidence/api-unavailable-partial-2026-11-01.json
?? docs/verification/evidence/api-unavailable-recovery-2026-11-01.json
?? docs/verification/evidence/application-logging-events-2026-11-01.json
?? docs/verification/evidence/application-logging-resources-2026-11-01.json
?? docs/verification/evidence/application-logging-samples-2026-11-01.txt
?? docs/verification/evidence/application-tracing-dlq-2026-11-01.json
?? docs/verification/evidence/application-tracing-resources-2026-11-01.json
?? docs/verification/evidence/collector-outage-2026-11-01.json
?? docs/verification/evidence/collector-recovery-trace-2026-11-01.json
?? docs/verification/evidence/dlq-alert-2026-11-01.json
?? docs/verification/evidence/final-dlq-correlation-2026-11-01.json
?? docs/verification/evidence/final-dlq-trace-2026-11-01.json
?? docs/verification/evidence/milestone8-final-checks-2026-11-01.json
?? docs/verification/evidence/postgres-unavailable-alert-2026-11-01.json
?? docs/verification/evidence/worker-crash-counter-2026-11-01.json
?? docs/verification/evidence/worker-crash-partial-2026-11-01.json
?? docs/verification/evidence/worker-crash-recovery-2026-11-01.json
?? docs/verification/evidence/worker-crash-trace-2026-11-01.json
?? docs/verification/evidence/worker-lag-alerts-2026-11-01.json
?? scripts/ci-helm.sh
```


Дата: 2 ноября 2026 года. Исходный HEAD: `3175c49618f4d811d73aedbe8ea28a34c7cc902a`; проверки выполнялись на рабочем дереве с изменениями, нового commit нет.

## Восстановление: локальная проверка пройдена

Источник — read-only дерево `a8cf93bcc3c4a6316550a58e009d3406f80cd599`. Найдены и индивидуально проверены 22 файла: 21 JSON и 1 TXT, около 2,12 MB. Git blobs рабочего источника совпали с историческим деревом; исходное дерево осталось чистым. [Каталог решений и контрольных сумм](README.md#первичные-доказательства-milestone-8): 20 Original, 2 Sanitized, 0 Derived, 0 Private Only. Исключённых файлов из этого набора нет; attachment dumps не входят в него.

В `api-unavailable-partial` проверены вхождения token/password: `automountServiceAccountToken=false`, ссылки `secretKeyRef` и имя переменной `LAB_DB_PASSWORD`, без значения credentials. Сохранён статус тайм-аута первоначального наблюдателя. Адреса в архивах относятся к лабораторным Service/Pod/loopback. Trace/job/event ID и offsets оставлены. Пользовательский путь удалён только из prompts двух файлов.

Двадцать оригиналов сравнены по SHA-256 с источниками. Для двух Sanitized-файлов исходный JSON после объявленной замены сравнен с публичным JSON целиком: все значения, кроме префикса пути, совпали. Это проверка исторического архива, без повторного запуска Kubernetes-экспериментов.

Добавлены `TestHistoricalEvidence`, `TestHistoricalAlertFingerprint` и `TestMarkdownLocalLinks`: JSON syntax, 22 уникальных имени, индекс и SHA-256, локальные Markdown targets, запрещённые пути и признаки credentials. Смысловые проверки подтверждают duplicates 2→3, succeeded/effects=1, три partition с committed=end и lag=0, сохранение частичного отказа API, Pending/Firing, восстановление inactive и одинаковый fingerprint уведомлений firing/resolved outbox. Полная семантическая перепроверка всех spans/alert cycles автоматическим тестом пока не реализована; их значения защищены контрольными суммами архивов.

Gitleaks v8.24.3: пять находок в evidence разобраны как три image identity и два SHA-256 диагностических ключей; точные значения перечислены в `.gitleaks.toml`. При сканировании всего снимка дополнительно разобраны 34 срабатывания: исторические image identity и `curlimages/curl:8.16.0`, ошибочно распознанный как curl authentication. Точные fingerprints закреплены в `.gitleaksignore`; полные redacted-отчёты остаются вне Git. Эти исключения не подтверждают отсутствие неизвестных типов секретов.

## Локальные команды

В Windows, Go 1.26.5:

```sh
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/...
go test ./cmd/labctl -run 'TestHistorical|TestMarkdownLocalLinks' -count=1
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7
go run github.com/zricethezav/gitleaks/v8@v8.24.3 dir . --config .gitleaks.toml --redact --no-banner
git diff --check
```

Go test/race/vet/build прошли вне sandbox. Первоначальные test/race внутри sandbox были неуспешны из-за запрета локальных HTTP sockets и rename временного архива; эти ошибки не скрываются и не считаются дефектом проверяемого Kubernetes-сценария.

Helm 4.3.0: lint и template прошли для 21 сочетания chart/profile, включая специальные namespace и image tag alerting. В CI закреплён Helm 3.17.3; его исполнение в Linux runner ещё предстоит. `scripts/ci-helm.sh` повторяет эту матрицу без обращения к кластеру. Существующие Go-тесты контрактов Helm входят в полный test.

## GitHub Actions: Prepared

Создан `.github/workflows/ci.yml`: PR/push main и ручной запуск, read-only contents, отключение сохранения checkout credentials, SHA-pinned actions, timeout и concurrency. Jobs: Go test/race/vet/build/formatting; evidence/links/secret scan/actionlint; Helm lint/render. YAML проверен actionlint 1.7.7 локально.

Workflow run ID, runner execution, run URL и новый Git SHA отсутствуют: commit/push требуют явного разрешения владельца. CI не имеет статуса Passed. Контейнерная сборка, security scan зависимостей/образов, GHCR digest, kind smoke/negative scenario, Argo CD, sync, drift/self-heal и recovery в этом изменении ещё не выполнены. Kubernetes version, Argo CD version и Pod imageID для Milestone 9 отсутствуют. Исторические кластеры и PVC не затрагивались.

Итерация 0 выполнена; итерация 1 проверена локально с указанными ограничениями семантического покрытия; итерация 2 Prepared до первого GitHub run. Итерации 3–8 остаются незавершёнными. Milestone 9 не Done. Следующее действие — разрешённые владельцем commit/push и первый фактический run CI, затем контейнерная итерация.
