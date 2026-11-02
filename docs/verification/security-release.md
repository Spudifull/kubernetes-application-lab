# Security gate и приёмка нового immutable image

Исходные ветки: main `09babeebccfdc204af7044d04e7973f72b23b731`, dev `3caf5b5044872e263ff48765f8c10fc206d9c2b6`. Ветка `codex/security-release` создана от dev и fast-forward до main. Исторический релиз source `6f6521d`, digest `b5a4ea8d…fb44e677`, promotion `b2b8ccb` остаётся отдельным наблюдением и не переписывается.

## Проверка findings

`labctl govulncheck --file <report>` читает streaming JSON protocol v1.0.0. Config source/symbol и SBOM обязательны; пустой, повреждённый, усечённый или неподдерживаемый report отклоняется. Policy соответствует symbol-level scanner: finding с function в первом trace frame блокирует независимо от severity и наличия fixed version. Module-only и package-only findings сохраняются в summary, но не выдаются за вызванный vulnerable symbol. ID дедуплицируются по максимальному уровню достижимости. Allowlist отсутствует.

Локально gate отклонил реальный сохранённый JSON OpenTelemetry 1.44.0 с called `GO-2026-6505`, несмотря на exit=0 scanner. После согласованного обновления модулей OpenTelemetry до 1.45.0 новый scan CLI прошёл: called=[], imported=[], required=[GO-2026-6860, GO-2026-6866]. Последние два относятся только к требуемому x/net, а не вызываемому коду; это не утверждение об отсутствии всех advisories. Trivy HIGH/CRITICAL остаётся отдельным блокирующим контролем образов.

Gate добавлен после JSON scan каждого из API/publisher/worker; CLI также сканируется в Go job. Raw reports и summaries сохраняются Actions artifacts даже при отказе. Обновлены связанные выбранные зависимости и каталог license/notice по фактически скачанным исходникам полного module graph.

Unit-тесты проверяют clean/module/package/symbol, duplicate init finding, неверный scan level/protocol, отсутствие config/SBOM/ID/trace, неизвестные сообщения и усечённый JSON; CLI возвращает ошибку при reachable finding, сохраняя диагностический summary. Go test/race/vet/build, 21 Helm profile, actionlint и Gitleaks прошли локально и в CI.

`gitops-observe.ps1` и smoke допускают отдельный CandidatePath: новый опубликованный proposal проверяется в dev до изменения принятого candidate в Git. Cluster/context/namespace guards сохраняются. Основной и network-кластеры, SQL/Kafka PVC/topics/offsets не входят в эту итерацию. Полный Go regression означает все обычные Go tests/race/vet/build; отдельная live SQL/Kafka/observability регрессия не заявляется.

## Новый source, публикация и dev: Passed

[PR №15](https://github.com/Spudifull/kubernetes-application-lab/pull/15) прошёл CI `41511749933` и слит как source `348373135c844c06a3b53fa3d729d7ee1df05348`. [Push/dev run 41512671718](https://github.com/Spudifull/kubernetes-application-lab/actions/runs/41512671718) прошёл go/evidence/helm/container/kind и реальный `publish / publish`. Publishing job `125510901045` скачал checked archive текущего run, выполнил Go context/image checks и tag/push без build, сохранил canonical proposal. Checked и publish run IDs равны. Временный ENABLE_GHCR_PUBLISH удалён после completed/success; дальнейшие публикации выключены.

Новый registry digest: `sha256:ae4b503cd0f56643bd6467a406c25df9de7c3b71f5e91b65f5e024e1480b452a`, config ID `sha256:dab7c01e52f27fca55f42b01aa551dd083773c9159c347e2270cfc11c930ccf3`. [Anonymous registry receipt](milestone9/security-registry.json) подтверждает hashes manifest/config, revision source, non-root и linux/amd64. Config ID равен проверенному CI API image: это связь при переходе от CI OCI manifest к registry Docker schema2, а не утверждение об одинаковых manifest digest.

Derived [CI receipt](milestone9/security-ci.json) получен из Actions ZIP с проверенными SHA-256. Во всех трёх image SBOM — OpenTelemetry SDK 1.45.0; called/imported=[], HIGH/CRITICAL=0. Required-only advisory IDs сохранены. CLI gate и kind failed rollout/Service failure с recovery прошли. Это подтверждает результат нового source, а не перезаписывает старые scans.

[Baseline dev](milestone9/security-baseline-dev.json) и [baseline stable](milestone9/security-baseline-stable.json) сняты до обновления. [PR №16](https://github.com/Spudifull/kubernetes-application-lab/pull/16) прошёл CI `41513934883`, слит как GitOps revision `5193107f14f99fb931be7b3ec2ca6c7b74f206f4`. После exact sync и smoke с опубликованным proposal [новый dev proof](milestone9/security-dev.json) показал Synced/Healthy, observed generation=current, 1/1 ready/updated, HTTP 200, `/version` source `3483731` и фактический Pod imageID нового digest. Диагностический Pod удалён. Новый Pod UID отличается от baseline.

Candidate и stable profile подготовлены с тем же digest после dev proof. Promotion SHA до фактического main merge — null. Stable promotion и повторное сравнение двух окружений остаются Prepared до выполнения Release PR/manual sync; в этот момент stable ещё использует прежний image.
