param(
    [Parameter(Mandatory)][ValidateSet('preflight','render','install','status','sync','smoke','cleanup')][string]$Action,
    [ValidateSet('lab-dev','lab-stable')][string]$Environment = 'lab-dev',
    [string]$Revision,
    [string]$CandidatePath = 'gitops/candidate.json',
    [string]$ConfirmCluster
)
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
Set-Location $root
$cluster = 'kubernetes-application-lab-gitops'
$context = "kind-$cluster"
$config = Join-Path $root '.cache/gitops/kubeconfig'
$kubectl = Join-Path $root '.cache/tools/kubectl-1.35.9.exe'
$scope = @('--kubeconfig',$config,'--context',$context)
function Invoke-Checked([string]$Program,[string[]]$Arguments) {
    & $Program @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Program failed: $Arguments" }
}
function Kube([string[]]$Arguments) { Invoke-Checked $kubectl ($scope + $Arguments) }
function Read-Kube([string[]]$Arguments) { (Kube ($Arguments + @('-o','json'))) | ConvertFrom-Json }
function Assert-Cluster {
    if (-not (Test-Path $config)) { throw 'Dedicated GitOps kubeconfig is missing' }
    $nodes = Read-Kube @('get','nodes')
    if ($nodes.items.Count -ne 1 -or $nodes.items[0].metadata.name -ne "$cluster-control-plane") { throw 'Unexpected GitOps cluster' }
}
function Require-Tools {
    foreach ($tool in @('docker','kind','helm','git')) { Get-Command $tool -ErrorAction Stop | Out-Null }
    if (-not (Test-Path $kubectl)) { throw 'Run gitops-install to provision checksum-verified kubectl 1.35.9' }
    Invoke-Checked docker @('info','--format','{{.ServerVersion}}')
    Invoke-Checked kind @('version')
    Invoke-Checked helm @('version','--short')
}
if ($Action -eq 'render') {
    foreach ($envName in @('lab-dev','lab-stable')) {
        $profile = if ($envName -eq 'lab-dev') { 'dev' } else { 'stable' }
        Invoke-Checked helm @('lint','helm/lab-api','-f',"gitops/$profile/values.yaml")
        $yaml = Invoke-Checked helm @('template',$envName,'helm/lab-api','--namespace',$envName,'--kube-version','1.34.0','-f',"gitops/$profile/values.yaml")
        New-Item -ItemType Directory -Force .cache/gitops/rendered | Out-Null
        [IO.File]::WriteAllLines((Join-Path $root ".cache/gitops/rendered/$envName.yaml"),[string[]]$yaml)
    }
    exit 0
}
if ($Action -eq 'install') {
    New-Item -ItemType Directory -Force .cache/tools,.cache/gitops | Out-Null
    if (-not (Test-Path $kubectl)) {
        $url = 'https://dl.k8s.io/release/v1.35.9/bin/windows/amd64/kubectl.exe'
        Invoke-WebRequest $url -OutFile "$kubectl.download"
        $expected = (Invoke-WebRequest "$url.sha256").Content.Trim()
        if ((Get-FileHash "$kubectl.download" -Algorithm SHA256).Hash.ToLowerInvariant() -ne $expected) { throw 'kubectl checksum mismatch' }
        Move-Item -LiteralPath "$kubectl.download" -Destination $kubectl
    }
    Require-Tools
    $clusters = @(Invoke-Checked kind @('get','clusters'))
    if ($clusters -notcontains $cluster) {
        if (Test-Path $config) { throw 'Stale kubeconfig exists; inspect it before recreating the cluster' }
        Invoke-Checked kind @('create','cluster','--name',$cluster,'--kubeconfig',$config,'--image','kindest/node:v1.34.0@sha256:7416a61b42b1662ca6ca89f02028ac133a309a2a30ba309614e8ec94d976dc5a','--wait','180s')
    }
    Assert-Cluster
    $manifest = Join-Path $root '.cache/gitops/argo-core-v3.5.6.yaml'
    Invoke-WebRequest 'https://raw.githubusercontent.com/argoproj/argo-cd/v3.5.6/manifests/core-install.yaml' -OutFile $manifest
    if ((Get-FileHash $manifest -Algorithm SHA256).Hash.ToLowerInvariant() -ne 'd3d9d3555ad77eb4b6d27b1ec0d2af7978a91537e84d54631f983c227f0a3d31') { throw 'Argo manifest checksum mismatch' }
    foreach ($ns in @('argocd','lab-dev','lab-stable')) {
        $nsJson = Kube @('create','namespace',$ns,'--dry-run=client','-o','json')
        $nsJson | & $kubectl @scope apply -f -
        if ($LASTEXITCODE -ne 0) { throw "Namespace apply failed: $ns" }
    }
    Kube @('-n','argocd','apply','--server-side','-f',$manifest)
    Kube @('-n','argocd','rollout','status','statefulset/argocd-application-controller','--timeout=240s')
    Kube @('-n','argocd','rollout','status','deployment/argocd-repo-server','--timeout=240s')
    Kube @('-n','argocd','rollout','status','deployment/argocd-redis','--timeout=240s')
    Kube @('-n','argocd','apply','-f','gitops/argocd/project.yaml')
    $documents = (Get-Content gitops/argocd/applications.yaml -Raw) -split '(?m)^---\r?$'
    foreach ($document in $documents) {
        $appName = if ($document -match 'name: lab-dev') { 'lab-dev' } else { 'lab-stable' }
        $existing = Kube @('-n','argocd','get','application',$appName,'--ignore-not-found','-o','name')
        if (-not $existing) {
            # Bootstrap stays manual; self-heal is enabled after the first smoke.
            $document -replace '(?s)  syncPolicy:.*$', '  syncPolicy: {}' | & $kubectl @scope apply -f -
            if ($LASTEXITCODE -ne 0) { throw 'Application bootstrap failed' }
        }
    }
    exit 0
}
Require-Tools
if ($Action -eq 'preflight') {
    Invoke-Checked kind @('get','clusters')
    if (Test-Path $config) { Assert-Cluster }
    Write-Output 'Dedicated kubeconfig/context; no default context changes. Install needs capacity for Argo Core and two stateless APIs.'
    exit 0
}
Assert-Cluster
switch ($Action) {
    'status' {
        Kube @('-n','argocd','get','applications','lab-dev','lab-stable')
        foreach ($ns in @('lab-dev','lab-stable')) { Kube @('-n',$ns,'get','deployments,pods,services,endpointslices') }
    }
    'sync' {
        if ($Revision -notmatch '^[a-f0-9]{40}$') { throw 'An exact 40-character Git revision is required' }
        $branch = if ($Environment -eq 'lab-stable') { 'main' } else { 'dev' }
        Invoke-Checked git @('merge-base','--is-ancestor',$Revision,"origin/$branch")
        if ($Environment -eq 'lab-stable') { Invoke-Checked go @('run','./cmd/labctl','release-check') }
        $patch = @{operation=@{sync=@{revision=$Revision;prune=$false}}} | ConvertTo-Json -Depth 5 -Compress
        Kube @('-n','argocd','patch','application',$Environment,'--type=merge','-p',$patch)
        $deadline = [DateTime]::UtcNow.AddMinutes(4)
        do {
            Start-Sleep -Seconds 2
            $app = Read-Kube @('-n','argocd','get','application',$Environment)
            if ($app.status.operationState.phase -in @('Failed','Error') -and $app.status.operationState.syncResult.revision -eq $Revision) { throw 'Argo sync failed' }
            if ([DateTime]::UtcNow -gt $deadline) { throw 'Argo sync timed out' }
        } until ($app.status.operationState.phase -eq 'Succeeded' -and $app.status.operationState.syncResult.revision -eq $Revision -and $app.status.sync.status -eq 'Synced' -and $app.status.health.status -eq 'Healthy')
        Kube @('-n',$Environment,'rollout','status',"deployment/$Environment",'--timeout=120s')
    }
    'smoke' {
        $existing = Kube @('-n',$Environment,'get','pod','lab-smoke','--ignore-not-found','-o','name')
        if ($existing) { throw 'Diagnostic Pod lab-smoke already exists; refusing to reuse or delete it' }
        $created = $false
        try {
            Kube @('-n',$Environment,'run','lab-smoke','--image=curlimages/curl:8.16.0','--restart=Never','--command','--','sleep','600')
            $created = $true
            Kube @('-n',$Environment,'wait','--for=condition=Ready','pod/lab-smoke','--timeout=120s')
            Kube @('-n',$Environment,'exec','lab-smoke','--','curl','--fail','--silent','--show-error','--max-time','5',"http://$Environment.$Environment.svc/readyz")
            & (Join-Path $PSScriptRoot 'gitops-observe.ps1') -Environment $Environment -CandidatePath $CandidatePath -OutputPath ".cache/gitops/observations/$Environment.json" -RequireHealthy
        } finally {
            if ($created) { Kube @('-n',$Environment,'delete','pod','lab-smoke','--wait=true') }
        }
    }
    'cleanup' {
        if ($ConfirmCluster -cne $cluster) { throw "Cleanup requires -ConfirmCluster $cluster; both GitOps environments will be removed" }
        Invoke-Checked kind @('delete','cluster','--name',$cluster,'--kubeconfig',$config)
    }
}
