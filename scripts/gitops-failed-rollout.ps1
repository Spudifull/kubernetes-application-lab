$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$kubectl = Join-Path $root '.cache/tools/kubectl-1.35.9.exe'
$scope = @('--kubeconfig',(Join-Path $root '.cache/gitops/kubeconfig'),'--context','kind-kubernetes-application-lab-gitops')
$candidate = Get-Content (Join-Path $root 'gitops/candidate.json') -Raw | ConvertFrom-Json
$nodes = & $kubectl @scope get nodes -o json | ConvertFrom-Json
if ($LASTEXITCODE -ne 0 -or $nodes.items.Count -ne 1 -or $nodes.items[0].metadata.name -ne 'kubernetes-application-lab-gitops-control-plane') { throw 'Unexpected GitOps cluster' }
$baseline = & $kubectl @scope -n argocd get application lab-dev -o json | ConvertFrom-Json
if ($baseline.status.sync.status -ne 'Synced' -or $baseline.status.health.status -ne 'Healthy') { throw 'Development must be healthy before the fault' }
$deployment = & $kubectl @scope -n lab-dev get deployment lab-dev -o json | ConvertFrom-Json
if ($deployment.spec.progressDeadlineSeconds -ne 30) { throw 'Expected bounded development progress deadline' }
try {
    & $kubectl @scope -n argocd patch application lab-dev --type merge -p '{"spec":{"syncPolicy":{"automated":null}}}'
    if ($LASTEXITCODE -ne 0) { throw 'Cannot pause automatic recovery for observation' }
    $fault = @{spec=@{template=@{spec=@{containers=@(@{name='api';image=($candidate.image_repository+'@sha256:'+('0'*64))})}}}} | ConvertTo-Json -Depth 8 -Compress
    & $kubectl @scope -n lab-dev patch deployment lab-dev --type strategic -p $fault
    if ($LASTEXITCODE -ne 0) { throw 'Fault injection failed' }
    & $kubectl @scope -n lab-dev rollout status deployment/lab-dev --timeout=60s
    if ($LASTEXITCODE -eq 0) { throw 'Negative rollout unexpectedly succeeded' }
    & $kubectl @scope -n argocd annotate application lab-dev argocd.argoproj.io/refresh=hard --overwrite
    & $kubectl @scope -n argocd wait --for=jsonpath='{.status.health.status}'=Degraded application/lab-dev --timeout=90s
    if ($LASTEXITCODE -ne 0) { throw 'Argo CD did not report degraded rollout' }
    & (Join-Path $PSScriptRoot 'gitops-observe.ps1') -Environment lab-dev -OutputPath docs/verification/milestone9/dev-failed-rollout.json
    $observed = Get-Content docs/verification/milestone9/dev-failed-rollout.json -Raw | ConvertFrom-Json
    if ($observed.app_health -ne 'Degraded' -or @($observed.pods.containers | Where-Object {$_.waiting_reason -in @('ErrImagePull','ImagePullBackOff')}).Count -lt 1) { throw 'Missing image-pull failure observation' }
} finally {
    # Restore the known Git desired state, not an Argo history rollback under auto-sync.
    $restore = @{operation=@{sync=@{revision=$baseline.status.sync.revision;prune=$false}}} | ConvertTo-Json -Depth 6 -Compress
    & $kubectl @scope -n argocd patch application lab-dev --type merge -p $restore
    & $kubectl @scope -n argocd wait --for=jsonpath='{.status.sync.status}'=Synced application/lab-dev --timeout=120s
    & $kubectl @scope -n lab-dev rollout status deployment/lab-dev --timeout=120s
    & $kubectl @scope -n argocd wait --for=jsonpath='{.status.health.status}'=Healthy application/lab-dev --timeout=120s
    $dev = ((Get-Content (Join-Path $root 'gitops/argocd/applications.yaml') -Raw) -split '(?m)^---\r?$')[0]
    $dev | & $kubectl @scope apply -f -
    & (Join-Path $PSScriptRoot 'gitops-observe.ps1') -Environment lab-dev -OutputPath docs/verification/milestone9/dev-recovered.json -RequireHealthy
}
Write-Output 'Failed rollout diagnosed and known Git state restored; actual observations saved.'
