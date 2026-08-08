#!/usr/bin/env bash
set -euo pipefail

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
rg -n \
  -e 'github\.com/tysonthomas9/loomcli/internal/infra/memstore' \
  cmd internal \
  --glob '*.go' \
  --glob '!**/*_test.go' \
  --glob '!internal/infra/memstore/**' \
  >"$tmp" || true

if [[ -s "$tmp" ]]; then
  fail_with_matches \
    "memstore referenced from non-test runtime code:" \
    "use bootstrap.OpenStore/cmdstore.OpenStore so runtime traffic goes through fleet-db over HTTP."
fi

# Runtime code outside bootstrap and the declared capability-composition seams
# should not import the fleet-db store client directly. Bootstrap still owns
# local/cloud selection and construction. The Phase 2 Workflow Catalog seams
# below only share that already-constructed client, expose its capability key,
# or translate its narrow transport into the module-owned adapter contract.
# The Phase 3 Automation seams likewise share the same client to expose the
# consumer-owned durability and atomic dispatch ports; neither constructs a
# second client or creates an alternate control-plane path. The Phase 4
# Execution and Artifacts seams follow the same rule: files listed below are
# composition roots or narrow transport adapters over the one bootstrap-owned
# client, never additional client constructors.
# The Phase 5 Agents, Interaction, Source Control/Connectors, and
# AgentProvisioning seams likewise receive only StoreHandle.FleetDBClient or a
# capability-specific transport derived from that one client. Workspace
# repository admission receives only the shared client's
# RepositoryAdmissionTransport (plus its DTO/error types) at the serve
# composition root. Every Phase 5 exception is an exact file: none constructs
# fleetdb.New, selects a base URL, authenticates another client, or opens an
# alternate control-plane path.
rg -n \
  -e 'github\.com/tysonthomas9/loomcli/internal/infra/fleetdb' \
  cmd internal \
  --glob '*.go' \
  --glob '!**/*_test.go' \
  --glob '!internal/infra/fleetdb/**' \
  >"$tmp" || true

if [[ -s "$tmp" ]]; then
  disallowed="$(mktemp)"
  trap 'rm -f "$tmp" "$disallowed"' EXIT
  while IFS=: read -r file line text; do
    [[ -n "${file:-}" ]] || continue
    case "$file" in
      internal/bootstrap/openstore.go | \
      internal/bootstrap/embedded.go | \
      internal/app/serve/artifacts.go | \
      internal/app/serve/artifacts_fleetdb.go | \
      internal/app/serve/automationcomposition/automation_execution.go | \
      internal/app/serve/automationcomposition/automation_fleetdb.go | \
      internal/app/serve/automationcomposition/workflow_catalog.go | \
      internal/app/serve/execution.go | \
      internal/app/serve/execution_driver_run_fleetdb.go | \
      internal/app/serve/execution_task_run_ports.go | \
      internal/app/serve/execution_task_run_recovery.go | \
      internal/app/serve/sourcecontrolcomposition/source_control.go | \
      internal/app/serve/sourcecontrolcomposition/connectors_fleetdb.go | \
      internal/app/serve/agentcomposition/provisioning.go | \
      internal/app/serve/agentcomposition/agents.go | \
      internal/app/serve/agentcomposition/agents_fleetdb.go | \
      internal/app/serve/interactioncomposition/interaction_fleetdb.go | \
      internal/app/serve/interactioncomposition/interaction_transcript.go | \
      internal/app/serve/workflow_catalog.go | \
      internal/app/serve/workflow_catalog_fleetdb.go | \
      internal/app/agentprovisioning/fleetdb/adapter.go | \
      internal/cli/serve/serve.go | \
      internal/cli/serve/serveadapter/source_control.go | \
      internal/cli/serve/workspacemgr/workspace_store.go | \
      internal/cli/serve/workspacemgr/workspace_admission_operations.go | \
      internal/cli/serve/workspacemgr/workspace_store_repository_materialization.go | \
      internal/cli/serve/workspacemgr/repository_admission_lease.go | \
      internal/cli/serve/workspacemgr/repository_admission_process.go | \
      internal/cli/serve/workspacemgr/repository_admission_workspace_operations.go | \
      internal/cli/serve/serveadapter/workflow_catalog.go)
        ;;
      *)
        printf '%s:%s:%s\n' "$file" "$line" "$text" >>"$disallowed"
        ;;
    esac
  done <"$tmp"

  if [[ -s "$disallowed" ]]; then
    cp "$disallowed" "$tmp"
    fail_with_matches \
      "fleet-db store client imported outside bootstrap or a declared composition seam:" \
      "route callers through bootstrap.OpenStore/cmdstore.OpenStore, or add a reviewed capability adapter/composition seam without constructing another client."
  fi
fi

# The fleet-db store client should be constructed by the bootstrap opener only.
# That keeps local/cloud selection centralized and prevents alternate runtime
# control-plane paths from growing in command or UI code.
# Match both identifiers used for the infra package itself, but not longer
# adapter aliases such as catalogfleetdb.New. Construction remains restricted
# to bootstrap even though the exact import allowlist above permits composition
# code to share the client.
rg -n \
  -e '\bfleetdb\.New\(' \
  -e '\binfrafleetdb\.New\(' \
  cmd internal \
  --glob '*.go' \
  --glob '!**/*_test.go' \
  >"$tmp" || true

if [[ -s "$tmp" ]]; then
  disallowed="$(mktemp)"
  trap 'rm -f "$tmp" "$disallowed"' EXIT
  while IFS=: read -r file line text; do
    [[ -n "${file:-}" ]] || continue
    if [[ "$file" != "internal/bootstrap/openstore.go" && "$file" != "internal/bootstrap/embedded.go" ]]; then
      printf '%s:%s:%s\n' "$file" "$line" "$text" >>"$disallowed"
    fi
  done <"$tmp"

  if [[ -s "$disallowed" ]]; then
    cp "$disallowed" "$tmp"
    fail_with_matches \
      "fleetdb.New called outside bootstrap client construction:" \
      "route callers through bootstrap.OpenStore/cmdstore.OpenStore instead of constructing a separate client path."
  fi
fi

# The deployment mode enum is intentionally closed over local/cloud.
rg -o 'Mode[A-Z][A-Za-z0-9_]*' internal/bootstrap/mode.go \
  | sort -u \
  | grep -vxE 'ModeLocal|ModeCloud' \
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
