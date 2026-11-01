# Проектирование

| Документ | Ответственность |
|---|---|
| [`application-runtime.md`](application-runtime.md) | поведение Go-приложения во время выполнения |
| [`observability-architecture.md`](observability-architecture.md) | отдельная платформа метрик, ресурсный бюджет, контракт прикладных сигналов и Target telemetry Milestone 8 |
| [`deployment-model.md`](deployment-model.md) | поставка приложения через Kubernetes и Helm |
| [`postgresql-stateful-storage.md`](postgresql-stateful-storage.md) | отдельный release PostgreSQL, PVC, необязательный профиль API и миграции |
| [`kafka-event-processing.md`](kafka-event-processing.md) | Target Milestone 7: подготовленная инфраструктура Kafka/TLS probe, будущие outbox и worker, границы надёжной обработки |

Новый проектный документ создаётся только при появлении самостоятельного контракта, который неуместно хранить в существующих файлах.
