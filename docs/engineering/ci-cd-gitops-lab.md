# Проверка CI/CD и GitOps

Быстрый CI [проверен](../verification/evidence-restoration-ci.md); container/kind, GHCR и оба GitOps-окружения подтверждены [runtime-отчётом](../verification/gitops-development.md). Архитектурные границы — в [контракте](../design/ci-cd-gitops.md).

Перед отправкой изменений выполнить Go test/race/vet/build, evidence tests, actionlint и `bash scripts/ci-helm.sh`. Последняя команда использует только локальное формирование YAML и не меняет кластер. Для Windows доступны эквивалентные `helm lint` / `helm template`; установленные версии фиксируются в отчёте.

После разрешённых владельцем commit/push дождаться jobs workflow CI в GitHub Actions. Сохранить run URL/ID, точный Git SHA, runner и результаты каждого job. При отказе сохранить диагностику и повторить после исправления. До этого workflow остаётся Prepared.

Контейнерные jobs общего workflow `ci.yml` выполняет Buildx, govulncheck, Trivy для API/publisher/worker и передаёт проверенный API archive в отдельный kind job. `scripts/ci-kind.sh` создаёт исключительно свой временный кластер `lab-ci-*`, использует отдельный kubeconfig, проверяет Ready EndpointSlice, `/readyz` и `/version` из client Pod через Service DNS. Неисправный rollout должен дать ProgressDeadlineExceeded/ErrImageNeverPull, затем восстановиться; дополнительно проверяется отказ Service selector и восстановление. Ресурсы, события и логи сохраняются перед удалением своего временного кластера. Команды: `task ci-container` для локального API; для Linux `IMAGE=lab-ci-api:<revision> REVISION=<revision> task ci-kind`. Локальные образы имеют dirty revision до фиксации исходников.

GHCR publication и оба Argo runtime [проверены](../verification/gitops-development.md) на одном digest. Используется отдельный kind `kubernetes-application-lab-gitops`, собственный `.cache/gitops/kubeconfig`, kubectl 1.35.9 и Argo CD Core 3.5.6. Application lab-stable впервые создана и вручную синхронизирована после Release PR №7; development использует self-heal. Основные кластеры не используются. Release gate стал required main после первого successful run на Release PR.

Для новой публикации включить `ENABLE_GHCR_PUBLISH=true` и отправить проверяемое изменение в dev. Общий `ci.yml` вызывает reusable `publish.yml` через `needs` после успеха go/evidence/helm/container/kind; run ID передаётся явно и совпадает с текущим. Самостоятельного push-trigger у publish больше нет. После success удалить временный флаг. Публикуется загруженный `checked-api` без build; Go проверяет event/source/run до загрузки и image identity после неё, затем сохраняет candidate proposal. Исторический run `41394590401` не переписывается; новая публикация в рамках рефакторинга не выполнялась.

## Защита main

Проверка 2 ноября 2026 года: репозиторий Public. GitHub API обеих веток `dev` и `main` подтвердил обязательный PR, checks `go`, `evidence`, `helm` от GitHub Actions (app ID 15368), strict=true, применение к администраторам, запрет force push и удаления. Обязательных чужих approvals нет: владелец может работать один. PR №1 закрыт с merged=true, merge SHA `b34fe8b19da5cfa67e0b16745586a3b2d18f1568`; CI подтверждён на PR и push в dev.

Новые required checks включать только после успешного запуска на соответствующих PR. `release-gate` включён в required main после успешного run `41400612212`; сама защита не доказывает успешный runtime promotion. Доступность защиты описана в [документации GitHub](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches).

После успешного PR №2 для dev через API добавлены required checks `container`, `kind`, с сохранением `go`, `evidence`, `helm` и strict=true. На push merge SHA `6f6521d` все пять checks успешны. Publishing run `41394590401` сначала был skipped, после отдельного разрешения повторён и завершился success без пересборки.

## Воспроизводимое управление выделенным стендом

Windows/PowerShell: `task gitops-preflight`, `task gitops-render`, `task gitops-install`, `task gitops-status`. Требуются Docker Desktop Linux, kind 0.33.0, Helm (CI использует 3.17.3; локальная проверка выполнена 4.3.0), Go и Git. Install скачивает kubectl 1.35.9 с проверкой SHA-256 и Argo CD Core 3.5.6 по закреплённому SHA-256 manifest. Собственный kubeconfig не меняет default context. Существующий одноимённый кластер принимается только с отдельным kubeconfig и ожидаемым узлом. Install создаёт отсутствующие Applications с manual policy; существующие не перезаписывает.

`task gitops-sync GITOPS_ENV=lab-dev GITOPS_REVISION=<полный-SHA>` синхронизирует конкретную ревизию из `origin/dev`, без prune, ожидает Succeeded/Synced/Healthy. Для stable указать `lab-stable` и SHA из `origin/main`; дополнительно выполняется release-check. Перед sync обновить remote refs через `git fetch origin`; команда не заменяет review и release gate. После первой успешной manual sync/smoke development можно включить policy из `gitops/argocd/applications.yaml`.

`task gitops-smoke GITOPS_ENV=lab-dev` (или lab-stable) создаёт собственный диагностический Pod, проверяет readyz/version/imageID и удаляет Pod в finally. Если Pod с таким именем уже есть, команда отказывается его использовать или удалять. Это удобная замена ручной подготовке Pod для observer ниже. JSON сохраняется в `.cache/gitops/observations/`.

`task gitops-cleanup CONFIRM_GITOPS_CLUSTER=kubernetes-application-lab-gitops` удаляет весь выделенный кластер с обоими окружениями. Не запускать для обычного завершения smoke; основной и network-кластеры не входят в область команды. Отсутствующее или неверное подтверждение приводит к отказу. Повторный install на существующем стенде и отказ cleanup проверены; создание с нуля и фактический cleanup остаются Prepared, существующие окружения сохраняются.

`container` проверяет три компонента через `scripts/ci-images.sh`. `ci-kind.sh` проверяет Ready EndpointSlice, failed rollout/recovery и Service failure/recovery с диагностикой. Release policy и тестируемые HTTP-клиенты находятся в Go; контракт описан в [design](../design/ci-cd-gitops.md).

## Runtime и release

`task gitops-observe-dev` сохраняет выбранные Argo/Pod/HTTP поля и проверяет health, version и imageID. `task gitops-failed-rollout` намеренно ломает image только lab-dev с bounded progress deadline, сохраняет Degraded/ErrImagePull и в finally восстанавливает известный Git desired state. Он временно отключает auto-sync для наблюдения ошибки, затем возвращает policy из Git. Не запускать этот опыт в stable.

`task release-check` остаётся offline-проверкой receipts и API inputs. `task release-verify` дополнительно требует `GH_TOKEN`, `GITHUB_EVENT_PATH`, `GITHUB_REPOSITORY` и `GITHUB_EVENT_NAME=pull_request`; в Actions эти metadata задаёт GitHub. В Go проверяются source/base, runs/jobs, принятая main identity либо оригинальный artifact, затем anonymous GHCR manifest/config. YAML только запускает команду и задаёт permissions. Для каждого кандидата нужны новые runtime receipts; исторические не перезаписывать. После настоящего promotion выполнить manual stable sync и smoke.

Observer выполняет HTTP из диагностического Pod `lab-smoke` выбранного namespace. Перед повтором создать его в отдельном GitOps-кластере и дождаться Ready, например для development:

```powershell
$k = '.cache/tools/kubectl-1.35.9.exe'
$scope = @('--kubeconfig','.cache/gitops/kubeconfig','--context','kind-kubernetes-application-lab-gitops')
& $k @scope -n lab-dev run lab-smoke --image=curlimages/curl:8.16.0 --restart=Never --command -- sleep 600
& $k @scope -n lab-dev wait --for=condition=Ready pod/lab-smoke --timeout=120s
task gitops-observe-dev
& $k @scope -n lab-dev delete pod lab-smoke
```

Для stable использовать namespace lab-stable и task gitops-observe-stable. Эти Pod не управляются Argo и удаляются после измерений; отсутствие diagnostic Pod является отказом наблюдателя, а не доказательством недоступности приложения. Promotion SHA в observer берётся из фактической последней sync operation, отдельно от Git revision текущего сравнения Argo.
