#!/usr/bin/env bash
set -euo pipefail
: "${IMAGE:?IMAGE is required}"
: "${REVISION:?REVISION is required}"
# This script owns only the uniquely named cluster it creates.
cluster="lab-ci-${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}"
if kind get clusters | grep -Fxq "$cluster"; then
  echo "Refusing to reuse existing cluster $cluster" >&2
  exit 1
fi
mkdir -p .cache/container/kind .cache/kind
export KUBECONFIG="$PWD/.cache/kind/$cluster.config"
created=false
cleanup() {
  if [[ "$created" == true ]]; then
    kubectl --context "kind-$cluster" get pods,deploy,svc,endpointslices -n ci-smoke -o json > .cache/container/kind/resources.json || true
    kubectl --context "kind-$cluster" get events -n ci-smoke -o json > .cache/container/kind/events.json || true
    kind delete cluster --name "$cluster"
  fi
}
trap cleanup EXIT
created=true
kind create cluster --name "$cluster" --image kindest/node:v1.34.0@sha256:7416a61b42b1662ca6ca89f02028ac133a309a2a30ba309614e8ec94d976dc5a --wait 180s
kind load docker-image "$IMAGE" --name "$cluster"
repository="${IMAGE%:*}"
tag="${IMAGE##*:}"
helm upgrade --install ci-api helm/lab-api --namespace ci-smoke --create-namespace \
  --kube-context "kind-$cluster" --set-string image.repository="$repository" --set-string image.tag="$tag" --wait --timeout 120s
kubectl --context "kind-$cluster" version -o json > .cache/container/kind/kubernetes-version.json
kubectl --context "kind-$cluster" -n ci-smoke run smoke --image=curlimages/curl:8.16.0 --restart=Never --command -- sleep 600
kubectl --context "kind-$cluster" -n ci-smoke wait --for=condition=Ready pod/smoke --timeout=120s
request() {
  kubectl --context "kind-$cluster" -n ci-smoke exec smoke -- curl --fail --silent --show-error --max-time 5 "http://ci-api.ci-smoke.svc/$1"
}
request readyz
request version > .cache/container/kind/version.json
jq -e --arg revision "$REVISION" '.revision == $revision' .cache/container/kind/version.json
kubectl --context "kind-$cluster" -n ci-smoke get pods -l app.kubernetes.io/instance=ci-api -o json > .cache/container/kind/ready-pods.json
# Deliberately break Service selection, assert HTTP failure, then restore it.
kubectl --context "kind-$cluster" -n ci-smoke patch service ci-api --type merge -p '{"spec":{"selector":{"ci-fault":"missing"}}}'
failed=false
for attempt in {1..10}; do
  if ! request readyz; then failed=true; break; fi
  sleep 1
done
[[ "$failed" == true ]] || { echo 'Negative smoke did not detect broken Service' >&2; exit 1; }
kubectl --context "kind-$cluster" -n ci-smoke patch service ci-api --type json -p '[{"op":"remove","path":"/spec/selector/ci-fault"}]'
recovered=false
for attempt in {1..20}; do
  if request readyz; then recovered=true; break; fi
  sleep 1
done
[[ "$recovered" == true ]] || { echo 'Service did not recover' >&2; exit 1; }
printf '{"negative_detected":true,"service_recovered":true}\n' > .cache/container/kind/negative.json
