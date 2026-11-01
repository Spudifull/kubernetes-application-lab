# Инженерная работа

| Задача | Документ |
|---|---|
| Поднять локальный стенд | [`local-development.md`](local-development.md) |
| Выбрать достаточную проверку | [`verification-strategy.md`](verification-strategy.md) |
| Проверить PostgreSQL, миграции и восстановление | [`database-lab.md`](database-lab.md) |
| Проверить аварию worker до Kafka offset commit | [`kafka-redelivery-lab.md`](kafka-redelivery-lab.md) |
| Проверить конечные повторы worker и DLQ | [`kafka-retry-dlq-lab.md`](kafka-retry-dlq-lab.md) |
| Проверить остановку брокера и восстановление outbox | [`kafka-broker-outage-lab.md`](kafka-broker-outage-lab.md) |
| Проверить расписание CronJob и повтор логического запуска | [`cronjob-lab.md`](cronjob-lab.md) |
| Проверить Forbid, deadline и отказ зависимости CronJob | [`cronjob-lab.md`](cronjob-lab.md#расширение-forbid-deadline-и-отказ-postgresql-для-cronjob) |
| Проверить масштабирование worker по Kafka lag через KEDA | [`keda-lab.md`](keda-lab.md) |

Команды после появления `Taskfile.yml` принадлежат ему; документация объясняет назначение и порядок применения.
