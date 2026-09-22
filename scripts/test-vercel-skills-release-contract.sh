#!/usr/bin/env bash
# shellcheck disable=SC2016 # Contract needles intentionally contain literal shell expressions.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
compat="$ROOT/scripts/test-vercel-skills-compat.sh"
release="$ROOT/.github/workflows/release.yml"
workflow="$ROOT/.github/workflows/skills-compatibility.yml"
makefile="$ROOT/Makefile"

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
require_fixed "$release" "fleetdb_ref: main"
require_fixed "$workflow" "ref: dd089a8c752c966dee8bf0f27cb625ba193ffd9e"
require_fixed "$compat" 'VERCEL_SKILLS_REF="${VERCEL_SKILLS_REF:-dd089a8c752c966dee8bf0f27cb625ba193ffd9e}"'
require_fixed "$makefile" 'test-skills-release-compat: test-skills-release-contract'

# The release log must identify every input to the compatibility result.
require_fixed "$compat" 'Compatibility revisions: loomcli=$loom_sha fleetdb=$fleet_db_sha vercel_skills=$actual_vercel_ref'

# deploy-to-vercel is a supported corpus member with exactly three bundled
# files. Archive.zip receives explicit binary integrity checks in addition to
# the whole-bundle byte comparison.
require_fixed "$compat" 'import_ok deploy-to-vercel'
require_fixed "$compat" 'persisted skill count is $persisted_count, want 9'
require_fixed "$compat" 'compare_skill deploy-to-vercel deploy-to-vercel 3'
require_fixed "$compat" 'verify_binary_file deploy-to-vercel Archive.zip'
require_fixed "$compat" 'source size=$source_size sha256=$source_hash'
require_fixed "$compat" 'materialized size=$target_size sha256=$target_hash'
require_fixed "$compat" 'cmp -s "$source_file" "$target_file"'
require_fixed "$compat" '9 compatible skills'

if grep -Fq 'expected binary rejection' "$compat" ||
  grep -Fq 'binary content is not supported: Archive.zip' "$compat"; then
  fail "$compat still treats deploy-to-vercel as an expected rejection"
fi

echo "PASS: release skills compatibility contract"
