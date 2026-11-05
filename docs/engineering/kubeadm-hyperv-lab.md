# Hyper-V и kubeadm

Статус Prepared. Apply и destroy выполняются после отдельных разрешений.

После уточнения владельца основной путь 10B использует Terraform, NoCloud
и Ansible. Конфигурации, команды и ограничения находятся в
[cluster/hyperv](../../cluster/hyperv/README.md), подтверждённые свойства — в
[verification 10B](../verification/kubeadm-hyperv.md). Ручная процедура ниже
сохраняется как инженерное описание этапов.

## Preflight

Воспроизводимые команды из корня репозитория:

```powershell
pwsh -NoProfile -File scripts/hyperv-preflight.ps1
pwsh -NoProfile -File scripts/hyperv-preflight.ps1 -Action plan -Prefix kal10b-demo1
```

Обе команды читают состояние системы и выводят JSON. Код 1 означает
блокирующее условие либо недоступную проверку. Plan показывает имена VM,
дисков и private switch. Инвентаризация Hyper-V требует прав управления.
Для apply дополнительно проверяются Windows Features, firmware virtualization,
конкретный ISO с checksum и выбранная схема доступа VM к пакетам.
Порог RAM включает 12 ГиБ для VM и выбранный запас хоста, по умолчанию 8 ГиБ.
Порог свободного диска 120 ГиБ включает 100 ГиБ виртуальных дисков и запас.

На Windows 11 Pro прочитайте состояние Hyper-V через
`Get-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V-All`,
`Get-CimInstance Win32_Processor`, `Get-VMHost`, `Get-Volume`,
`Get-CimInstance Win32_OperatingSystem`, `Get-NetAdapter`, `Get-VMSwitch`
и `Get-VM`. Сверьте аппаратную виртуализацию, свободную RAM и диск.
Если Hyper-V отключён, завершите preflight.

## Plan

### Оценка Terraform от 2026-11-04

Registry возвращает taliesins/hyperv 1.2.1 от 2024-02-11. Автор указывает
beta-статус и удалённое исполнение PowerShell через WinRM.
[Исходники provider](https://github.com/taliesins/terraform-provider-hyperv).

Актуальный форк Bafbi/hyperv 1.5.4 опубликован 2026-03-27. Документация
предусматривает SSH, OpenSSH Server на Windows и PowerShell как DefaultShell.
Это кандидат для отдельного пробного lifecycle VM. Проверка повторного apply,
refresh и destroy с этим provider пока не выполнена.
Внедрение Terraform требует проверки схемы закреплённой
версии, установки Terraform и согласованной настройки транспорта Windows.
[Исходники форка](https://github.com/Bafbi/terraform-provider-hyperv).

Выберите уникальный префикс, например kal10b-<id>, выделенный каталог и private
switch <prefix>-net. Зафиксируйте имена, будущие пути VHDX и действия до apply.
VM <prefix>-cp, <prefix>-w1 и <prefix>-w2 получают по 2 vCPU и 4 ГБ RAM, диски
30, 35 и 35 ГБ. Private switch изолирует VM. Подготовьте Ubuntu Server 24.04 LTS,
пакеты и OCI-образы через согласованный временный доступ либо офлайн до
отключения внешней сети. Выберите непересекающиеся подсети VM, Pod и Service.

## Apply

Проверьте отсутствие совпадений имён и путей. Создайте private switch через
`New-VMSwitch -Name <prefix>-net -SwitchType Private`. Для каждого гостя
используйте `New-VM -Generation 2 -Name <name> -MemoryStartupBytes 4GB
-NewVHDPath <owned-path> -NewVHDSizeBytes <size> -SwitchName <prefix>-net`
и `Set-VMProcessor -VMName <name> -Count 2`. Для Ubuntu Secure Boot используйте
шаблон MicrosoftUEFICertificateAuthority. Установите Ubuntu с проверенного ISO,
назначьте уникальные hostname и статические адреса. Сохраните VM ID, switch ID
и пути дисков в локальном инвентаре.

На каждой VM отключите swap, загрузите overlay и br_netfilter, сохраните их
в modules-load.d и включите net.ipv4.ip_forward и bridge-nf-call-iptables.
Установите containerd с CRI и SystemdCgroup=true, перезапустите службу.
Из pkgs.k8s.io для выбранного minor установите одинаковые закреплённые версии
kubelet, kubeadm и kubectl, сохраните версии и поставьте пакеты на hold.
На control plane выполните kubeadm init с адресом private switch и Pod CIDR
выбранного CNI. Установите совместимый закреплённый Calico, затем присоедините
два worker через выданную kubeadm join команду. Токен и admin.conf храните локально.

## Verify

В отдельном kubeconfig дождитесь Ready всех трёх Node и rollout CoreDNS. Установите
Go API через helm/lab-api с доступным из VM образом и двумя репликами. Проверьте
размещение и HTTP из Pod. Остановите одну лабораторную VM, дождитесь изменения
Ready, сравните Pod UID и Node до и после восстановления, верните VM и подтвердите
Ready и HTTP. Экспортируйте только выбранные поля API и версии инструментов.

## Destroy

Сверьте каждую VM по имени и ID с инвентарём. Проверьте абсолютные пути VHDX и
что они лежат внутри выделенного каталога и отсутствуют у других VM. Остановите
свои VM, удалите их по ID, затем удалите только перечисленные VHDX. Удалите switch
по ID после проверки отсутствия других подключений. При расхождении прервите
операцию. Инвентарь сохраните вместе с журналом действий без credentials.
