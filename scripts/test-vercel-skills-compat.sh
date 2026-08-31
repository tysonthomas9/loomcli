#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FLEET_DB_REPO="${FLEET_DB_REPO:?set FLEET_DB_REPO to a fleet-db checkout}"
VERCEL_SKILLS_REPO="${VERCEL_SKILLS_REPO:?set VERCEL_SKILLS_REPO to the pinned vercel-labs/agent-skills checkout}"
VERCEL_SKILLS_REF="${VERCEL_SKILLS_REF:-dd089a8c752c966dee8bf0f27cb625ba193ffd9e}"
LOOM_E2E_REDIS_ADDR="${LOOM_E2E_REDIS_ADDR:?set LOOM_E2E_REDIS_ADDR to the run-owned real Redis address}"

if [[ ! -d "$FLEET_DB_REPO/cmd/fleet-db" ]]; then
  echo "fleet-db checkout not found at $FLEET_DB_REPO" >&2
  exit 2
fi
if [[ ! -d "$VERCEL_SKILLS_REPO/skills" ]]; then
  echo "Vercel skills checkout not found at $VERCEL_SKILLS_REPO" >&2
  exit 2
fi

actual_vercel_ref="$(git -C "$VERCEL_SKILLS_REPO" rev-parse HEAD)"
if [[ "$actual_vercel_ref" != "$VERCEL_SKILLS_REF" ]]; then
  echo "Vercel skills checkout is $actual_vercel_ref, want pinned $VERCEL_SKILLS_REF" >&2
  exit 2
fi

loom_sha="$(git -C "$ROOT" rev-parse HEAD)"
fleet_db_sha="$(git -C "$FLEET_DB_REPO" rev-parse HEAD)"
echo "Compatibility revisions: loomcli=$loom_sha fleetdb=$fleet_db_sha vercel_skills=$actual_vercel_ref"
echo "Real-service topology: loom-cli fleet-db redis projector http object-store=${FLEET_WORKSPACE_FILE_STORE:-local}"

tmp="$(mktemp -d -t loom-vercel-skills-compat.XXXXXX)"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

loom_bin="$tmp/loom"
fleet_db_bin="$tmp/fleet-db"
echo "Building loomcli $loom_sha"
(cd "$ROOT" && go build -o "$loom_bin" ./cmd/loom)
echo "Building fleet-db $fleet_db_sha"
(cd "$FLEET_DB_REPO" && go build -o "$fleet_db_bin" ./cmd/fleet-db)

export HOME="$tmp/home"
export LOOM_CONFIG_DIR="$tmp/loom-config"
export LOOM_FLEET_DB_ACTOR="vercel-skills-compat"
export LOOM_ISSUE_BACKEND="fleetdb"
export LOOM_LOG_FORMAT="text"
export LOOM_WORKSPACE="SKILLSRELEASE"
export FLEET_DB_BIN="$fleet_db_bin"
mkdir -p "$HOME" "$LOOM_CONFIG_DIR"
printf '{"version":1,"fleetdb_redis":{"enabled":true,"addr":"%s"},"agent_runtime":{"default":"local"}}\n' \
  "$LOOM_E2E_REDIS_ADDR" >"$LOOM_CONFIG_DIR/local-settings.json"

workspace_log="$tmp/workspace-add.log"
if ! "$loom_bin" workspace add "$LOOM_WORKSPACE" --description "release compatibility corpus" >"$workspace_log" 2>&1; then
  cat "$workspace_log" >&2
  exit 1
fi

skills_e2e_log="$tmp/skills-e2e.log"
if ! (cd "$ROOT" && SKILLS_E2E_LOOM_BIN="$loom_bin" \
  go test -tags=e2e -count=1 -v ./test/skills-e2e) >"$skills_e2e_log" 2>&1; then
  cat "$skills_e2e_log" >&2
  exit 1
fi
cat "$skills_e2e_log"

import_ok() {
  source_name="$1"
  log="$tmp/import-$source_name.log"
  if ! "$loom_bin" skill import "$VERCEL_SKILLS_REPO/skills/$source_name" >"$log" 2>&1; then
    echo "Vercel skill import unexpectedly failed: $source_name" >&2
    cat "$log" >&2
    exit 1
  fi
}

import_ok composition-patterns
import_ok deploy-to-vercel
import_ok react-best-practices
import_ok react-native-skills
import_ok react-view-transitions
import_ok vercel-cli-with-tokens
import_ok vercel-optimize
import_ok web-design-guidelines
import_ok writing-guidelines

list_log="$tmp/skill-list.log"
list_stderr="$tmp/skill-list.stderr"
if ! "$loom_bin" skill list >"$list_log" 2>"$list_stderr"; then
  cat "$list_stderr" >&2
  exit 1
fi
persisted_count="$(grep 'scope=workspace' "$list_log" | grep -vc '^exact-round-trip ')"
if [[ "$persisted_count" != "9" ]]; then
  echo "persisted skill count is $persisted_count, want 9" >&2
  cat "$list_log" >&2
  exit 1
fi
if ! grep -q '^vercel-react-view-transitions ' "$list_log"; then
  echo "react-view-transitions was not persisted" >&2
  cat "$list_log" >&2
  exit 1
fi

materialized="$tmp/materialized"
mkdir -p "$materialized"
materialize_log="$tmp/materialize.log"
if ! (cd "$materialized" && "$loom_bin" skill materialize) >"$materialize_log" 2>&1; then
  cat "$materialize_log" >&2
  exit 1
fi

compare_skill() {
  source_name="$1"
  stored_name="$2"
  expected_bundle_count="$3"
  source_dir="$VERCEL_SKILLS_REPO/skills/$source_name"
  target_dir="$materialized/.agents/skills/$stored_name"

  source_count="$(find "$source_dir" -type f ! -name SKILL.md | wc -l | tr -d ' ')"
  target_count="$(find "$target_dir" -type f ! -name SKILL.md | wc -l | tr -d ' ')"
  if [[ "$source_count" != "$expected_bundle_count" || "$target_count" != "$expected_bundle_count" ]]; then
    echo "$source_name bundle count source=$source_count materialized=$target_count want=$expected_bundle_count" >&2
    exit 1
  fi

  while IFS= read -r -d '' source_file; do
    relative_path="${source_file#"$source_dir"/}"
    target_file="$target_dir/$relative_path"
    if [[ ! -f "$target_file" ]] || ! cmp -s "$source_file" "$target_file"; then
      echo "$source_name bundled file mismatch: $relative_path" >&2
      exit 1
    fi
    if [[ -x "$source_file" && ! -x "$target_file" ]] || [[ ! -x "$source_file" && -x "$target_file" ]]; then
      echo "$source_name executable-bit mismatch: $relative_path" >&2
      exit 1
    fi
  done < <(find "$source_dir" -type f ! -name SKILL.md -print0)

  if ! cmp -s "$source_dir/SKILL.md" "$target_dir/SKILL.md"; then
    source_size="$(wc -c <"$source_dir/SKILL.md" | tr -d ' ')"
    target_size="$(wc -c <"$target_dir/SKILL.md" | tr -d ' ')"
    source_hash="$(sha256_file "$source_dir/SKILL.md")"
    target_hash="$(sha256_file "$target_dir/SKILL.md")"
    echo "$source_name SKILL.md byte mismatch" >&2
    echo "source size=$source_size sha256=$source_hash" >&2
    echo "materialized size=$target_size sha256=$target_hash" >&2
    exit 1
  fi
}

sha256_file() {
  file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{ print $1 }'
  else
    shasum -a 256 "$file" | awk '{ print $1 }'
  fi
}

verify_binary_file() {
  source_name="$1"
  relative_path="$2"
  source_file="$VERCEL_SKILLS_REPO/skills/$source_name/$relative_path"
  target_file="$materialized/.agents/skills/$source_name/$relative_path"

  [[ -f "$source_file" ]] || {
    echo "$source_name binary source is missing: $relative_path" >&2
    exit 1
  }
  [[ -f "$target_file" ]] || {
    echo "$source_name binary materialization is missing: $relative_path" >&2
    exit 1
  }

  source_size="$(wc -c <"$source_file" | tr -d ' ')"
  target_size="$(wc -c <"$target_file" | tr -d ' ')"
  source_hash="$(sha256_file "$source_file")"
  target_hash="$(sha256_file "$target_file")"

  if [[ "$source_size" != "$target_size" || "$source_hash" != "$target_hash" ]] ||
    ! cmp -s "$source_file" "$target_file"; then
    echo "$source_name binary mismatch: $relative_path" >&2
    echo "source size=$source_size sha256=$source_hash" >&2
    echo "materialized size=$target_size sha256=$target_hash" >&2
    exit 1
  fi
  echo "$source_name/$relative_path verified: size=$source_size sha256=$source_hash"
}

compare_skill composition-patterns vercel-composition-patterns 13
compare_skill deploy-to-vercel deploy-to-vercel 3
compare_skill react-best-practices vercel-react-best-practices 75
compare_skill react-native-skills vercel-react-native-skills 41
compare_skill react-view-transitions vercel-react-view-transitions 7
compare_skill vercel-cli-with-tokens vercel-cli-with-tokens 0
compare_skill vercel-optimize vercel-optimize 155
compare_skill web-design-guidelines web-design-guidelines 0
compare_skill writing-guidelines writing-guidelines 0
verify_binary_file deploy-to-vercel Archive.zip

if ! grep -Fq "<ViewTransition>" \
  "$materialized/.agents/skills/vercel-react-view-transitions/SKILL.md"; then
  echo "react-view-transitions description was not preserved in SKILL.md" >&2
  exit 1
fi
if ! grep -Fq "<ViewTransition>" "$materialized/.agents/skills/INDEX.md"; then
  echo "react-view-transitions description was not preserved in INDEX.md" >&2
  exit 1
fi

echo "PASS: pinned Vercel corpus imported through FleetDB and materialized (9 compatible skills)"
