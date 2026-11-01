# Проверка Collector и Tempo

Состояние [Passed в ограниченном объёме](../verification/tracing-infrastructure.md). Это первая итерация [контракта](../design/tracing-infrastructure.md): один диагностический span проверяет транспорт; реальные job traces [подготовлены отдельно](application-tracing-lab.md) и ещё не проверены. Все команды выполняет владелец. Новые прикладные образы и миграции не требуются.

## Подготовка

В каждом PowerShell-окне выбрать `.cache/kubectl/v1.35.9` через PATH. Guard server/client skew не отключать. Сохранить HEAD/status, observability-preflight, worker-group-status, outbox-status, dependency-metrics-verify и smoke. Worker fixed=1, Cron suspended, прежние зависимости готовы, нет активного отказа.

Выполнить `go test -count=1 -race ./cmd/labctl`, `go vet ./cmd/labctl`, `git diff --check`, `task tracing-render`. Просмотреть три YAML в evidence: namespace наблюдаемости, pinned images, ресурсы, selectors, отсутствие hostPort/NodePort/hostPath/privileges/token, PVC standard 1Gi, отсутствие новых CRD/кластерных RBAC. Render не проверяет конфигурацию процесса. Проверить Collector config выбранным образом с validate или фактическим стартом; Tempo — стартом с выбранным config. После исправлений повторить проверки и создать commit.

## Установка

Из чистого дерева сохранить SHA/status, повторить tracing-render и observability-preflight. `task tracing-install` проверяет namespace, ёмкость, StorageClass, CRD, ownership и server dry-run; устанавливает policies, Tempo, Collector. Ошибка сохраняет ресурсы, автоматической очистки нет. Pending PVC до назначения Pod ожидаем при WaitForFirstConsumer. Затем `task tracing-status`: проверить Ready Pod, Bound PVC, реальные resources и imageIDs, events и metrics. Helm deployed не доказывает OTLP.

После готовности выполнить observability-render и observability-install для datasource Tempo; это может пересоздать Grafana, но не переустанавливает Collector/Tempo и приложение. Перезапустить grafana-forward, проверить сохранение Prometheus/Alertmanager datasources. Повторить grafana-status, prometheus-targets, application-metrics-verify, dependency-metrics-verify и smoke. Новые targets Collector/Tempo должны быть UP отдельно от прежних.

## Диагностический span

В двух окнах запустить `task collector-forward` и `task tempo-forward`, держать открытыми. Максимум каждого — 12 минут. В основном окне выполнить `task tracing-probe`: один OTLP/HTTP span, запрет partial success, чтение из Tempo в пределах 40 секунд с HTTP timeout 5 секунд. Redirects/proxy отключены. У возвращённого span проверяются trace ID, span ID и имя. Locator/ответ Tempo сохраняются в `.local-observability/<UTC>-tracing-probe`.

В Grafana Explore выбрать Tempo, найти trace ID из locator; проверить service.name=labctl-trace-probe и имя collector-tempo-transport-probe. Сохранить скриншот с фактическим span и datasource health, если доступен. Наличие datasource или ID в терминале не доказывает запрос через Grafana. Повторить targets/smoke, снять 15 минут CPU/memory/RESTARTS Collector/Tempo/Grafana, состояние PVC и свободное место. Максимум выборки не равен абсолютному пику.

## При ошибке и очистка

Tracing-status собирает ответы всех трёх releases и инвентарь даже при отсутствии одного release. Просмотреть events, describe/logs конкретных Pod, config и очередь; не удалять PVC при timeout/config failure. Ready/UP не заменяют найденный span. Не расширять права и не ослаблять securityContext ради запуска. Прямой port-forward не проверяет сетевой путь от приложения.

После записи результатов закрыть collector-forward/tempo-forward через Ctrl+C. Не удалять stack/PVC ради очистки одного span: retention асинхронно удалит старые traces. Прежние workloads сохраняются. Возврат — отдельный явный rollback соответствующего release к сохранённой revision, без удаления PVC. Продолжение: [проверка подготовленного SDK и настоящего HTTP→worker пути](application-tracing-lab.md).
