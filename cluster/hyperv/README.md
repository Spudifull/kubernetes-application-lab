# Hyper-V, Terraform и kubeadm

Статус Prepared. Схема provider Bafbi/hyperv 1.5.4 сверена с его исходниками.
Проверки на настоящих VM, повторный apply, refresh и destroy ещё ожидаются.
Локальный preflight владельца от 2026-11-04 подтвердил administrator=true,
ready=true, 22,89 ГиБ свободной RAM и 408,77 ГиБ диска. Это снимок ресурсов,
перед apply проверка повторяется.

## Предварительные условия

Windows управляет Hyper-V, дисками и Terraform. Linux-контроллер выполняет
генерацию ISO и Ansible. Можно использовать уже установленный WSL Ubuntu,
если он доступен и согласован. Установка WSL отдельным действием владельца.
Нужны Python 3.11+, xorriso, cloud-image-utils и ansible-core 2.19.3.
Terraform закреплён на 1.13.5. Установку инструментов выполняйте из официальных
источников с проверкой checksum. Виртуальное окружение Ansible создаётся
командами python3 -m venv и pip install -r ansible/requirements.txt.

Provider подключается только к 127.0.0.1. В его исходниках SSH использует
InsecureIgnoreHostKey. Конфигурация пригодна для оценки локального Hyper-V,
развёртывание на удалённом production-хосте требует другого решения проверки
подлинности SSH. Windows OpenSSH Server, ключ доступа, права Hyper-V и
PowerShell DefaultShell готовятся владельцем отдельно. Локальный SSH должен
быть ограничен loopback, без открытия порта в LAN. Для подключения Ansible
к Ubuntu проверяйте guest host key через консоль VM перед добавлением в known_hosts.
Приватные ключи, state, планы и inventory хранятся в локальном каталоге
с доступом только владельца. В Git добавляется provider lockfile после init
и проверки установленных checksum, секреты в конфигурацию provider не помещаются.

## Сеть

Terraform создаёт только private switch <prefix>-net. Kubernetes использует
10.77.10.0/24, Pod CIDR 192.168.0.0/16 и Service CIDR 10.96.0.0/12.
Перед созданием проверьте пересечение с VPN, локальными маршрутами, Docker/WSL.
VM также подключаются к существующему Default Switch через второй адаптер.
Существующий switch используется без изменения его настроек.
DHCP-адреса management-интерфейсов заносятся в локальный Ansible inventory.
Адрес kubelet и Calico закреплён за private-интерфейсом cluster0.
Один control plane и общая Windows-машина ограничивают отказоустойчивость.

## Проба provider

Из terraform/ запустите terraform init с абсолютным backend-config path,
например -backend-config="path=C:/.../kal10b-probe.tfstate".
Скопируйте lab.tfvars.example в локальный lab.tfvars вне Git, используйте
отдельный префикс kal10b-probe1, mode=probe, start_vms=false.
Создайте выделенный пустой каталог storage_root/<prefix> после сверки плана
и отсутствия коллизий имён. State пробного стенда хранится отдельно от cluster.

```powershell
terraform plan -var-file=C:/local/probe.tfvars -out=C:/local/probe.tfplan
terraform show -json C:/local/probe.tfplan | Out-File -Encoding utf8 C:/local/probe-plan.json
python check_plan.py C:/local/probe-plan.json --prefix kal10b-probe1
terraform show C:/local/probe.tfplan
```

После отдельного разрешения примените сохранённый файл командой
terraform apply C:/local/probe.tfplan. Проба создаёт выключенную VM с 1 vCPU,
1 ГиБ RAM, диском 1 ГиБ и private switch. Сохраните terraform output -json ownership,
VM ID и switch ID. Повторный terraform plan -detailed-exitcode должен вернуть 0.
Проверьте refresh-only plan. Для проверки drift измените notes только пробной VM
по её сохранённому ID, проверьте обнаружение и возврат декларативного значения.

Destroy также выполняется после отдельного разрешения. Создайте
terraform plan -destroy с тем же var-file и отдельным -out. Проверьте JSON
через check_plan.py с --destroy и тем же --prefix, просмотрите текстовый plan,
затем примените только этот сохранённый файл. Подтвердите отсутствие собственных
VM, switch и VHDX по сохранённым ID/путям. Каталог state и журнал сохраняются.
При ошибке provider сначала сопоставьте state и реальные объекты, повторный
destroy выполняется после проверки оставшихся собственных ресурсов.

## Ubuntu и три узла

После приёмки provider используйте новый prefix и отдельный Terraform state.
mode=cluster, start_vms=false. installer_iso и seed_directory указывают
будущие абсолютные Windows-пути готовых ISO. Генерация выполняется до apply.

Сначала сохраните Terraform plan с этими путями и экспортируйте его JSON.
В planned_values.outputs.bootstrap.value известны hostname, MAC и private IP.
Передайте этот plan.json генератору на Linux:

```bash
python3 cluster/hyperv/bootstrap/prepare.py \
  --iso /local/ubuntu-24.04.5-live-server-amd64.iso \
  --checksums /local/SHA256SUMS --nodes /local/cluster-plan.json \
  --public-key /local/kal10b_guest.pub --output /local/kal10b-media
```

Генератор сверяет исходный ISO по SHA256SUMS, добавляет autoinstall в GRUB,
создаёт отдельные NoCloud seed ISO для трёх MAC-наборов и manifest.json
с checksum исходного и всех полученных ISO. Встроенные учётные записи
используют SSH-ключ и заблокированный пароль. Generated installer загружается
с Secure Boot шаблоном MicrosoftUEFICertificateAuthority, это ещё нужно проверить
на реальной VM. Подготовленные носители копируются в Windows по путям plan.
Сверьте SHA-256 после копирования.

После проверки плана и отдельного разрешения примените его. VM остаются
выключенными. Проверьте аппаратную конфигурацию и подключённые носители.
Для начала установки сделайте новый plan со start_vms=true и примените его
после просмотра. Autoinstall завершает установку выключением VM.
Верните start_vms=false в конфигурации и сохраните план. Прикреплённый seed
содержит только public key. Boot order предпочитает установленный диск.
Когда все узлы установлены, задайте attach_media=false и примените просмотренный
план отключения обоих DVD. Затем повторно запустите VM просмотренным планом
со start_vms=true. Исходные ISO сохраняются для воспроизведения.

## Настройка kubeadm и приёмка

Сверьте guest host keys, получите адреса management NIC и скопируйте
ansible/inventory.example.yml в приватный локальный inventory.
Ansible проверяет Ubuntu 24.04, имена всех трёх узлов и их private IP.
Выберите актуальные patch-версии в выбранной minor-линейке по apt-cache madison,
зафиксируйте точные kubernetes_package_version, kubernetes_version и
containerd_package_version в локальном versions.yml. Конфигурация containerd
рассчитана на 1.7.x. Обновление существующего кластера требует отдельной процедуры.

```bash
ansible-playbook -i /local/inventory.yml cluster/hyperv/ansible/site.yml -e @/local/versions.yml
ansible-playbook -i /local/inventory.yml cluster/hyperv/ansible/site.yml -e @/local/versions.yml
ansible-playbook -i /local/inventory.yml cluster/hyperv/ansible/deploy.yml -e @/local/versions.yml -e @/local/api-identity.yml
```

api-identity.yml содержит проверенные api_digest и api_revision опубликованного
GHCR образа. Deploy копирует существующий Helm chart, проверяет Service DNS/HTTP,
revision и фактические Pod imageID. Выбранные поля сохраняются локально в
.cache/hyperv/evidence. Join token скрыт в выводе и живёт 15 минут. admin.conf
остаётся на control plane. Второй запуск оценивается по фактическому Ansible recap.

До Passed ещё требуется подтвердить provider lifecycle, автоматическую установку,
версии runtime, все Ready Node, HTTP, PDB/drain, отказ/возврат worker и cleanup.
Для отказа останавливается только одна VM по сохранённому ID, после восстановления
сравниваются Pod UID, nodeName и Deployment generation. Удаление всего стенда
выполняется сохранённым Terraform destroy plan с проверкой ownership.
