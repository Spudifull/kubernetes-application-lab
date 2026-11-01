# Архитектурные решения

ADR объясняет уже принятое решение и его последствия. Он не заменяет текущий контракт.

Допустимые статусы: `Accepted`, `Superseded`.

| ADR | Решение | Статус |
|---|---|---|
| [0001](0001-separate-deployment-lab.md) | отдельный Kubernetes-стенд | Accepted |
| [0002](0002-application-delivery-boundaries.md) | границы Helm, GitOps и Terraform | Accepted |
| [0003](0003-dual-cluster-model.md) | `kind` и поздний `kubeadm` | Accepted |
| [0004](0004-failure-scenarios-as-verification.md) | сценарии отказа как доказательство | Accepted |

## Шаблон

```markdown
# ADR-NNNN: Название

- Статус: Accepted
- Дата: YYYY-MM-DD
- Supersedes / Superseded by: при наличии

## Контекст
## Решение
## Альтернативы
## Последствия
## Проверка соблюдения
```
