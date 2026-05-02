/**
 * Pre-flight checks: prove reference (:8081) and fleet (:8082) really are on
 * different backends and that fleet-side writes actually route to fleet-db
 * before any spec runs. If ANY check fails, the entire suite aborts — we
 * do NOT support a "skip because no stack" mode, since that's a silent
 * fallback vector (see ui-test-plan.md §0).
 *
 * Side effects:
 *   - Mutates webui-gaps.md Step 0 table with actual/pass-fail columns
 *   - Writes a JSON preflight report to artifacts/reports/preflight.json
 *   - Throws on failure (aborts entire suite)
 */
import * as fs from "node:fs/promises";
import * as path from "node:path";
import { execSync } from "node:child_process";
import { PARITY_URLS } from "../playwright.config";
import { composeRun, composeRuntime, PARITY_CONTAINER_PREFIX } from "./compose";
import { discoverWorkspaceId } from "./backends";
import { isFleetOnlyMode } from "./mode";

const WEBUI_GAPS_PATH = path.resolve(
  __dirname,
  "../../../../docs/design/parity-report-2026-04-22/webui-gaps.md",
);
const PREFLIGHT_JSON = path.resolve(
  __dirname,
  "../artifacts/reports/preflight.json",
);

export interface PreflightCheck {
  name: string;
  expected: string;
  actual: string;
  pass: boolean;
  error?: string;
}

export interface PreflightResult {
  started_at: string;
  finished_at: string;
  all_passed: boolean;
  checks: PreflightCheck[];
  env_snapshot: Record<string, string>;
}

// Cached across the whole run — preflight must run exactly once via beforeAll.
let cached: PreflightResult | null = null;

// 15s default — long enough to absorb a cold-start fleet-db connection pool
// on the first preflight probe while still failing fast if the service is
// actually wedged. Earlier 5s timeout was tripping the AbortController on
// the first call after a fresh `compose up` even though every subsequent
// request landed in <10ms.
async function httpJson(
  url: string,
  timeoutMs = 15000,
  headers?: Record<string, string>,
): Promise<any> {
  const controller = new AbortController();
  const t = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const r = await fetch(url, { signal: controller.signal, headers });
    if (!r.ok) throw new Error(`HTTP ${r.status} on ${url}`);
    return await r.json();
  } finally {
    clearTimeout(t);
  }
}

async function httpPost(
  url: string,
  body: unknown,
  timeoutMs = 15000,
): Promise<{ status: number; body: any }> {
  const controller = new AbortController();
  const t = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const r = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
      signal: controller.signal,
    });
    let payload: any = null;
    const text = await r.text();
    try {
      payload = text ? JSON.parse(text) : null;
    } catch {
      payload = text;
    }
    return { status: r.status, body: payload };
  } finally {
    clearTimeout(t);
  }
}

function dockerExec(service: string, cmd: string): string {
  try {
    return execSync(
      composeRun(`exec -T ${service} sh -c ${JSON.stringify(cmd)}`),
      {
        cwd: path.resolve(__dirname, "../../../.."),
        encoding: "utf-8",
        timeout: 10_000,
      },
    ).trim();
  } catch (e: any) {
    throw new Error(`docker exec failed for ${service}: ${e?.message ?? e}`);
  }
}

async function fleetDBLogTail(linesFromEnd = 50): Promise<string> {
  try {
    return execSync(composeRun(`logs --tail=${linesFromEnd} fleet-db 2>&1`), {
      cwd: path.resolve(__dirname, "../../../.."),
      encoding: "utf-8",
      timeout: 10_000,
    });
  } catch (e) {
    return "";
  }
}

/**
 * Fetch fleet-db log lines emitted after the given timestamp. Used in
 * preflight's POST probe — `fleetDBLogTail(N)` is unsafe under steady-state
 * SSE polling because the per-second log volume routinely exceeds N within
 * one tick of the probe loop, so a fixed line window can't span the
 * "before the POST → after the POST" boundary.
 */
async function fleetDBLogSince(sinceISO: string): Promise<string> {
  try {
    return execSync(composeRun(`logs --since=${sinceISO} fleet-db 2>&1`), {
      cwd: path.resolve(__dirname, "../../../.."),
      encoding: "utf-8",
      timeout: 10_000,
    });
  } catch (e) {
    return "";
  }
}

async function runAllChecks(): Promise<PreflightResult> {
  const started = new Date().toISOString();
  const checks: PreflightCheck[] = [];
  const fleetOnly = isFleetOnlyMode();

  // 1. reference config
  if (fleetOnly) {
    checks.push({
      name: "GET :8081/api/config .issue_backend",
      expected: "skipped in fleet-only mode",
      actual: "skipped",
      pass: true,
    });
  } else {
    try {
      const j = await httpJson(`${PARITY_URLS.reference}/api/config`);
      const backend = j.issue_backend ?? j.backend ?? "(missing)";
      checks.push({
        name: "GET :8081/api/config .issue_backend",
        expected: "reference",
        actual: String(backend),
        pass: backend === "reference",
      });
    } catch (e: any) {
      checks.push({
        name: "GET :8081/api/config .issue_backend",
        expected: "reference",
        actual: "UNREACHABLE",
        pass: false,
        error: e?.message ?? String(e),
      });
    }
  }

  // 2. fleet config
  try {
    const j = await httpJson(`${PARITY_URLS.fleet}/api/config`);
    const backend = j.issue_backend ?? j.backend ?? "(missing)";
    checks.push({
      name: "GET :8082/api/config .issue_backend",
      expected: "fleet",
      actual: String(backend),
      pass: backend === "fleet",
    });
  } catch (e: any) {
    checks.push({
      name: "GET :8082/api/config .issue_backend",
      expected: "fleet",
      actual: "UNREACHABLE",
      pass: false,
      error: e?.message ?? String(e),
    });
  }

  // 3. Container healthchecks. Bypass compose: podman-compose 1.0.x
  // rejects `ps --format json`. The runtime's own ps with a name filter
  // works on both podman and docker.
  try {
    const ps = execSync(
      `${composeRuntime()} ps --filter name=${PARITY_CONTAINER_PREFIX} --format '{{.Names}}|{{.Status}}'`,
      {
        cwd: path.resolve(__dirname, "../../../.."),
        encoding: "utf-8",
        timeout: 10_000,
      },
    );
    const services: { name: string; status: string }[] = ps
      .split("\n")
      .filter((l) => l.trim().length > 0)
      .map((l) => {
        const [name, ...rest] = l.split("|");
        return { name, status: rest.join("|") };
      });
    const healthFor = (svc: string) =>
      services.find((s) => s.name.includes(svc))?.status ?? "(not found)";
    const fleetHealth = healthFor("loom-fleet");
    const referenceHealth = fleetOnly ? "skipped" : healthFor("loom-reference");
    const fleetDbHealth = healthFor("fleet-db");
    const required = fleetOnly
      ? [fleetHealth, fleetDbHealth]
      : [fleetHealth, referenceHealth, fleetDbHealth];
    const allHealthy = required.every(
      (h) => typeof h === "string" && /healthy|running|Up/i.test(h),
    );
    checks.push({
      name: "Container healthchecks all green",
      expected: fleetOnly
        ? "healthy (loom-fleet, fleet-db); loom-reference not required"
        : "healthy (loom-reference, loom-fleet, fleet-db)",
      actual: `loom-reference=${referenceHealth} loom-fleet=${fleetHealth} fleet-db=${fleetDbHealth}`,
      pass: allHealthy,
    });
  } catch (e: any) {
    checks.push({
      name: "Container healthchecks all green",
      expected: "healthy",
      actual: "container ps failed",
      pass: false,
      error: e?.message ?? String(e),
    });
  }

  // 4. Probe POST to :8082 must cause fleet-db to log POST <ws>/issues.
  //
  // Poll the log tail rather than sleeping a fixed 2s because CI can
  // take longer to flush stdout on busy runners — a fixed sleep produces
  // false-negatives that abort the whole suite. Success detection breaks
  // out of the poll as soon as the log entry lands; the total budget
  // (5s) is the fail threshold.
  try {
    const fleetWs = await discoverWorkspaceId(PARITY_URLS.fleet);
    // Stamp T0 just before the POST so the log query bounds itself to the
    // probe window. Subtract 1s so a slightly-skewed container clock or a
    // log line that lands "at" T0 isn't excluded.
    const probeStart = new Date(Date.now() - 1000).toISOString();
    const probe = await httpPost(
      `${PARITY_URLS.fleet}/api/workspaces/${fleetWs}/issues`,
      {
        title: `parity-preflight-${Date.now()}`,
        issue_type: "task",
        priority: 3,
        description: "preflight routing probe — safe to delete",
      },
    );
    // fleet-db emits structured JSON logs, not classic Apache-style lines.
    const inboundPath = `/api/v1/${PARITY_URLS.workspace}/issues`;
    const escaped = inboundPath.replace(/\//g, "\\/");
    const sawInboundPattern = (logBlob: string) =>
      new RegExp(`"method":"POST".*"path":"${escaped}"`).test(logBlob) ||
      new RegExp(`"path":"${escaped}".*"method":"POST"`).test(logBlob) ||
      new RegExp(`POST\\s+${escaped}`).test(logBlob);

    const pollDeadline = Date.now() + 5000;
    let sawInbound = false;
    while (Date.now() < pollDeadline) {
      const since = await fleetDBLogSince(probeStart);
      if (sawInboundPattern(since)) {
        sawInbound = true;
        break;
      }
      await new Promise((r) => setTimeout(r, 200));
    }
    checks.push({
      name: "Probe POST to :8082 shows up in fleet-db logs",
      expected: "yes",
      actual: sawInbound
        ? `yes — fleet-db received POST ${inboundPath}`
        : `no (probe status=${probe.status})`,
      pass: sawInbound,
    });
  } catch (e: any) {
    checks.push({
      name: "Probe POST to :8082 shows up in fleet-db logs",
      expected: "yes",
      actual: "probe failed",
      pass: false,
      error: e?.message ?? String(e),
    });
  }

  // 5. loom-fleet env LOOM_FLEET_URL
  try {
    const v = dockerExec("loom-fleet", "printenv LOOM_FLEET_URL");
    checks.push({
      name: "loom-fleet env LOOM_FLEET_URL",
      expected: "http://fleet-db:8080",
      actual: v || "(empty)",
      pass: v === "http://fleet-db:8080",
    });
  } catch (e: any) {
    checks.push({
      name: "loom-fleet env LOOM_FLEET_URL",
      expected: "http://fleet-db:8080",
      actual: "docker exec failed",
      pass: false,
      error: e?.message ?? String(e),
    });
  }

  // 6. loom-fleet env LOOM_WORKSPACE
  try {
    const v = dockerExec("loom-fleet", "printenv LOOM_WORKSPACE");
    checks.push({
      name: "loom-fleet env LOOM_WORKSPACE",
      expected: "PARITY",
      actual: v || "(empty)",
      pass: v === "PARITY",
    });
  } catch (e: any) {
    checks.push({
      name: "loom-fleet env LOOM_WORKSPACE",
      expected: "PARITY",
      actual: "docker exec failed",
      pass: false,
      error: e?.message ?? String(e),
    });
  }

  // 7. fleet-db has workspace PARITY.
  // fleet-db's admin/workspaces requires authentication; under
  // --auth-dev-mode it accepts X-Actor as the authenticated identity.
  // Without it the request 401s before reaching the workspace lookup.
  try {
    const j = await httpJson(
      `${PARITY_URLS.fleetDB}/api/v1/admin/workspaces`,
      5000,
      { "X-Actor": "parity-harness" },
    );
    const list: any[] = j?.data ?? j?.workspaces ?? (Array.isArray(j) ? j : []);
    const keys: string[] = list.map(
      (w: any) => w.key ?? w.Key ?? w.name ?? w.Name,
    );
    const has = keys.includes("PARITY");
    checks.push({
      name: "fleet-db has workspace PARITY",
      expected: "yes",
      actual: has ? "yes" : `no (found: ${keys.join(",") || "(none)"})`,
      pass: has,
    });
  } catch (e: any) {
    checks.push({
      name: "fleet-db has workspace PARITY",
      expected: "yes",
      actual: "admin/workspaces unreachable",
      pass: false,
      error: e?.message ?? String(e),
    });
  }

  // 8/9. Settings page backend indicator — asserted via /api/config since
  // that's what the Settings page reads. Two separate table rows.
  if (fleetOnly) {
    checks.push({
      name: "Settings page shows 'reference' on :8081",
      expected: "skipped in fleet-only mode",
      actual: "skipped",
      pass: true,
    });
  } else {
    try {
      const j = await httpJson(`${PARITY_URLS.reference}/api/config`);
      const b = j.issue_backend ?? j.backend ?? "(missing)";
      checks.push({
        name: "Settings page shows 'reference' on :8081",
        expected: "yes",
        actual: b === "reference" ? "yes" : `no (${b})`,
        pass: b === "reference",
      });
    } catch (e: any) {
      checks.push({
        name: "Settings page shows 'reference' on :8081",
        expected: "yes",
        actual: "UNREACHABLE",
        pass: false,
        error: e?.message ?? String(e),
      });
    }
  }
  try {
    const j = await httpJson(`${PARITY_URLS.fleet}/api/config`);
    const f = j.issue_backend ?? j.backend ?? "(missing)";
    checks.push({
      name: "Settings page shows 'fleet' on :8082",
      expected: "yes",
      actual: f === "fleet" ? "yes" : `no (${f})`,
      pass: f === "fleet",
    });
  } catch (e: any) {
    checks.push({
      name: "Settings page shows 'fleet' on :8082",
      expected: "yes",
      actual: "UNREACHABLE",
      pass: false,
      error: e?.message ?? String(e),
    });
  }

  return {
    started_at: started,
    finished_at: new Date().toISOString(),
    all_passed: checks.every((c) => c.pass),
    checks,
    env_snapshot: {
      LOOM_REFERENCE_URL: PARITY_URLS.reference,
      LOOM_FLEET_URL: PARITY_URLS.fleet,
      FLEET_DB_URL: PARITY_URLS.fleetDB,
      PARITY_WORKSPACE: PARITY_URLS.workspace,
      PARITY_MODE: PARITY_URLS.mode,
    },
  };
}

/**
 * Rewrites the Step 0 table in webui-gaps.md with actual preflight results.
 * Safe to call even if the file has been hand-edited between runs — we
 * match on the column `Check` name, not line numbers.
 */
async function writeStepZeroTable(result: PreflightResult) {
  let md: string;
  try {
    md = await fs.readFile(WEBUI_GAPS_PATH, "utf-8");
  } catch {
    return; // File missing; the preflight result still lands in JSON.
  }

  // Map check name (or stable prefix) -> row update.
  const map = new Map<string, PreflightCheck>();
  for (const c of result.checks) map.set(c.name, c);

  const lines = md.split("\n");
  const out: string[] = [];
  let inStep0 = false;
  for (const line of lines) {
    if (/^##\s+Step\s+0:/.test(line)) inStep0 = true;
    else if (inStep0 && /^##\s/.test(line)) inStep0 = false;

    if (
      inStep0 &&
      /^\|\s.+\|\s.+\|\s.+\|\s.+\|/.test(line) &&
      !/^\|\s*---/.test(line)
    ) {
      // Skip header row
      if (/^\|\s*Check\s*\|/.test(line)) {
        out.push(line);
        continue;
      }
      // Try match first column (Check) against known check names.
      const cols = line
        .split("|")
        .slice(1, -1)
        .map((c) => c.trim());
      if (cols.length >= 4) {
        const checkCol = cols[0];
        // Normalize both sides for fuzzy matching — strip markdown
        // backticks and normalize quote style so "reference" and 'reference'
        // compare equal. The source table uses straight double quotes
        // while our check-name strings use straight single quotes.
        const norm = (s: string) =>
          s
            .replace(/[`]/g, "")
            .replace(/[“”""]/g, '"')
            .replace(/[‘’'']/g, "'")
            .replace(/["']/g, "")
            .trim();
        const cn = norm(checkCol);
        let match: PreflightCheck | undefined;
        for (const [key, v] of map) {
          const kn = norm(key);
          if (
            cn === kn ||
            cn.includes(kn) ||
            kn.includes(cn) ||
            (cn.includes("config") && kn.includes(cn.split("/api")[1] ?? ""))
          ) {
            match = v;
            break;
          }
        }
        if (match) {
          cols[2] = match.actual;
          cols[3] = match.pass ? "PASS" : "FAIL";
          out.push(`| ${cols.join(" | ")} |`);
          continue;
        }
      }
    }
    out.push(line);
  }

  // Also append a timestamped footer so human readers can confirm
  // the table reflects the latest run.
  const updated = out.join("\n");
  const footer = `\n<!-- preflight: ${result.finished_at} all_passed=${result.all_passed} -->\n`;
  const withoutOldFooter = updated.replace(/\n<!-- preflight:.*?-->\n?/g, "");
  await fs.writeFile(WEBUI_GAPS_PATH, withoutOldFooter + footer);
}

async function writeJsonReport(result: PreflightResult) {
  await fs.mkdir(path.dirname(PREFLIGHT_JSON), { recursive: true });
  await fs.writeFile(PREFLIGHT_JSON, JSON.stringify(result, null, 2));
}

/**
 * Run exactly once for the whole suite (beforeAll). Subsequent calls
 * return the cached result. Throws on ANY failed check — suite aborts.
 */
export async function preflight(): Promise<PreflightResult> {
  if (cached) {
    if (!cached.all_passed) {
      throw preflightError(cached);
    }
    return cached;
  }
  const result = await runAllChecks();
  cached = result;
  await writeJsonReport(result);
  if (!isFleetOnlyMode()) {
    await writeStepZeroTable(result);
  }

  if (!result.all_passed) {
    throw preflightError(result);
  }
  // eslint-disable-next-line no-console
  console.log(
    `[preflight] ${result.checks.filter((c) => c.pass).length}/${result.checks.length} checks passed`,
  );
  return result;
}

function preflightError(r: PreflightResult): Error {
  const failed = r.checks.filter((c) => !c.pass);
  const summary = failed
    .map(
      (c) =>
        `  - ${c.name}: expected=${c.expected} actual=${c.actual}${c.error ? ` (err=${c.error})` : ""}`,
    )
    .join("\n");
  const setup = isFleetOnlyMode()
    ? `  docker compose -f test/parity/docker-compose.parity.yml up -d --build redis fleet-db loom-fleet ui-fleet parity-seed-fleet`
    : `  docker compose -f test/parity/docker-compose.parity.yml up -d\n` +
      `  docker compose -f test/parity/docker-compose.parity.yml run --rm parity-seed`;
  return new Error(
    `Parity preflight failed — cannot run suite.\n\n` +
      `Failing checks (${failed.length}/${r.checks.length}):\n${summary}\n\n` +
      `The docker-compose.parity.yml stack must be up and healthy. Run:\n` +
      `${setup}\n\n` +
      `Then retry. Never stub out this preflight — see ui-test-plan.md §0.\n` +
      `Detailed JSON: ${PREFLIGHT_JSON}`,
  );
}
