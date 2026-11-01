# Документация Kubernetes Application Lab

Начните с [`overview.md`](overview.md). Он объясняет цель стенда, границу между приложением и платформой и порядок наращивания сложности.

## Куда идти дальше

| Вопрос | Каноническое место |
|---|---|
| Как связаны крупные части стенда? | [`architecture/`](architecture/) |
| Как должно вести себя приложение? | [`design/application-runtime.md`](design/application-runtime.md) |
| Как организована поставка в Kubernetes? | [`design/deployment-model.md`](design/deployment-model.md) |
| Как работать локально? | [`engineering/local-development.md`](engineering/local-development.md) |
| Как проверять изменения? | [`engineering/verification-strategy.md`](engineering/verification-strategy.md) |
| Чем подтверждается эксплуатационное свойство? | [`verification/`](verification/) |
| Как устроен рабочий процесс агента? | [`process/`](process/) |
| Почему принято архитектурное решение? | [`adr/`](adr/) |
| Что делать дальше? | [`roadmap.md`](roadmap.md) |
| Какие навыки агента используются? | [`skills.md`](skills.md) |

## Иерархия источников истины

1. Код и исполняемая конфигурация определяют фактическое состояние `Current`.
2. `architecture/` и `design/` описывают действующие и явно отмеченные целевые контракты.
3. `verification/` хранит доказательства, но не владеет архитектурными правилами.
4. ADR объясняют причины решений и не заменяют текущую спецификацию.
5. `roadmap.md` описывает незавершённую работу.
6. `AGENTS.md` и `.agents/skills/` задают рабочие правила для агента.

Правила размещения знания и значения `Current`/`Target` находятся в [`process/documentation-governance.md`](process/documentation-governance.md).
