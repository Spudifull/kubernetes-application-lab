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
  helm lint "$chart" --namespace "$namespace" --set-string image.tag="$tag"
  helm template "$(basename "$chart")" "$chart" --namespace "$namespace" --set-string image.tag="$tag" > /dev/null
  for profile in "$chart"/values-*.yaml; do
    [[ -f "$profile" ]] || continue
    helm lint "$chart" -f "$profile" --namespace "$namespace" --set-string image.tag="$tag"
    helm template "$(basename "$chart")" "$chart" -f "$profile" --namespace "$namespace" --set-string image.tag="$tag" > /dev/null
  done
done
