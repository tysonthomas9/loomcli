#!/usr/bin/env bash
# test-a1-live-review.sh — drive the A1 github-review-agent stack against the
# REAL open PR and confirm the agent posts a COMMENT review.
#
# Flow:
#   1. bring the stack up via scripts/run-github-review-codex-stack.sh (which
#      provisions the connector/grant/binding and seals the sealed gh token +
#      a generated inbound webhook secret) — capturing the resolved inbound
#      secret so we can sign with the SAME value the connector verifies against;
#   2. snapshot the PR's existing reviews (baseline) so we detect only a NEW one;
#   3. fetch the PR's real data (gh api …/pulls/N), reshape it into a github
#      `pull_request.opened` webhook event, and POST it to the container's
#      inbound webhook endpoint with a valid HMAC-sha256 signature in the
#      X-Hub-Signature-256 header (scheme read from
#      internal/webui/handlers/webhooks/github.go: sha256= + hex(HMAC(body)));
#   4. poll `gh api …/pulls/N/reviews` until a NEW COMMENT review appears, or
#      a hard timeout fires.
#
# Teardown ALWAYS runs `podman rm -f <container>` (podman's own container
# remove) — never shell `rm` on files. Secrets ride env/stdin, never argv.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STACK_SCRIPT="${STACK_SCRIPT:-${ROOT_DIR}/scripts/run-github-review-codex-stack.sh}"

CONTAINER="${CONTAINER:-loom-github-review}"
HOST_PORT="${HOST_PORT:-8093}"
BASE_URL="${BASE_URL:-http://localhost:${HOST_PORT}}"
WORKSPACE_ID="${WORKSPACE_ID:-GITHUB-REVIEW}"
A1_WEBHOOK_ENDPOINT_PATH="${A1_WEBHOOK_ENDPOINT_PATH:-/webhooks/github}"
WEBHOOK_URL="${WEBHOOK_URL:-${BASE_URL}/api/workspaces/${WORKSPACE_ID}${A1_WEBHOOK_ENDPOINT_PATH}}"

A1_GITHUB_REPO="${A1_GITHUB_REPO:-tysonthomas9/loom-review-sandbox}"
A1_PR_NUMBER="${A1_PR_NUMBER:-1}"

# How long to wait for the agent's COMMENT review to land (seconds), and the
# poll interval. The full path is webhook -> dispatch -> driver run -> codex
# task run -> connector post, so allow generous headroom.
REVIEW_TIMEOUT_SECS="${REVIEW_TIMEOUT_SECS:-600}"
REVIEW_POLL_SECS="${REVIEW_POLL_SECS:-10}"

# SECRETS_FILE is where the stack writes the resolved inbound webhook secret so
# this driver can sign with the exact value the connector verifies against. It
# is created with mktemp and only ever contains the non-sensitive-by-itself
# webhook HMAC secret + the vault key; it is cleaned up by the OS temp reaper
# (this script never runs shell rm).
SECRETS_FILE=""
A1_WEBHOOK_SECRET="${A1_WEBHOOK_SECRET:-}"

log() {
  printf '[a1-live-test] %s\n' "$*"
}

require_cmd() {
  local name="$1"
  if ! command -v "$name" >/dev/null 2>&1; then
    printf 'missing required command: %s\n' "$name" >&2
    exit 1
  fi
}

require_path() {
  if [[ ! -e "$1" ]]; then
    printf 'missing required path: %s\n' "$1" >&2
    exit 1
  fi
}

# teardown is registered via `trap teardown EXIT INT TERM`; shellcheck cannot
# see the indirect invocation through trap.
# shellcheck disable=SC2329
teardown() {
  local status=$?
  log "tearing down container ${CONTAINER}"
  # podman's own container remove — NOT shell rm on files.
  podman rm -f "$CONTAINER" >/dev/null 2>&1 || true
  exit "$status"
}

# fetch_review_ids prints the PR's current review ids (one per line). Used to
# baseline before firing and to detect a NEW review after.
fetch_review_ids() {
  gh api "repos/${A1_GITHUB_REPO}/pulls/${A1_PR_NUMBER}/reviews" --paginate \
    --jq '.[].id' 2>/dev/null || true
}

# build_event reshapes the live PR JSON (on stdin) into a github
# pull_request.opened webhook event body and prints it on stdout. The adapter
# (internal/webui/handlers/webhooks/github.go) reads action, number,
# pull_request.{number,draft,head.sha,base.ref}, repository.full_name and
# sender.login — so the event carries exactly those, sourced from the real PR.
build_event() {
  node -e '
const fs = require("node:fs");
const pr = JSON.parse(fs.readFileSync(0, "utf8"));
const event = {
  action: "opened",
  number: pr.number,
  pull_request: {
    number: pr.number,
    draft: Boolean(pr.draft),
    state: pr.state,
    title: pr.title,
    head: { sha: pr.head && pr.head.sha, ref: pr.head && pr.head.ref },
    base: { ref: pr.base && pr.base.ref, sha: pr.base && pr.base.sha },
  },
  repository: { full_name: pr.base && pr.base.repo && pr.base.repo.full_name },
  sender: { login: (pr.user && pr.user.login) || "loom-review-bot" },
};
if (!event.repository.full_name) {
  event.repository.full_name = process.env.A1_GITHUB_REPO || "";
}
process.stdout.write(JSON.stringify(event));
'
}

# sign_body computes the X-Hub-Signature-256 header value for the body on
# stdin, reading the secret from A1_WEBHOOK_SECRET (env, never argv). Matches
# githubSignature(): "sha256=" + hex(HMAC_SHA256(secret, body)).
sign_body() {
  node -e '
const crypto = require("node:crypto");
const fs = require("node:fs");
const body = fs.readFileSync(0);
const secret = process.env.A1_WEBHOOK_SECRET || "";
const mac = crypto.createHmac("sha256", secret).update(body).digest("hex");
process.stdout.write("sha256=" + mac);
'
}

# wait_for_review polls the PR reviews until a review id not present in the
# baseline appears with state COMMENTED, or the hard timeout fires.
wait_for_review() {
  local baseline="$1"
  local deadline now reviews_json new_comment
  deadline=$(( $(date +%s) + REVIEW_TIMEOUT_SECS ))
  while :; do
    reviews_json="$(gh api "repos/${A1_GITHUB_REPO}/pulls/${A1_PR_NUMBER}/reviews" --paginate 2>/dev/null || echo '[]')"
    # A NEW review whose id is not in the baseline and whose state is COMMENTED.
    new_comment="$(BASELINE="$baseline" node -e '
const fs = require("node:fs");
const reviews = JSON.parse(fs.readFileSync(0, "utf8"));
const baseline = new Set(String(process.env.BASELINE || "").split(/\s+/).filter(Boolean));
const hit = reviews.find((r) => r && r.state === "COMMENTED" && !baseline.has(String(r.id)));
if (hit) {
  process.stdout.write(JSON.stringify({ id: hit.id, user: hit.user && hit.user.login, url: hit.html_url }));
}
' <<<"$reviews_json")"
    if [[ -n "$new_comment" ]]; then
      log "agent COMMENT review detected: ${new_comment}"
      return 0
    fi
    now=$(date +%s)
    if (( now >= deadline )); then
      log "timed out after ${REVIEW_TIMEOUT_SECS}s waiting for a new COMMENT review"
      log "recent container logs:"
      podman logs --tail 80 "$CONTAINER" 2>&1 || true
      return 1
    fi
    sleep "$REVIEW_POLL_SECS"
  done
}

main() {
  require_cmd gh
  require_cmd node
  require_cmd curl
  require_cmd podman
  require_cmd date
  require_path "$STACK_SCRIPT"

  trap teardown EXIT INT TERM

  # 1. Bring the stack up, capturing the resolved inbound webhook secret so we
  #    sign with the exact value the connector verifies against.
  SECRETS_FILE="$(mktemp "${TMPDIR:-/tmp}/a1-live-secrets.XXXXXX")"
  log "starting stack via ${STACK_SCRIPT} (container ${CONTAINER})"
  A1_SECRETS_OUT="$SECRETS_FILE" \
  CONTAINER="$CONTAINER" \
  HOST_PORT="$HOST_PORT" \
  BASE_URL="$BASE_URL" \
  WORKSPACE_ID="$WORKSPACE_ID" \
  A1_GITHUB_REPO="$A1_GITHUB_REPO" \
  A1_WEBHOOK_ENDPOINT_PATH="$A1_WEBHOOK_ENDPOINT_PATH" \
    bash "$STACK_SCRIPT"

  # The stack wrote A1_WEBHOOK_SECRET=… into SECRETS_FILE. Source it only if the
  # operator did not already pin A1_WEBHOOK_SECRET in the environment.
  if [[ -z "$A1_WEBHOOK_SECRET" && -s "$SECRETS_FILE" ]]; then
    # shellcheck disable=SC1090
    A1_WEBHOOK_SECRET="$(grep '^A1_WEBHOOK_SECRET=' "$SECRETS_FILE" | head -1 | cut -d= -f2-)"
  fi
  if [[ -z "$A1_WEBHOOK_SECRET" ]]; then
    printf 'could not resolve the inbound webhook secret to sign with\n' >&2
    exit 1
  fi
  export A1_WEBHOOK_SECRET A1_GITHUB_REPO

  # 2. Baseline the existing reviews so we detect only a freshly posted one.
  local baseline_ids
  baseline_ids="$(fetch_review_ids | tr '\n' ' ')"
  log "baseline reviews on ${A1_GITHUB_REPO}#${A1_PR_NUMBER}: [${baseline_ids}]"

  # 3. Fetch the real PR, reshape into a pull_request.opened event, sign, POST.
  local pr_json event_body signature delivery_id http_status
  pr_json="$(gh api "repos/${A1_GITHUB_REPO}/pulls/${A1_PR_NUMBER}")"
  event_body="$(printf '%s' "$pr_json" | build_event)"
  if [[ -z "$event_body" ]]; then
    printf 'failed to build webhook event body from PR JSON\n' >&2
    exit 1
  fi
  signature="$(printf '%s' "$event_body" | sign_body)"
  delivery_id="a1-live-$(date +%s)-$$"

  log "POSTing signed pull_request.opened to ${WEBHOOK_URL}"
  http_status="$(printf '%s' "$event_body" | curl -sS -o /dev/null -w '%{http_code}' \
    -X POST "$WEBHOOK_URL" \
    -H "Content-Type: application/json" \
    -H "X-GitHub-Event: pull_request" \
    -H "X-GitHub-Delivery: ${delivery_id}" \
    -H "X-Hub-Signature-256: ${signature}" \
    --data-binary @-)"
  log "webhook ingress responded HTTP ${http_status}"
  case "$http_status" in
    202 | 200) : ;;
    *)
      printf 'webhook ingress rejected the event (HTTP %s); aborting\n' "$http_status" >&2
      podman logs --tail 80 "$CONTAINER" 2>&1 || true
      exit 1
      ;;
  esac

  # 4. Poll for the new COMMENT review.
  log "polling for a new COMMENT review (timeout ${REVIEW_TIMEOUT_SECS}s)"
  if wait_for_review "$baseline_ids"; then
    log "PASS: A1 github-review-agent posted a COMMENT review on ${A1_GITHUB_REPO}#${A1_PR_NUMBER}"
    exit 0
  fi
  printf 'FAIL: no COMMENT review appeared within the timeout\n' >&2
  exit 1
}

main "$@"
