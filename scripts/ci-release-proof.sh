#!/usr/bin/env bash
set -euo pipefail
: "${BASE_SHA:?Trusted PR base SHA is required}"
[[ "$BASE_SHA" =~ ^[a-f0-9]{40}$ ]]
mkdir -p .cache/release
registry=$(jq -r '.registry_observation // "docs/verification/milestone9/registry.json"' gitops/candidate.json)
# Read the accepted identity from the GitHub-supplied protected main base,
# never from a PR-provided ref. New identities still need original CI evidence.
git show "$BASE_SHA:gitops/candidate.json" > .cache/release/accepted.json
accepted_registry=$(jq -r '.registry_observation // "docs/verification/milestone9/registry.json"' .cache/release/accepted.json)
git show "$BASE_SHA:$accepted_registry" > .cache/release/accepted-registry.json
identity='[.source_commit_sha,.image_repository,.image_digest,.checked_container_run_id,.publish_run_id]'
if diff -u <(jq -Sc "$identity" .cache/release/accepted.json) <(jq -Sc "$identity" gitops/candidate.json) && \
   diff -u <(jq -Sc . .cache/release/accepted-registry.json) <(jq -Sc . "$registry"); then
  echo "Reusing image identity already accepted by protected main at $BASE_SHA; Git receipt has no artifact retention limit."
else
  source=$(jq -r .source_commit_sha gitops/candidate.json)
  checked=$(jq -r .checked_container_run_id gitops/candidate.json)
  gh run download "$checked" --name container-evidence --dir .cache/release
  jq -e --arg sha "$source" --slurpfile registry "$registry" \
    '.[0].Id == $registry[0].config_digest and .[0].Config.Labels["org.opencontainers.image.revision"] == $sha' .cache/release/image.json
fi
