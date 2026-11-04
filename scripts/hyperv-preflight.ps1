param(
    [ValidateSet('preflight', 'plan')]
    [string]$Action = 'preflight',
    [ValidatePattern('^kal10b-[a-z0-9]{4,16}$')]
    [string]$Prefix = 'kal10b-local',
    [string]$Root = (Join-Path $env:LOCALAPPDATA 'kubernetes-application-lab'),
    [int]$HostReserveGiB = 8
)

$ErrorActionPreference = 'Stop'
if ($HostReserveGiB -lt 4) { throw 'Host reserve must be at least 4 GiB.' }
$rootPath = [IO.Path]::GetFullPath($Root)
if ($rootPath -notmatch '^[A-Za-z]:\\') { throw 'A local absolute storage path is required.' }
$directory = Join-Path $rootPath $Prefix
$machines = @(
    [ordered]@{ name = "$Prefix-cp"; cpu = 2; memoryGiB = 4; diskGiB = 30 }
    [ordered]@{ name = "$Prefix-w1"; cpu = 2; memoryGiB = 4; diskGiB = 35 }
    [ordered]@{ name = "$Prefix-w2"; cpu = 2; memoryGiB = 4; diskGiB = 35 }
)
foreach ($machine in $machines) {
    $machine.diskPath = Join-Path $directory ($machine.name + '.vhdx')
}
$blockers = [Collections.Generic.List[string]]::new()
$computer = $null
$os = $null
$volume = $null
$existingVMs = @()
$existingSwitches = @()
try {
    $computer = Get-CimInstance Win32_ComputerSystem
    $os = Get-CimInstance Win32_OperatingSystem
    $volume = Get-Volume -DriveLetter $rootPath.Substring(0, 1)
    if (-not $computer.HypervisorPresent) { $blockers.Add('Hypervisor is not running.') }
    if ($os.FreePhysicalMemory * 1KB -lt (12 + $HostReserveGiB) * 1GB) {
        $blockers.Add('Insufficient free RAM for 12 GiB guests plus host reserve.')
    }
    if ($volume.SizeRemaining -lt 120GB) { $blockers.Add('At least 120 GiB free storage is required.') }
} catch {
    $blockers.Add('Windows resource inventory failed. ' + $_.Exception.Message)
}
try {
    $existingVMs = @(Get-VM)
    $existingSwitches = @(Get-VMSwitch)
} catch {
    $blockers.Add('Hyper-V inventory failed. ' + $_.Exception.Message)
}
foreach ($machine in $machines) {
    if ($machine.name -in $existingVMs.Name) { $blockers.Add('VM name already exists. ' + $machine.name) }
}
if ("$Prefix-net" -in $existingSwitches.Name) { $blockers.Add('Switch name already exists.') }
if (Test-Path -LiteralPath $directory) { $blockers.Add('Project storage directory already exists.') }
$result = [ordered]@{
    action = $Action
    administrator = ([Security.Principal.WindowsPrincipal]::new(
        [Security.Principal.WindowsIdentity]::GetCurrent()
    )).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    ready = $blockers.Count -eq 0
    hypervisorPresent = $computer.HypervisorPresent
    freeMemoryGiB = [math]::Round($os.FreePhysicalMemory * 1KB / 1GB, 2)
    freeStorageGiB = [math]::Round($volume.SizeRemaining / 1GB, 2)
    hostReserveGiB = $HostReserveGiB
    blockers = @($blockers.ToArray())
}
if ($Action -eq 'plan') {
    $result.directory = $directory
    $result.switch = [ordered]@{ name = "$Prefix-net"; type = 'Private' }
    $result.machines = $machines
    $result.actions = @('Create dedicated directory and private switch', 'Create three Generation 2 VM disks and VMs', 'Install Ubuntu and kubeadm after image and network preparation')
}
$result | ConvertTo-Json -Depth 6
if (-not $result.ready) { exit 1 }
