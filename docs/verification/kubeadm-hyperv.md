# Verification Hyper-V и kubeadm

Статус Prepared. Владелец выполнил локальный preflight 2026-11-04 и предоставил
JSON с administrator=true, ready=true и пустым blockers. Измерено 22,89 ГиБ
свободной RAM и 408,77 ГиБ диска. Команда планирует kal10b-demo1 и три VM,
пока подтверждена только готовность хоста к следующей проверке.

| Проверка | Статус |
|---|---|
| Ресурсы и доступ к Hyper-V из окна владельца | Passed |
| Schema review Bafbi/hyperv 1.5.4 | Prepared |
| terraform init/validate и lockfile | Prepared |
| Пробный create, no-op, drift, destroy | Prepared |
| Автоматическая установка Ubuntu | Prepared |
| Повторный Ansible apply | Prepared |
| kubeadm, три Ready Node и CoreDNS | Prepared |
| Helm, HTTP и фактический digest | Prepared |
| PDB/drain, отказ worker и восстановление | Prepared |
| Удаление стенда по ownership | Prepared |

Источники схемы provider закреплены тегом v1.5.4. SSH provider отключает проверку
host key, Terraform ограничен loopback. Перед удалённым использованием этот
механизм должен быть заменён или исправлен. Лаборатория с одним control plane
не подтверждает HA и физическую независимость узлов.

Процедура и ограничения описаны в [инструкции](../../cluster/hyperv/README.md).

## Локальная подготовка конфигураций

По уточнению владельца от 2026-11-04 подготовлены только файлы.
Python AST, разбор четырёх YAML-файлов и git diff --check прошли.
В тесте с заглушками xorriso/cloud-localds генератор прочитал Terraform
planned_values с BOM, создал три конфигурации NoCloud, добавил autoinstall
до разделителя GRUB, обновил media checksum и сформировал manifest.
Проверка plan приняла создание объектов выбранного префикса и отклонила
чужой путь VHDX и replacement. Это проверки логики с тестовыми данными.

Terraform, provider plugin, Ansible и средства ISO не исполнялись.
В текущем процессе Terraform/Ansible отсутствуют, исходящие запросы загрузки
не проходят DNS. init, fmt, validate, lockfile и ansible --syntax-check
перенесены на подготовленный Linux/Windows controller. Запросы повышения
прав и применение конфигураций не выполнялись.

Файлы реализации:

- cluster/hyperv/terraform/versions.tf
- cluster/hyperv/terraform/variables.tf
- cluster/hyperv/terraform/main.tf
- cluster/hyperv/terraform/outputs.tf
- cluster/hyperv/terraform/lab.tfvars.example
- cluster/hyperv/terraform/check_plan.py
- cluster/hyperv/bootstrap/prepare.py
- cluster/hyperv/ansible/requirements.txt
- cluster/hyperv/ansible/inventory.example.yml
- cluster/hyperv/ansible/group_vars/all.yml
- cluster/hyperv/ansible/site.yml
- cluster/hyperv/ansible/deploy.yml
- cluster/hyperv/README.md
- scripts/check-hyperv-config.sh
- scripts/hyperv-preflight.ps1
- scripts/download-ubuntu-iso.ps1
- Taskfile.yml
- .gitignore
- docs/engineering/kubeadm-hyperv-lab.md
- docs/verification/kubeadm-hyperv.md
