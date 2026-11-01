# Third-party acknowledgements

Корневая MIT относится к собственному коду. Внешние навыки не включены в снимок, установка необязательна; точные версии в skills.lock.json.

| Источник | SHA | Навыки | Лицензия upstream |
|---|---|---|---|
| https://github.com/LukasNiessen/kubernetes-skill | 34f93c1e409156958cc7c410ab1aa88ab512f10f | kubernetes | MIT, Copyright 2025 Lukas Niessen |
| https://github.com/iuliandita/skills | df2d3cfff737995d6a54ceef41d06aa5d3d137b5 | docker, observability | MIT, Copyright 2025 Iulian Dita |
| https://github.com/hashicorp/agent-skills | f706481af9b8fedb66de909f6243ad29601afa0c | terraform-style-guide, terraform-test | MPL-2.0 |

Root LICENSE этих источников проверены на указанных SHA. При самостоятельной установке сохранять LICENSE и notices: MIT не заменяет MPL-2.0. См. [безопасную ручную настройку](docs/skills.md). Go dependencies перечислены отдельно в THIRD_PARTY_DEPENDENCIES.md; их исходники не vendored. Контейнеры и Helm charts скачиваются из upstream и не перелицензируются этим проектом.
