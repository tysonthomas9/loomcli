#!/usr/bin/env bash
# setup.sh — idempotent A1 github-review-agent provisioning against a running
# `loom serve` (same container, embedded fleet-db). It wires the three pieces
# the live review flow needs, in dependency order:
#
#   1. the github CONNECTOR — one inbound webhook secret (HMAC verification
#      root for every github binding of this source kind) plus the outbound
#      gh-token CREDENTIAL, both supplied over stdin and sealed before the
#      store write (LOOM_CONNECTOR_VAULT_KEY, shared with serve);
#   2. the deny-by-default GRANT authorizing exactly github.review.post on
#      repo:<owner>/<repo> for this binding (egress is deny-by-default —
#      without this the connector dispatch refuses the review post);
#   3. the trigger BINDING that routes github.pull_request.opened (+ the
#      synchronize/reopened/ready_for_review actions via event patterns) to
#      the github-review-agent workflow, replace-on-resubscribe per subject
#      repo#PR, with the requested retry policy.
#
# Everything is keyed off env so the stack script and a human operator drive
# it the same way. Re-running is safe: each create is treated as success when
# the object already exists (the embedded store returns conflict/already-exists,
# which we tolerate). Secrets travel on stdin ONLY — never argv, never echoed.
#
# Confirmed against the live CLI (do NOT trust stale specs):
#   internal/cli/connector/connector_cmd.go  — create reads stdin in the order
#     inbound-secret (line 1) THEN outbound-credential (line 2); grant create
#     takes --connector/--binding/--action/--resource.
#   internal/cli/trigger/trigger_cmd.go       — bindings create flags:
#     --route-key --workflow --secret --binding-id --concurrency-policy
#     --subject-key-template --event-pattern (repeatable) --retry-max-attempts
#     --retry-backoff (seconds). An enabled github binding requires --secret.
set -euo pipefail

# ── Required env ──────────────────────────────────────────────────────────
# LOOM_WORKSPACE             active workspace key (connector/grant/binding live here)
# A1_GITHUB_REPO             owner/name of the review subject repo, e.g. tysonthomas9/loom-review-sandbox
# A1_WEBHOOK_SECRET          inbound webhook HMAC secret (stdin line 1 of connector create)
# GH_TOKEN                   outbound gh credential to seal (stdin line 2 of connector create)
# LOOM_CONNECTOR_VAULT_KEY   standard-base64 32-byte vault key (must match serve's)
#
# ── Optional env (defaults) ───────────────────────────────────────────────
A1_CONNECTOR_ID="${A1_CONNECTOR_ID:-github}"
A1_CONNECTOR_NAME="${A1_CONNECTOR_NAME:-A1 GitHub review connector}"
A1_WEBHOOK_ENDPOINT_PATH="${A1_WEBHOOK_ENDPOINT_PATH:-/webhooks/github}"
A1_WORKFLOW_NAME="${A1_WORKFLOW_NAME:-github-review-agent}"
A1_ROUTE_KEY="${A1_ROUTE_KEY:-github.pull_request.opened}"
A1_BINDING_ID="${A1_BINDING_ID:-a1-github-review}"
A1_REVIEW_ACTION="${A1_REVIEW_ACTION:-github.review.post}"
# The workflow makes THREE connector calls, each gated by its own deny-by-default
# grant (Evaluate requires g.Action == action exactly): the pre-flight liveness
# read (github.pull_request.read), the diff fetch (github.compare.read), and the
# COMMENT review post (github.review.post). All three are scoped to the one
# subject repo. Space-separated so the loop below grants each.
A1_GRANT_ACTIONS="${A1_GRANT_ACTIONS:-github.pull_request.read github.compare.read github.review.post}"
A1_CONCURRENCY_POLICY="${A1_CONCURRENCY_POLICY:-replace}"
# NOTE: the default subject-key template contains "}" characters, which would
# prematurely close a ${VAR:-default} expansion, so it is assigned in two steps.
A1_SUBJECT_KEY_TEMPLATE="${A1_SUBJECT_KEY_TEMPLATE:-}"
if [[ -z "$A1_SUBJECT_KEY_TEMPLATE" ]]; then
  A1_SUBJECT_KEY_TEMPLATE='{{attrs.repo}}#{{attrs.pr_number}}'
fi
A1_RETRY_MAX_ATTEMPTS="${A1_RETRY_MAX_ATTEMPTS:-2}"
A1_RETRY_BACKOFF_SECS="${A1_RETRY_BACKOFF_SECS:-30}"
# Event-type patterns fanned out onto the same binding. They are matched
# against the FULL route key (github.{event}.{action}) segment-by-segment with
# dot-segmented globs (internal/trigger/pattern.go), NOT against the action
# alone — so each pattern is a complete route key. The exact RouteKey
# (github.pull_request.opened) owns ingress; the {a,b} alternation broadens the
# fan-out to synchronize (new pushes), reopened, and A1-7 ready_for_review.
# NOTE: the default contains "}", which would prematurely close a
# ${VAR:-default} expansion, so it is assigned in two steps.
A1_EVENT_PATTERNS="${A1_EVENT_PATTERNS:-}"
if [[ -z "$A1_EVENT_PATTERNS" ]]; then
  A1_EVENT_PATTERNS='github.pull_request.{opened,synchronize,reopened,ready_for_review}'
fi
# loom binary + per-call flags. LOOM_BIN lets the container point at its own
# /usr/local/bin/loom; LOOM_WORKSPACE is also passed as --workspace so the
# active-workspace resolution is deterministic regardless of stored default.
LOOM_BIN="${LOOM_BIN:-loom}"

log() { printf '[a1-setup] %s\n' "$*"; }

die() {
  printf '[a1-setup] error: %s\n' "$*" >&2
  exit 1
}

require_env() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    die "required env ${name} is unset or empty"
  fi
}

# loom_ws runs the loom CLI scoped to the active workspace. --workspace is
# passed explicitly (belt-and-suspenders over LOOM_WORKSPACE) so resolution
# never depends on a stored default.
loom_ws() {
  "$LOOM_BIN" --workspace "$LOOM_WORKSPACE" "$@"
}

# exists_already classifies a captured stderr blob as an already-exists /
# conflict outcome (idempotent re-run is fine) versus a real failure.
exists_already() {
  printf '%s' "$1" | grep -qiE 'already exist|already-exist|conflict|duplicate|exists'
}

# tolerate_exists runs a creating command and treats an
# already-exists/conflict failure as success (idempotent re-run). Any other
# failure aborts. stderr is captured into a variable (no temp files, so no
# rm) so we can classify it; on a tolerated conflict we surface a short note,
# on a real failure we re-emit stderr verbatim and propagate the status.
tolerate_exists() {
  local what="$1"
  shift
  local err status=0
  # Capture stderr into a variable (stdout discarded), no temp file so no rm.
  # The `|| status=$?` keeps `set -e` from aborting on a non-zero CLI exit and
  # records the real exit code (the redirection is not a command, so the
  # command substitution's status is "$@"'s own status).
  err="$( { "$@" 1>/dev/null; } 2>&1 )" || status=$?
  if [[ "$status" -eq 0 ]]; then
    return 0
  fi
  if exists_already "$err"; then
    log "${what} already exists — leaving it in place"
    return 0
  fi
  printf '%s\n' "$err" >&2
  return "$status"
}

create_connector() {
  log "creating github connector ${A1_CONNECTOR_ID} (inbound secret + sealed gh credential)"
  # stdin order matters and is verified against connector_cmd.go:
  #   line 1 = inbound webhook secret   (--inbound-secret-stdin)
  #   line 2 = outbound credential       (--credential-stdin, sealed)
  # printf (not echo) writes exactly the two newline-terminated lines and never
  # exposes the secrets on argv or any process listing. The secrets are read
  # from the (already-exported) shell variables by printf itself, so they never
  # appear as command arguments. stderr is captured into a variable (no temp
  # file, so no rm); the pipe's exit status is the loom create's own status.
  local err status=0
  err="$( { printf '%s\n%s\n' "$A1_WEBHOOK_SECRET" "$GH_TOKEN" \
    | loom_ws connector create \
        --source github \
        --id "$A1_CONNECTOR_ID" \
        --name "$A1_CONNECTOR_NAME" \
        --endpoint-path "$A1_WEBHOOK_ENDPOINT_PATH" \
        --inbound-secret-stdin \
        --credential-stdin \
        1>/dev/null; } 2>&1 )" || status=$?
  if [[ "$status" -eq 0 ]]; then
    return 0
  fi
  if exists_already "$err"; then
    log "connector ${A1_CONNECTOR_ID} already exists — leaving it in place"
    return 0
  fi
  printf '%s\n' "$err" >&2
  return "$status"
}

create_binding() {
  log "creating trigger binding ${A1_BINDING_ID} (route ${A1_ROUTE_KEY} -> ${A1_WORKFLOW_NAME})"
  # The binding's --secret is the pre-connector back-compat fallback: once the
  # connector exists, its inbound secret is the real verification root and this
  # value is never consulted (resolveInboundSecretCandidates prefers the
  # connector). But the CLI still rejects an ENABLED github binding with an
  # empty --secret, so we pass the same webhook secret to satisfy that check.
  # The CLI exposes no stdin path for the binding secret, so it necessarily
  # rides argv here; it equals the connector secret already sealed over stdin,
  # so no NEW secret is exposed.
  local -a pattern_flags=()
  local pattern
  for pattern in $A1_EVENT_PATTERNS; do
    pattern_flags+=(--event-pattern "$pattern")
  done
  tolerate_exists "binding ${A1_BINDING_ID}" \
    loom_ws trigger bindings create \
      --source github \
      --route-key "$A1_ROUTE_KEY" \
      --workflow "$A1_WORKFLOW_NAME" \
      --binding-id "$A1_BINDING_ID" \
      --secret "$A1_WEBHOOK_SECRET" \
      --concurrency-policy "$A1_CONCURRENCY_POLICY" \
      --subject-key-template "$A1_SUBJECT_KEY_TEMPLATE" \
      --retry-max-attempts "$A1_RETRY_MAX_ATTEMPTS" \
      --retry-backoff "$A1_RETRY_BACKOFF_SECS" \
      "${pattern_flags[@]}"
}

create_grant() {
  local resource="repo:${A1_GITHUB_REPO}"
  # Deny-by-default: one grant per action the workflow invokes, each scoped to
  # exactly the one subject repo. No action wildcards; the resource is the
  # exact repo:owner/name (MatchResource is segment-exact). Evaluate requires
  # g.Action == action, so each connector action needs its own grant.
  local action
  for action in $A1_GRANT_ACTIONS; do
    log "granting ${action} on ${resource} to binding ${A1_BINDING_ID}"
    tolerate_exists "grant ${action} for ${A1_BINDING_ID}" \
      loom_ws connector grant create \
        --connector "$A1_CONNECTOR_ID" \
        --binding "$A1_BINDING_ID" \
        --action "$action" \
        --resource "$resource"
  done
}

main() {
  require_env LOOM_WORKSPACE
  require_env A1_GITHUB_REPO
  require_env A1_WEBHOOK_SECRET
  require_env GH_TOKEN
  require_env LOOM_CONNECTOR_VAULT_KEY

  command -v "$LOOM_BIN" >/dev/null 2>&1 || die "loom binary not found: ${LOOM_BIN}"

  # owner/name shape check: the grant resource and the workflow subject both
  # depend on it. A bad shape would silently scope the grant wrong.
  if [[ "$A1_GITHUB_REPO" != */* || "$A1_GITHUB_REPO" == /* || "$A1_GITHUB_REPO" == */ ]]; then
    die "A1_GITHUB_REPO must be owner/name, got: ${A1_GITHUB_REPO}"
  fi

  # Dependency order: connector before grant (grant references the connector)
  # and binding before grant (grant references the binding id).
  create_connector
  create_binding
  create_grant

  log "provisioning complete for workspace ${LOOM_WORKSPACE}: connector=${A1_CONNECTOR_ID} binding=${A1_BINDING_ID} repo=${A1_GITHUB_REPO}"
}

main "$@"
