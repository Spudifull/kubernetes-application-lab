param(
    [ValidateSet('lab-dev','lab-stable')][string]$Environment = 'lab-dev',
    [string]$OutputPath = '.cache/gitops/observations/current.json',
    [switch]$RequireHealthy
)
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$kubectl = Join-Path $root '.cache/tools/kubectl-1.35.9.exe'
$scope = @('--kubeconfig',(Join-Path $root '.cache/gitops/kubeconfig'),'--context','kind-kubernetes-application-lab-gitops')
function Read-KubeJson([string[]]$Arguments) {
    $raw = & $kubectl @scope @Arguments -o json
    if ($LASTEXITCODE -ne 0) { throw "kubectl failed: $Arguments" }
    return ($raw | ConvertFrom-Json)
}
$nodes = Read-KubeJson @('get','nodes')
if ($nodes.items.Count -ne 1 -or $nodes.items[0].metadata.name -ne 'kubernetes-application-lab-gitops-control-plane') { throw 'Unexpected GitOps cluster' }
$candidate = Get-Content (Join-Path $root 'gitops/candidate.json') -Raw | ConvertFrom-Json
$app = Read-KubeJson @('-n','argocd','get','application',$Environment)
$deployment = Read-KubeJson @('-n',$Environment,'get','deployment',$Environment)
$pods = Read-KubeJson @('-n',$Environment,'get','pods','-l',"app.kubernetes.io/instance=$Environment")
$controller = Read-KubeJson @('-n','argocd','get','statefulset','argocd-application-controller')
$namespace = Read-KubeJson @('get','namespace',$Environment)
$response = & $kubectl @scope -n $Environment exec lab-smoke -- curl --silent --show-error --max-time 5 --write-out '\n%{http_code}' "http://$Environment.$Environment.svc/version"
$curlExit = $LASTEXITCODE
$lines = @($response)
$httpStatus = if ($curlExit -eq 0 -and $lines.Count -ge 2) { [int]$lines[-1] } else { 0 }
$version = if ($httpStatus -eq 200) { ($lines[0..($lines.Count-2)] -join "`n") | ConvertFrom-Json } else { $null }
$observation = [ordered]@{
    observed_at = [DateTime]::UtcNow.ToString('o')
    environment = $Environment
    cluster = 'kubernetes-application-lab-gitops'
    namespace_uid = $namespace.metadata.uid
    argo_image = $controller.spec.template.spec.containers[0].image
    gitops_revision = $app.status.sync.revision
    promotion_commit_sha = if ($Environment -eq 'lab-stable') { $app.status.sync.revision } else { $null }
    app_sync = $app.status.sync.status
    app_health = $app.status.health.status
    operation_phase = $app.status.operationState.phase
    source_commit_sha = $candidate.source_commit_sha
    image_repository = $candidate.image_repository
    image_digest = $candidate.image_digest
    desired_images = @($deployment.spec.template.spec.containers.image)
    deployment_generation = $deployment.metadata.generation
    progress_deadline_seconds = $deployment.spec.progressDeadlineSeconds
    deployment_conditions = @($deployment.status.conditions | Select-Object type,status,reason,lastTransitionTime)
    observed_generation = $deployment.status.observedGeneration
    replicas = $deployment.spec.replicas
    ready_replicas = [int]$deployment.status.readyReplicas
    updated_replicas = [int]$deployment.status.updatedReplicas
    http_status = $httpStatus
    curl_exit = $curlExit
    version = $version
    pods = @($pods.items | ForEach-Object {
        [ordered]@{name=$_.metadata.name;uid=$_.metadata.uid;deletion_timestamp=$_.metadata.deletionTimestamp;phase=$_.status.phase;containers=@($_.status.containerStatuses | ForEach-Object {
            [ordered]@{name=$_.name;ready=$_.ready;image_id=$_.imageID;waiting_reason=$_.state.waiting.reason;restarts=$_.restartCount}
        })}
    })
}
$directory = Split-Path $OutputPath -Parent
if ($directory) { New-Item -ItemType Directory -Path $directory -Force | Out-Null }
[IO.File]::WriteAllText([IO.Path]::GetFullPath($OutputPath),($observation | ConvertTo-Json -Depth 12)+"`n",[Text.UTF8Encoding]::new($false))
if ($RequireHealthy) {
    $expected = $candidate.image_repository+'@'+$candidate.image_digest
    $ready = @($observation.pods | Where-Object {-not $_.deletion_timestamp} | ForEach-Object {$_.containers} | Where-Object {$_.ready})
    if ($observation.app_sync -ne 'Synced' -or $observation.app_health -ne 'Healthy' -or
        $observation.ready_replicas -ne $observation.replicas -or $observation.updated_replicas -ne $observation.replicas -or
        $observation.observed_generation -ne $observation.deployment_generation -or
        $observation.http_status -ne 200 -or $version.revision -ne $candidate.source_commit_sha -or
        $ready.Count -lt 1 -or @($ready | Where-Object {$_.image_id -ne $expected}).Count -gt 0) { throw 'GitOps health or image identity assertion failed; actual observation preserved' }
}
Write-Output "Observation saved: $OutputPath; $($observation.app_sync)/$($observation.app_health), HTTP $httpStatus"
