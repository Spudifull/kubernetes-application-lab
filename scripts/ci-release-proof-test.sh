#!/usr/bin/env bash
set -euo pipefail
script="$(pwd)/scripts/ci-release-proof.sh"
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
cd "$fixture"
git init --quiet
git config user.name fixture
git config user.email fixture@example.invalid
mkdir -p gitops docs/verification/milestone9 bin
printf '%s\n' '{"source_commit_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","image_repository":"example/api","image_digest":"sha256:aaa","checked_container_run_id":1,"publish_run_id":2}' > gitops/candidate.json
printf '%s\n' '{"config_digest":"sha256:checked"}' > docs/verification/milestone9/registry.json
git add .
git commit --quiet -m 'Создана тестовая база'
export BASE_SHA
BASE_SHA=$(git rev-parse HEAD)
export PATH="$fixture/bin:$PATH"
cat > bin/gh <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
echo called >> gh-calls
printf '%s\n' '[{"Id":"sha256:checked","Config":{"Labels":{"org.opencontainers.image.revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}]' > .cache/release/image.json
MOCK
chmod +x bin/gh
# An accepted identity must work even when the original artifact is unavailable.
bash "$script"
test ! -e gh-calls
# A changed run ID cannot inherit trust from a receipt for the old candidate.
jq '.checked_container_run_id = 3' gitops/candidate.json > candidate-new.json
mv candidate-new.json gitops/candidate.json
bash "$script"
test "$(wc -l < gh-calls)" -eq 1
# A tampered receipt must match original checked image metadata, or fail.
jq '.config_digest = "sha256:tampered"' docs/verification/milestone9/registry.json > registry-new.json
mv registry-new.json docs/verification/milestone9/registry.json
if bash "$script"; then echo 'Tampered config digest accepted' >&2; exit 1; fi
echo 'Release proof: accepted baseline, changed candidate and tampered receipt checks passed.'
