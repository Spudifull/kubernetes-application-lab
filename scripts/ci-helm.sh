#!/usr/bin/env bash
set -euo pipefail
# All local charts and their values profiles; no cluster access.
for chart in helm/*; do
  [[ -f "$chart/Chart.yaml" ]] || continue
  namespace=kubernetes-application-lab
  tag=static-review
  case "$chart" in
    helm/lab-alerting|helm/lab-dependency-metrics|helm/lab-logging|helm/lab-observability|helm/lab-tracing-controls)
      namespace=kubernetes-application-lab-observability ;;
  esac
  if [[ "$chart" == helm/lab-alerting ]]; then
    namespace=kubernetes-application-lab-observability
    tag=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  fi
  helm lint "$chart" --kube-version 1.34.0 --namespace "$namespace" --set-string image.tag="$tag"
  helm template "$(basename "$chart")" "$chart" --kube-version 1.34.0 --namespace "$namespace" --set-string image.tag="$tag" > /dev/null
  for profile in "$chart"/values-*.yaml; do
    [[ -f "$profile" ]] || continue
    helm lint "$chart" -f "$profile" --kube-version 1.34.0 --namespace "$namespace" --set-string image.tag="$tag"
    helm template "$(basename "$chart")" "$chart" -f "$profile" --kube-version 1.34.0 --namespace "$namespace" --set-string image.tag="$tag" > /dev/null
  done
done
# A promoted digest must override tags, and malformed digests must fail closed.
digest="sha256:$(printf 'a%.0s' {1..64})"
helm template ci helm/lab-api -f gitops/dev/values.yaml --set-string "image.digest=$digest" \
  | grep -F "ghcr.io/spudifull/kubernetes-application-lab-api@$digest"
if helm template ci helm/lab-api --set-string image.digest=invalid > /dev/null 2>&1; then
  echo 'Malformed digest was accepted' >&2
  exit 1
fi
