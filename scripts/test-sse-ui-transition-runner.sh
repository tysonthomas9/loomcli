#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERIFY="$ROOT_DIR/scripts/verify-sse-ui-transition-report.mjs"
CHECK_PROJECT="$ROOT_DIR/scripts/check-sse-ui-compose-project.sh"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "$TMP_ROOT"' EXIT

manifest="$TMP_ROOT/manifest.tsv"
cp "$ROOT_DIR/scripts/sse-ui-transition-manifest.tsv" "$manifest"

write_report() {
  node - "$manifest" "$TMP_ROOT/report.json" "$1" "$2" "$3" <<'JS'
const fs = require("node:fs");
const [, , manifest, destination, t01Status, t02Status, t01Results] = process.argv;
const specs = fs.readFileSync(manifest, "utf8").trim().split("\n").slice(1)
  .filter(line => line.startsWith("mocked\tmocked\t"))
  .map(line => {
    const [, , id, file, marker] = line.split("\t");
    return { file: file.replace(/^tests\/e2e\//, ""), title: `scenario ${marker}`, ok: true,
      tests: [{ expectedStatus: "passed", status: id === "T01" ? t01Status : id === "T02" ? t02Status : "expected",
        annotations: [], results: id === "T01" ? JSON.parse(t01Results) : [{status:"passed"}] }] };
  });
fs.writeFileSync(destination, JSON.stringify({config:{rootDir:"/repo/internal/webui/frontend/tests/e2e"},suites:[{specs}]}));
JS
}

expect_failure() {
  if "$@" >/dev/null 2>&1; then
    echo "expected failure: $*" >&2
    exit 1
  fi
}

write_report expected expected '[{"status":"passed"}]'
node "$VERIFY" --mode list --manifest "$manifest" --tier mocked --backend mocked --report "$TMP_ROOT/report.json" >/dev/null
node "$VERIFY" --mode result --manifest "$manifest" --tier mocked --backend mocked --report "$TMP_ROOT/report.json" >/dev/null

write_report expected expected '[{"status":"passed"},{"status":"passed"}]'
expect_failure node "$VERIFY" --mode result --manifest "$manifest" --tier mocked --backend mocked --report "$TMP_ROOT/report.json"

write_report skipped expected '[{"status":"skipped"}]'
expect_failure node "$VERIFY" --mode result --manifest "$manifest" --tier mocked --backend mocked --report "$TMP_ROOT/report.json"

write_report unexpected expected '[{"status":"failed"}]'
expect_failure node "$VERIFY" --mode result --manifest "$manifest" --tier mocked --backend mocked --report "$TMP_ROOT/report.json"

cat >"$TMP_ROOT/missing.json" <<'EOF'
{"config":{"rootDir":"/repo/internal/webui/frontend/tests/e2e"},"suites":[{"file":"ui-transitions.spec.ts","specs":[
  {"title":"keeps detail @sse-ui-transition @T01","ok":true,"tests":[{"expectedStatus":"passed","status":"expected","annotations":[],"results":[{"status":"passed"}]}]}
]}]}
EOF
expect_failure node "$VERIFY" --mode list --manifest "$manifest" --tier mocked --backend mocked --report "$TMP_ROOT/missing.json"

sed '/mocked.*T07/d' "$manifest" >"$TMP_ROOT/reduced-manifest.tsv"
write_report expected expected '[{"status":"passed"}]'
expect_failure node "$VERIFY" --mode list --manifest "$TMP_ROOT/reduced-manifest.tsv" --tier mocked --backend mocked --report "$TMP_ROOT/report.json"

node -e 'const fs=require("fs"); const p=process.argv[1]; const r=require(p); r.suites[0].specs.push({...r.suites[0].specs[0]}); fs.writeFileSync(p,JSON.stringify(r))' "$TMP_ROOT/report.json"
expect_failure node "$VERIFY" --mode list --manifest "$manifest" --tier mocked --backend mocked --report "$TMP_ROOT/report.json"

write_report expected expected '[{"status":"passed"}]'
node -e 'const fs=require("fs"); const p=process.argv[1]; const r=require(p); r.suites[0].specs[0].file="wrong.spec.ts"; fs.writeFileSync(p,JSON.stringify(r))' "$TMP_ROOT/report.json"
expect_failure node "$VERIFY" --mode list --manifest "$manifest" --tier mocked --backend mocked --report "$TMP_ROOT/report.json"

write_report expected expected '[{"status":"passed"}]'
node -e 'const fs=require("fs"); const p=process.argv[1]; const r=require(p); r.errors=[{message:"global teardown failed"}]; fs.writeFileSync(p,JSON.stringify(r))' "$TMP_ROOT/report.json"
expect_failure node "$VERIFY" --mode result --manifest "$manifest" --tier mocked --backend mocked --report "$TMP_ROOT/report.json"

# A suffix is a distinct case, never a match for its shorter parent marker.
write_report expected expected '[{"status":"passed"}]'
node -e 'const fs=require("fs"); const p=process.argv[1]; const r=require(p); r.suites[0].specs[0].title += "-EXTRA"; fs.writeFileSync(p,JSON.stringify(r))' "$TMP_ROOT/report.json"
expect_failure node "$VERIFY" --mode list --manifest "$manifest" --tier mocked --backend mocked --report "$TMP_ROOT/report.json"

sed '1s/2/1/' "$manifest" >"$TMP_ROOT/old-manifest.tsv"
expect_failure node "$VERIFY" --mode list --manifest "$TMP_ROOT/old-manifest.tsv" --tier mocked --backend mocked --report "$TMP_ROOT/report.json"

expect_failure bash "$ROOT_DIR/scripts/test-sse-ui-transitions.sh" --backend invalid
expect_failure bash "$ROOT_DIR/scripts/test-sse-ui-transitions.sh" --backend mocked --run-id 'bad/id'

fake_engine="$TMP_ROOT/fake-engine"
cat >"$fake_engine" <<'EOF'
#!/usr/bin/env bash
if [[ "${FAIL_DISCOVERY:-}" == "$1" ]]; then exit 42; fi
if [[ "${REPORT_RESOURCE:-}" == "$1" ]]; then echo owned-resource; fi
EOF
chmod +x "$fake_engine"
project=loomcli-sse-ui-redis-selftest
"$CHECK_PROJECT" "$fake_engine" "$project"
expect_failure env FAIL_DISCOVERY=volume "$CHECK_PROJECT" "$fake_engine" "$project"
expect_failure env REPORT_RESOURCE=network "$CHECK_PROJECT" "$fake_engine" "$project"

echo "sse UI transition runner self-tests passed"
