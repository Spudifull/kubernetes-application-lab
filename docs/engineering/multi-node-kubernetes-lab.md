# Запуск многонодовой лаборатории

В Actions выберите Multi-node Kubernetes и группу all либо отдельный сценарий.
Для PR в dev workflow запускается при изменении кода или конфигурации стенда.
На Linux с Docker, kind 0.33.0, kubectl 1.35.9, Helm 3.17.3 и jq можно выполнить:

```bash
export REVISION="$(git rev-parse HEAD)"
export IMAGE="lab-multinode-api:$REVISION"
docker build -f docker/api.Dockerfile --build-arg "REVISION=$REVISION" -t "$IMAGE" .
GROUP=bootstrap bash scripts/ci-multinode.sh
```

Другие значения GROUP — scheduler, maintenance, outage и network.
Диагностика в .cache/multinode/evidence содержит отфильтрованные снимки узлов,
Pod, событий и результаты HTTP. Actions хранит артефакты 14 дней. При локальном
повторе сохраняйте или очищайте прежние evidence перед следующим запуском.
При аварийной остановке процесса проверьте имя собственного кластера по run.json
и удалите его командой kind delete cluster --name с этим точным именем.
