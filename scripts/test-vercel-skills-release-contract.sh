#!/usr/bin/env bash
# shellcheck disable=SC2016 # Contract needles intentionally contain literal shell expressions.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
compat="$ROOT/scripts/test-vercel-skills-compat.sh"
release="$ROOT/.github/workflows/release.yml"
workflow="$ROOT/.github/workflows/skills-compatibility.yml"
makefile="$ROOT/Makefile"
skills_e2e="$ROOT/test/skills-e2e/lifecycle_test.go"
skills_e2e_manifest="$ROOT/test/skills-e2e/testdata/exact-round-trip/expected.json"
skills_e2e_registry="$ROOT/test/skills-e2e/registry/scenarios.go"
skills_e2e_generator="$ROOT/test/skills-e2e/cmd/e2e-coverage/main.go"

fail() {
  echo "release skills contract: $*" >&2
  exit 1
}

require_fixed() {
  file="$1"
  text="$2"
  grep -Fq -- "$text" "$file" || fail "$file is missing: $text"
}

bash -n "$compat"

# Releases deliberately validate Loom against FleetDB main and the exact corpus
# revision understood by the compatibility script.
require_fixed "$release" "uses: ./.github/workflows/skills-compatibility.yml"
require_fixed "$release" 'loom_ref: ${{ github.sha }}'
require_fixed "$release" "fleetdb_ref: main"
require_fixed "$release" 'LOOMCLI_TOKEN: ${{ secrets.LOOMCLI_TOKEN }}'
require_fixed "$release" 'FLEET_DB_TOKEN: ${{ secrets.FLEET_DB_TOKEN }}'
if grep -Fq 'secrets: inherit' "$release"; then
  fail "$release exposes unrelated repository secrets to the compatibility workflow"
fi
require_fixed "$workflow" "ref: dd089a8c752c966dee8bf0f27cb625ba193ffd9e"
require_fixed "$workflow" 'loom_sha: ${{ steps.revisions.outputs.loom_sha }}'
require_fixed "$workflow" 'fleetdb_sha: ${{ steps.revisions.outputs.fleetdb_sha }}'
require_fixed "$workflow" 'corpus_sha: ${{ steps.revisions.outputs.corpus_sha }}'
require_fixed "$workflow" 'ref: ${{ needs.resolve.outputs.loom_sha }}'
require_fixed "$workflow" 'ref: ${{ needs.resolve.outputs.fleetdb_sha }}'
require_fixed "$workflow" 'ref: ${{ needs.resolve.outputs.corpus_sha }}'
require_fixed "$workflow" 'EXPECTED_LOOM_SHA: ${{ needs.resolve.outputs.loom_sha }}'
require_fixed "$workflow" 'EXPECTED_FLEETDB_SHA: ${{ needs.resolve.outputs.fleetdb_sha }}'
require_fixed "$workflow" 'EXPECTED_CORPUS_SHA: ${{ needs.resolve.outputs.corpus_sha }}'
require_fixed "$workflow" 'Compatibility checkout drifted from the resolved revision pair'
require_fixed "$workflow" 'skills-compatibility-resolved-revisions-${{ github.run_id }}-${{ github.run_attempt }}'
require_fixed "$workflow" "if: github.repository == 'tysonthomas9/loomcli'"
require_fixed "$workflow" 'FLEET_DB_TOKEN is required to read private FleetDB from a LoomCLI workflow'
require_fixed "$compat" 'VERCEL_SKILLS_REF="${VERCEL_SKILLS_REF:-dd089a8c752c966dee8bf0f27cb625ba193ffd9e}"'
require_fixed "$makefile" 'test-skills-release-compat: test-skills-release-contract'

# The same corpus proof must exercise both byte-plane adapters. The S3 leg uses
# an ephemeral, job-scoped MinIO instance and bucket; no cloud credentials or
# persistent object-store resources are needed.
require_fixed "$workflow" 'storage: [local, s3]'
require_fixed "$workflow" 'name: LoomCLI / FleetDB compatibility (${{ matrix.storage }})'
require_fixed "$workflow" 'FLEET_WORKSPACE_FILE_STORE: ${{ matrix.storage }}'
require_fixed "$workflow" 'FLEET_WORKSPACE_FILE_TOKEN_SECRET='
require_fixed "$workflow" 'FLEET_RATE_LIMIT_ENABLED=false'
require_fixed "$workflow" 'quay.io/minio/minio:RELEASE.2025-09-07T16-13-09Z@sha256:14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e'
require_fixed "$workflow" 'quay.io/minio/mc:RELEASE.2025-08-13T08-35-41Z@sha256:a7fe349ef4bd8521fb8497f55c6042871b2ae640607cf99d9bede5e9bdf11727'
require_fixed "$workflow" 'curl --fail --silent --show-error http://127.0.0.1:9000/minio/health/ready'
require_fixed "$workflow" 'FLEET_WORKSPACE_FILE_S3_PATH_STYLE=true'
require_fixed "$workflow" 'FLEET_WORKSPACE_FILE_S3_ENDPOINT=http://127.0.0.1:9000'
require_fixed "$workflow" 'if: always() && matrix.storage == '\''s3'\'''
require_fixed "$workflow" 'docker logs "$MINIO_CONTAINER"'
require_fixed "$workflow" 'make test-skills-release-compat 2>&1 | tee "../skills-compatibility-$STORAGE_MODE.log"'
require_fixed "$workflow" 'REDIS_CONTAINER=loom-skills-redis-$GITHUB_RUN_ID-$GITHUB_RUN_ATTEMPT-$STORAGE_MODE'
require_fixed "$workflow" 'redis:7.4.2-alpine@sha256:02419de7eddf55aa5bcf49efb74e88fa8d931b4d77c07eff8a6b2144472b6952'
require_fixed "$compat" 'LOOM_E2E_REDIS_ADDR'
require_fixed "$compat" 'go test -tags=e2e -count=1 -v ./test/skills-e2e'
require_fixed "$skills_e2e" 'func TestSkillUpdateSelectsAndMaterializesExactRevision'
require_fixed "$skills_e2e" 'func TestIdenticalSkillReimportKeepsContentRevision'
require_fixed "$skills_e2e" 'func TestSkillContentUpdatePreservesBundledFiles'
require_fixed "$skills_e2e" 'func TestSkillRematerializationRemovesStaleFiles'
require_fixed "$skills_e2e" 'func TestSkillDeletionPrunesExistingMaterialization'
require_fixed "$skills_e2e" 'func TestSkillListReportsSelectedRevision'
require_fixed "$skills_e2e_manifest" '"file_tree_revision": "wft1_igfqkQVa_aBOjSr27_UUdKDCWweouc67JnMLCbk_e0k"'
require_fixed "$skills_e2e" 'registry.SkillUpdateRoundTrip.Covers(t)'
require_fixed "$skills_e2e" 'registry.StableIdenticalReimport.Covers(t)'
require_fixed "$skills_e2e" 'registry.ContentUpdatePreservesBundles.Covers(t)'
require_fixed "$skills_e2e" 'registry.RematerializationPrunesStaleFiles.Covers(t)'
require_fixed "$skills_e2e" 'registry.DeletionPrunesMaterialization.Covers(t)'
require_fixed "$skills_e2e" 'registry.ListShowRevisionAgreement.Covers(t)'
require_fixed "$skills_e2e_registry" 'ID:        "skill-update-roundtrip"'
require_fixed "$skills_e2e_registry" 'ID:        "stable-identical-reimport"'
require_fixed "$skills_e2e_registry" 'ID:        "content-update-preserves-bundles"'
require_fixed "$skills_e2e_registry" 'ID:        "rematerialization-prunes-stale-files"'
require_fixed "$skills_e2e_registry" 'ID:        "deletion-prunes-materialization"'
require_fixed "$skills_e2e_registry" 'ID:        "list-show-revision-agreement"'
require_fixed "$skills_e2e_generator" 'registry.WriteYAML(os.Stdout, registry.Scenarios)'
require_fixed "$workflow" 'go test ./test/skills-e2e/registry'
require_fixed "$workflow" 'go run ./test/skills-e2e/cmd/e2e-coverage > "../e2e-coverage-$STORAGE_MODE.yaml"'
require_fixed "$workflow" 'e2e-coverage-${{ matrix.storage }}.yaml'
require_fixed "$workflow" 'real_processes=loom-cli,fleet-db,redis,projector,http,$object_provider'
require_fixed "$workflow" 'skills-compatibility-${{ matrix.storage }}-revisions-${{ github.run_id }}-${{ github.run_attempt }}'

# The release log must identify every input to the compatibility result.
require_fixed "$compat" 'Compatibility revisions: loomcli=$loom_sha fleetdb=$fleet_db_sha vercel_skills=$actual_vercel_ref'

if grep -Fq 'run_exact_skill_lifecycle' "$compat"; then
  fail "$compat still embeds the lifecycle scenario in shell"
fi
if [[ -e "$ROOT/test/skills-e2e/edge-cases.yaml" ]]; then
  fail "covered Skill E2E metadata must be authored in Go, not checked-in YAML"
fi

# deploy-to-vercel is a supported corpus member with exactly three bundled
# files. The complete imported SKILL.md and Archive.zip receive explicit byte
# integrity checks in addition to the whole-bundle comparison.
require_fixed "$compat" 'import_ok deploy-to-vercel'
require_fixed "$compat" 'persisted skill count is $persisted_count, want 9'
require_fixed "$compat" 'compare_skill deploy-to-vercel deploy-to-vercel 3'
require_fixed "$compat" 'verify_binary_file deploy-to-vercel Archive.zip'
require_fixed "$compat" 'cmp -s "$source_dir/SKILL.md" "$target_dir/SKILL.md"'
require_fixed "$compat" 'SKILL.md byte mismatch'
require_fixed "$compat" 'source size=$source_size sha256=$source_hash'
require_fixed "$compat" 'materialized size=$target_size sha256=$target_hash'
require_fixed "$compat" 'cmp -s "$source_file" "$target_file"'
require_fixed "$compat" '9 compatible skills'

if grep -Fq 'expected binary rejection' "$compat" ||
  grep -Fq 'binary content is not supported: Archive.zip' "$compat"; then
  fail "$compat still treats deploy-to-vercel as an expected rejection"
fi

echo "PASS: release skills compatibility contract"
