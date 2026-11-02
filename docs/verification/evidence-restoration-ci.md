# Восстановление evidence и подготовка CI

## Зафиксированные изменения

Дата проверки: 2 ноября 2026 года. Исходный HEAD до восстановления: `3175c49618f4d811d73aedbe8ea28a34c7cc902a`. Первичные локальные проверки выполнялись на рабочем дереве с изменениями; затем результат закоммичен и отправлен в `main`:

- `dff4e4b` — «Восстановлены доказательства и подготовлен CI»: документация, 22 архива, проверки, workflow и `.gitattributes`.
- `4567493` — «Исправлена версия Kubernetes в проверках Helm».
- `a791112` — «Сохранены исходные окончания строк доказательств».
- `5bd57b0` — «Исправлена проверка форматирования в CI»; успешный run описан ниже.
- `d76c71f` — «Зафиксирован успешный запуск CI».

Удалены `MANIFEST.md`, `docs/publication.md`, `docs/engineering/contributing-rules.md`; инженерные правила перенесены в `CONTRIBUTING.md`. Состав архивов, решения об очистке и SHA-256 перечислены в [индексе](README.md#первичные-доказательства-milestone-8). Полный состав каждого изменения хранится в Git.

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

Helm 4.3.0: lint и template прошли локально для 21 сочетания chart/profile, включая специальные namespace и image tag alerting. В CI Helm 3.17.3 выполнил ту же матрицу через `scripts/ci-helm.sh`, с явной Kubernetes 1.34.0 для формирования YAML и без обращения к кластеру. Существующие Go-тесты контрактов Helm входят в полный test.

## GitHub Actions: Passed для быстрого CI

[Успешный run 41389729014](https://github.com/Spudifull/kubernetes-application-lab/actions/runs/41389729014), Git SHA `5bd57b0c47dfbcf4ac3c2629b8c66b3ba2d10a67`, 2 ноября 2026 года. Все jobs завершены success: Go `125098242849`, evidence `125098242897`, Helm `125098242652`. Go job выполнил formatting, test, race, vet и build; evidence job — исторические assertions, Markdown links, полный Gitleaks scan и actionlint с ShellCheck. Helm job выполнил lint/render всей матрицы.

Runner: GitHub-hosted ubuntu-24.04, Ubuntu 24.04.5 LTS, image `20261025.353.1`, runner `2.338.2`. Go 1.26.0, Helm 3.17.3. Contents read подтверждён журналом. Эта приёмка относится к указанному SHA; последующее обновление отчёта не является повторной проверкой другого SHA.

### Первый фактический запуск

Commit `dff4e4bc616ff94ebf96b4a490b77ef30ca7144d` («Восстановлены доказательства и подготовлен CI») отправлен в main с разрешения владельца. [Run 41389104193](https://github.com/Spudifull/kubernetes-application-lab/actions/runs/41389104193): Helm job `125096357445` завершился ошибкой. Helm 3.17.3 при template использовал условную Kubernetes v1.32.0; chart observability требует >=1.34.0-0. В `scripts/ci-helm.sh` явно установлен `--kube-version 1.34.0` для lint/template, соответствующий целевому kind node из Taskfile. Это версия для формирования YAML; реальный Kubernetes в быстром CI не запускался. Повторный run требуется для приёмки.

Добавлен `.gitattributes`: evidence сохраняет исходные bytes, включая пробелы в TXT; shell-скрипты имеют LF. Стандартный staged whitespace check первоначально обнаружил пробелы в оригинальном TXT; они сохранены как часть исторического источника, для этого файла настроено узкое исключение whitespace.

Evidence job `125096357382` первого run также обнаружил несовпадение SHA-256 TXT: первоначальный `git add` нормализовал CRLF в LF, а добавление `.gitattributes` не перечитало уже индексированный файл. Исправлено явным `git add --renormalize docs/verification/evidence`: сохранены bytes файла из исходного Windows checkout, по которым зафиксирована публичная SHA-256. Исторический текст и измерения не менялись. Второй commit `4567493` содержит исправление Helm; исправление index TXT выполнено следующим коммитом. Приёмка требует успешного нового run.

[Run 41389381992](https://github.com/Spudifull/kubernetes-application-lab/actions/runs/41389381992), SHA `a7911123ddd4e7d6b68176a7ee1b4b6243e2abb2`: Helm успешно выполнен; evidence tests и полный secret scan прошли. Actionlint с доступным на Linux ShellCheck обнаружил SC2046 в formatting command (локально ShellCheck отсутствовал). Исправлено через NUL-separated `git ls-files -z` и `xargs -0`, без отключения ShellCheck.

Создан `.github/workflows/ci.yml`: PR/push main и ручной запуск, read-only contents, отключение сохранения checkout credentials, SHA-pinned actions, timeout и concurrency. Jobs: Go test/race/vet/build/formatting; evidence/links/secret scan/actionlint; Helm lint/render. YAML проверен actionlint 1.7.7 локально.

Контейнерная сборка, security scan зависимостей/образов, GHCR digest, kind smoke/negative scenario, Argo CD, sync, drift/self-heal и recovery в этом изменении ещё не выполнены. Runtime Kubernetes version, Argo CD version и Pod imageID для Milestone 9 отсутствуют. Исторические кластеры и PVC не затрагивались. Заголовки всех новых коммитов написаны на русском языке.

Итерация 0 выполнена; итерация 1 проверена локально и в CI с указанными ограничениями семантического покрытия; итерация 2 Passed на `5bd57b0` после сохранённых выше отказов и исправлений. Итерации 3–8 остаются незавершёнными. Milestone 9 не Done. Следующее действие — контейнерная итерация.
