# Проверка оркестрации публикации и release policy

Исходный main `0f204e335e0273496c46f5f9dc36294e304ffc8a`, dev `238d00f04de37fa21407055280e656a99f2455e2`. Ветка `codex/release-orchestration` создана от dev и fast-forward до main. Исторические SHA и результаты Milestone 9 сохранены.

Изменения: `ci.yml` объединяет прежние jobs без изменения имён required checks; `publish.yml` стал reusable workflow, зависит от всех проверок и получает checked run ID текущего запуска. Polling и отдельный `container.yml` удалены. `release-gate.yml` вызывает Go `release-verify`; shell `ci-release-proof.sh` и его mock-тест заменены Go-тестами. Offline `release-check` сохранён. `publish-context` выполняется до загрузки archive; `publish-check` и `publish-receipt` проверяют его identity и сохраняют proposal после push.

Локальная приёмка: Passed для Go test/vet/build, actionlint и перечисленных unit-сценариев. Проверены исторический и объединённый provenance, pagination jobs, wrong SHA/event/repository/workflow, failed/skipped/missing/duplicate jobs, неподтверждённая публикация, изменённые run ID/receipt, точность больших JSON numbers, expired/tampered artifacts, duplicate/path ZIP metadata, manifest/config mismatch, разделение GitHub и anonymous registry tokens, cancellation, размер ответа и redaction ошибок. Новые зависимости Go не добавлены.

Live read-only `release-verify` выполнен по реальным metadata Release PR №12: offline proof/API inputs, прежние CI/publishing runs, accepted identity из protected main base `50f80f4` и текущий anonymous GHCR manifest/config прошли. Никаких artifacts старого кандидата для reuse не скачивалось. Проверки GitHub Actions новой ревизии фиксируются в PR; локальная приёмка не выдаётся за запуск нового publishing job.

Новый GHCR image не публиковался, флаг публикации не включался, новый success-путь tag/push reusable workflow остаётся непроверенным реальной публикацией. Unit-тесты подтверждают guard/receipt, actionlint — структуру, CI PR — отсутствие привилегированной публикации. Образ `6f6521d` с digest `b5a4ea8d…fb44e677`, исходный promotion `b2b8ccb`, Helm values и кластеры не изменяются. Старые receipts не перезаписываются. Восстановление — revert через PR; старые контейнерные/publishing runs остаются валидными для исторического кандидата.

Три замечания среднего приоритета (разбиение kind сценария, декларативная Helm-матрица, единый интерфейс всех Go checks) отложены до роста соответствующих сценариев. Этот рефакторинг охватывает два согласованных приоритетных изменения.
