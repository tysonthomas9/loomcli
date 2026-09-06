#!/usr/bin/env bash
set -euo pipefail

# Search failures must not masquerade as a clean architecture check.
if ! command -v rg >/dev/null 2>&1; then
  echo "Error: ripgrep (rg) is required for the control-plane path guard." >&2
  exit 127
fi

# ripgrep uses 1 for no matches and 2 for errors. Only no matches is benign.
run_rg() {
  local status=0
  rg "$@" || status=$?
  if [[ $status -ne 0 && $status -ne 1 ]]; then
    echo "Error: control-plane ripgrep search failed (exit $status)." >&2
    return "$status"
  fi
}

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

bad=0

print_valid_paths() {
  cat >&2 <<'MSG'

Valid runtime control-plane paths are:
  local: loomcli -> HTTP client -> fleet-db subprocess -> RedisStorage -> miniredis or external Redis
  cloud: loomcli -> HTTP client -> fleet-db service -> Redis/Postgres
MSG
}

fail_with_matches() {
  local title="$1"
  local fix="$2"

  if [[ $bad -eq 0 ]]; then
    echo "control-plane path violations found:" >&2
  fi
  echo "" >&2
  echo "$title" >&2
  cat "$tmp" >&2
  echo "" >&2
  echo "Fix: $fix" >&2
  bad=1
}

# Runtime code must not import or construct the test-only in-memory store.
# Match the package import only: a bare `memstore.New(` pattern would also
# flag other packages named memstore (e.g. harness-wrapper's chat/memstore,
# an in-process chat turn-metadata store), and any use of the loomcli
# memstore requires importing it.
run_rg -n \
  -e 'github\.com/tysonthomas9/loomcli/internal/infra/memstore' \
  cmd internal \
  --glob '*.go' \
  --glob '!**/*_test.go' \
  --glob '!internal/infra/memstore/**' \
  >"$tmp"

if [[ -s "$tmp" ]]; then
  fail_with_matches \
    "memstore referenced from non-test runtime code:" \
    "use bootstrap.OpenStore/cmdstore.OpenStore so runtime traffic goes through fleet-db over HTTP."
fi

# Runtime code outside bootstrap should not import the fleet-db store client
# directly. Bootstrap owns local/cloud selection and returns the store.Store
# handle callers should use.
run_rg -n \
  -e 'github\.com/tysonthomas9/loomcli/internal/infra/fleetdb' \
  cmd internal \
  --glob '*.go' \
  --glob '!**/*_test.go' \
  --glob '!internal/bootstrap/openstore.go' \
  --glob '!internal/infra/fleetdb/**' \
  >"$tmp"

if [[ -s "$tmp" ]]; then
  fail_with_matches \
    "fleet-db store client imported outside internal/bootstrap/openstore.go:" \
    "route callers through bootstrap.OpenStore/cmdstore.OpenStore so mode selection stays centralized."
fi

# The fleet-db store client should be constructed by the bootstrap opener only.
# That keeps local/cloud selection centralized and prevents alternate runtime
# control-plane paths from growing in command or UI code.
run_rg -n 'fleetdb\.New\(' \
  cmd internal \
  --glob '*.go' \
  --glob '!**/*_test.go' \
  >"$tmp"

if [[ -s "$tmp" ]]; then
  disallowed="$(mktemp)"
  trap 'rm -f "$tmp" "$disallowed"' EXIT
  while IFS=: read -r file line text; do
    [[ -n "${file:-}" ]] || continue
    if [[ "$file" != "internal/bootstrap/openstore.go" ]]; then
      printf '%s:%s:%s\n' "$file" "$line" "$text" >>"$disallowed"
    fi
  done <"$tmp"

  if [[ -s "$disallowed" ]]; then
    cp "$disallowed" "$tmp"
    fail_with_matches \
      "fleetdb.New called outside internal/bootstrap/openstore.go:" \
      "route callers through bootstrap.OpenStore/cmdstore.OpenStore instead of constructing a separate client path."
  fi
fi

# The deployment mode enum is intentionally closed over local/cloud.
modes="$(run_rg -o 'Mode[A-Z][A-Za-z0-9_]*' internal/bootstrap/mode.go)"
printf '%s\n' "$modes" \
  | sort -u \
  | grep -vxE 'ModeLocal|ModeCloud|^$' \
  >"$tmp" || true

if [[ -s "$tmp" ]]; then
  fail_with_matches \
    "unexpected bootstrap modes found in internal/bootstrap/mode.go:" \
    "keep runtime storage modes to ModeLocal and ModeCloud; model any sub-option behind those modes."
fi

# Local mode must continue to spawn embedded fleet-db and then build the HTTP
# client. Cloud mode must continue to build the same HTTP client against the
# configured service URL.
for required in \
  'case ModeCloud:' \
  'return openCloudStore(cfg, logger)' \
  'case ModeLocal:' \
  'return openLocalStore(ctx, dataDir, cfg, logger)' \
  'StartEmbedded(ctx, dataDir, logger)' \
  'fleetdb.New(cfg)'
do
  if ! grep -Fq "$required" internal/bootstrap/openstore.go; then
    printf '%s\n' "internal/bootstrap/openstore.go missing required topology anchor: $required" >"$tmp"
    fail_with_matches \
      "bootstrap.OpenStore no longer matches the required local/cloud topology:" \
      "restore the centralized local/cloud fleet-db HTTP bootstrap path or update this guard with an explicit architecture decision."
  fi
done

if [[ $bad -ne 0 ]]; then
  print_valid_paths
  exit 1
fi

echo "Control-plane runtime paths are restricted to local/cloud fleet-db HTTP topologies."
