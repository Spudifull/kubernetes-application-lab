# Security gate и приёмка нового immutable image

Исходные ветки: main `09babeebccfdc204af7044d04e7973f72b23b731`, dev `3caf5b5044872e263ff48765f8c10fc206d9c2b6`. Ветка `codex/security-release` создана от dev и fast-forward до main. Исторический релиз source `6f6521d`, digest `b5a4ea8d…fb44e677`, promotion `b2b8ccb` остаётся отдельным наблюдением и не переписывается.

## Проверка findings

`labctl govulncheck --file <report>` читает streaming JSON protocol v1.0.0. Config source/symbol и SBOM обязательны; пустой, повреждённый, усечённый или неподдерживаемый report отклоняется. Policy соответствует symbol-level scanner: finding с function в первом trace frame блокирует независимо от severity и наличия fixed version. Module-only и package-only findings сохраняются в summary, но не выдаются за вызванный vulnerable symbol. ID дедуплицируются по максимальному уровню достижимости. Allowlist отсутствует.

Локально gate отклонил реальный сохранённый JSON OpenTelemetry 1.44.0 с called `GO-2026-6505`, несмотря на exit=0 scanner. После согласованного обновления модулей OpenTelemetry до 1.45.0 новый scan CLI прошёл: called=[], imported=[], required=[GO-2026-6860, GO-2026-6866]. Последние два относятся только к требуемому x/net, а не вызываемому коду; это не утверждение об отсутствии всех advisories. Trivy HIGH/CRITICAL остаётся отдельным блокирующим контролем образов.

Gate добавлен после JSON scan каждого из API/publisher/worker; CLI также сканируется в Go job. Raw reports и summaries сохраняются Actions artifacts даже при отказе. Обновлены связанные выбранные зависимости и каталог license/notice по фактически скачанным исходникам полного module graph.

Unit-тесты проверяют clean/module/package/symbol, duplicate init finding, неверный scan level/protocol, отсутствие config/SBOM/ID/trace, неизвестные сообщения и усечённый JSON; CLI возвращает ошибку при reachable finding, сохраняя диагностический summary. GitHub container/publishing/runtime приёмка нового кандидата до выполнения остаётся Prepared.

`gitops-observe.ps1` и smoke допускают отдельный CandidatePath: новый опубликованный proposal проверяется в dev до изменения принятого candidate в Git. Cluster/context/namespace guards сохраняются. Основной и network-кластеры, SQL/Kafka PVC/topics/offsets не входят в эту итерацию. Полный Go regression означает все обычные Go tests/race/vet/build; отдельная live SQL/Kafka/observability регрессия не заявляется.
