# Hyper-V и kubeadm

Статус Prepared. Apply и destroy выполняются после отдельных разрешений.

## Preflight

На Windows 11 Pro прочитайте состояние Hyper-V через
`Get-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V-All`,
`Get-CimInstance Win32_Processor`, `Get-VMHost`, `Get-Volume`,
`Get-CimInstance Win32_OperatingSystem`, `Get-NetAdapter`, `Get-VMSwitch`
и `Get-VM`. Сверьте аппаратную виртуализацию, свободную RAM и диск.
Если Hyper-V отключён, завершите preflight.

## Plan

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
