#!/usr/bin/env bash
# Capture a repeatable agent-browser frontend trace window and summarize OTEL span fanout.

set -euo pipefail

URL="${1:-${URL:-http://127.0.0.1:18889/ws/LOCALMODE/kanban}}"
SESSION="${AGENT_BROWSER_SESSION:-loom-otel-clean}"
WINDOW_SECONDS="${WINDOW_SECONDS:-45}"
JAEGER_URL="${JAEGER_URL:-http://127.0.0.1:16686}"
OTEL_SERVICE="${OTEL_SERVICE:-loom-serve}"
JAEGER_LIMIT="${JAEGER_LIMIT:-2000}"
OUT_DIR="${OUT_DIR:-/tmp}"
FLEET_HOST_REGEX="${FLEET_HOST_REGEX:-127\\.0\\.0\\.1:8280}"
CLOSE_SESSIONS="${CLOSE_SESSIONS:-loom-otel-clean,loom-otel-clean1,loom-otel-clean2,loom-otel-clean3,loom-otel-clean4,loom-otel-clean5,$SESSION}"
KEEP_BROWSER="${KEEP_BROWSER:-0}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 127
  }
}

need agent-browser
need curl
need jq
need python3

APP_PORT="${APP_PORT:-$(python3 - "$URL" <<'PY'
from urllib.parse import urlparse
import sys

parsed = urlparse(sys.argv[1])
print(parsed.port or (443 if parsed.scheme == "https" else 80))
PY
)}"

now_us() {
  python3 - <<'PY'
import time

print(int(time.time() * 1_000_000))
PY
}

close_session() {
  local session="$1"
  [[ -n "$session" ]] || return 0
  agent-browser --session "$session" close >/dev/null 2>&1 || true
}

IFS=',' read -r -a sessions_to_close <<<"$CLOSE_SESSIONS"
for old_session in "${sessions_to_close[@]}"; do
  close_session "$old_session"
done

safe_session="${SESSION//[^A-Za-z0-9_.-]/_}"
run_id="$(date -u +%Y%m%dT%H%M%SZ)-$safe_session"
trace_file="$OUT_DIR/loom-agent-browser-$run_id-jaeger.json"
snapshot_file="$OUT_DIR/loom-agent-browser-$run_id-snapshot.txt"
report_file="$OUT_DIR/loom-agent-browser-$run_id-report.json"

start_us="$(now_us)"
agent-browser --session "$SESSION" open "$URL"
agent-browser --session "$SESSION" wait 2000 >/dev/null 2>&1 || true
agent-browser --session "$SESSION" snapshot -i >"$snapshot_file" || true
sleep "$WINDOW_SECONDS"
end_us="$(now_us)"

curl -sS "${JAEGER_URL%/}/api/traces?service=$OTEL_SERVICE&start=$start_us&end=$end_us&limit=$JAEGER_LIMIT" -o "$trace_file"

jq \
  --arg url "$URL" \
  --arg session "$SESSION" \
  --arg service "$OTEL_SERVICE" \
  --arg appPort "$APP_PORT" \
  --arg fleet "$FLEET_HOST_REGEX" \
  --argjson start "$start_us" \
  --argjson end "$end_us" \
  --argjson window "$WINDOW_SECONDS" '
  def tag($k): ([.tags[]? | select(.key == $k) | .value][0] // "");
  def first_tag($keys):
    reduce $keys[] as $k (""; if . == "" then tag($k) else . end);
  def norm_kind: (.kind | tostring | ascii_downcase);
  def is_app_server:
    .service == $service and norm_kind == "server" and .server_port == $appPort;
  def is_business_app_server:
    is_app_server and ((.path | test("^/(assets|fonts)/|^/favicon|^/api/csp-report")) | not);
  def is_fleet_client:
    .service == $service and norm_kind == "client" and
    ((.url | test($fleet)) or ((.server_address + ":" + .server_port) | test($fleet)));
  def route_key:
    if .path != "" and .method != "" then .method + " " + .path
    elif .path != "" then .path
    else .operation
    end;
  def top_counts($items):
    $items | group_by(.) | map({name: .[0], count: length}) | sort_by(-.count, .name) | .[:20];

  [
    .data[] as $trace
    | $trace.spans[]
    | . as $span
    | {
        trace_id: $trace.traceID,
        span_id: $span.spanID,
        operation: ($span.operationName // ""),
        service: ($trace.processes[$span.processID].serviceName // ""),
        start_time: $span.startTime,
        duration: $span.duration,
        method: (first_tag(["http.request.method", "http.method"]) | tostring),
        kind: (tag("span.kind") | tostring),
        url: (tag("url.full") | tostring),
        path: (first_tag(["url.path", "http.target", "http.route"]) | tostring),
        server_address: (first_tag(["server.address", "net.host.name"]) | tostring),
        server_port: (first_tag(["server.port", "net.host.port", "network.peer.port"]) | tostring)
      }
  ] as $spans
  | {
      run: {
        url: $url,
        session: $session,
        service: $service,
        app_port: $appPort,
        fleet_host_regex: $fleet,
        start_unix_micros: $start,
        end_unix_micros: $end,
        requested_window_seconds: $window
      },
      counts: {
        traces: (.data | length),
        spans: ($spans | length),
        service_spans: ($spans | map(select(.service == $service)) | length),
        app_server_spans: ($spans | map(select(is_app_server)) | length),
        business_app_server_spans: ($spans | map(select(is_business_app_server)) | length),
        fleet_http_client_spans: ($spans | map(select(is_fleet_client)) | length)
      },
      first_app_server_start: ($spans | map(select(is_app_server) | .start_time) | min),
      top_app_routes: top_counts($spans | map(select(is_app_server) | route_key)),
      top_business_app_routes: top_counts($spans | map(select(is_business_app_server) | route_key)),
      top_fleet_urls: top_counts($spans | map(select(is_fleet_client) | .url)),
      top_operations: top_counts($spans | map(select(.service == $service) | .operation))
    }
  ' "$trace_file" >"$report_file"

if [[ "$KEEP_BROWSER" != "1" ]]; then
  close_session "$SESSION"
fi

echo "trace_json=$trace_file"
echo "snapshot=$snapshot_file"
echo "report_json=$report_file"
jq '.counts, {top_business_app_routes: .top_business_app_routes[:10], top_fleet_urls: .top_fleet_urls[:10]}' "$report_file"
