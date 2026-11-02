#!/usr/bin/env bash
set -euo pipefail
: "${SOURCE_SHA:?SOURCE_SHA is required}"
# Keep a single required container job: any component failure blocks the PR.
for component in api publisher worker; do
  output=".cache/container/$component"
  mkdir -p "$output"
  go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 -format json "./cmd/$component" > "$output/govulncheck.json"
  go run ./cmd/labctl govulncheck --file "$output/govulncheck.json" > "$output/govulncheck-summary.json"
  docker buildx build --load --provenance=false --platform linux/amd64 \
    --file "docker/$component.Dockerfile" --tag "lab-ci-$component:$SOURCE_SHA" \
    --build-arg "REVISION=$SOURCE_SHA" --build-arg VERSION=dev \
    --build-arg "BUILD_DATE=$(git show -s --format=%cI HEAD)" \
    --metadata-file "$output/build.json" .
  docker image inspect "lab-ci-$component:$SOURCE_SHA" > "$output/image.json"
  jq -e '.[0].Config.User == "65532:65532"' "$output/image.json"
  docker run --rm -v /var/run/docker.sock:/var/run/docker.sock \
    -v "$PWD/$output:/results" \
    aquasec/trivy@sha256:790fd66d1697395d8693d1acaa1ea702406a20f109b6fc7bac8db623eca6c06c \
    image --scanners vuln --severity HIGH,CRITICAL --exit-code 1 \
    --format json --output /results/trivy.json "lab-ci-$component:$SOURCE_SHA"
done
# Stable artifact paths consumed by publishing and older release gates.
cp .cache/container/api/image.json .cache/container/image.json
cp .cache/container/api/build.json .cache/container/build.json
jq -e --arg sha "$SOURCE_SHA" '.[0].Config.Labels["org.opencontainers.image.revision"] == $sha' .cache/container/image.json
docker save "lab-ci-api:$SOURCE_SHA" -o .cache/api.tar
