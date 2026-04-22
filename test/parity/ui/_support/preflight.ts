/**
 * Pre-flight checks: prove beads (:8081) and fleet (:8082) really are on
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

const WEBUI_GAPS_PATH = path.resolve(
    __dirname,
    "../../../../docs/design/parity-report-2026-04-22/webui-gaps.md",
);
const PREFLIGHT_JSON = path.resolve(__dirname, "../artifacts/reports/preflight.json");

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

async function httpJson(url: string, timeoutMs = 5000): Promise<any> {
    const controller = new AbortController();
    const t = setTimeout(() => controller.abort(), timeoutMs);
    try {
        const r = await fetch(url, { signal: controller.signal });
        if (!r.ok) throw new Error(`HTTP ${r.status} on ${url}`);
        return await r.json();
    } finally {
        clearTimeout(t);
    }
}

async function httpPost(
    url: string,
    body: unknown,
    timeoutMs = 5000,
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
            `docker compose -f test/parity/docker-compose.parity.yml exec -T ${service} sh -c ${JSON.stringify(cmd)}`,
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
        return execSync(
            `docker compose -f test/parity/docker-compose.parity.yml logs --tail=${linesFromEnd} fleet-db 2>&1`,
            {
                cwd: path.resolve(__dirname, "../../../.."),
                encoding: "utf-8",
                timeout: 10_000,
            },
        );
    } catch (e) {
        return "";
    }
}

async function runAllChecks(): Promise<PreflightResult> {
    const started = new Date().toISOString();
    const checks: PreflightCheck[] = [];

    // 1. beads config
    try {
        const j = await httpJson(`${PARITY_URLS.beads}/api/config`);
        const backend = j.issue_backend ?? j.backend ?? "(missing)";
        checks.push({
            name: "GET :8081/api/config .issue_backend",
            expected: "beads",
            actual: String(backend),
            pass: backend === "beads",
        });
    } catch (e: any) {
        checks.push({
            name: "GET :8081/api/config .issue_backend",
            expected: "beads",
            actual: "UNREACHABLE",
            pass: false,
            error: e?.message ?? String(e),
        });
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

    // 3. Container healthchecks
    try {
        const ps = execSync(
            `docker compose -f test/parity/docker-compose.parity.yml ps --format json`,
            {
                cwd: path.resolve(__dirname, "../../../.."),
                encoding: "utf-8",
                timeout: 10_000,
            },
        );
        // `docker compose ps --format json` may emit one JSON per line OR a
        // single array. Handle both.
        const rows: any[] = ps
            .split("\n")
            .filter((l) => l.trim().startsWith("{"))
            .map((l) => JSON.parse(l));
        const alsoArray = ps.trim().startsWith("[") ? JSON.parse(ps) : [];
        const services = [...rows, ...alsoArray];
        const healthFor = (name: string) =>
            services.find((s) => s.Service === name || s.Name?.includes(name))?.Health ??
            services.find((s) => s.Service === name || s.Name?.includes(name))?.Status ??
            "(not found)";
        const fleetHealth = healthFor("loom-fleet");
        const beadsHealth = healthFor("loom-beads");
        const fleetDbHealth = healthFor("fleet-db");
        const allHealthy = [fleetHealth, beadsHealth, fleetDbHealth].every(
            (h) => typeof h === "string" && /healthy|running|Up/i.test(h),
        );
        checks.push({
            name: "Container healthchecks all green",
            expected: "healthy (loom-beads, loom-fleet, fleet-db)",
            actual: `loom-beads=${beadsHealth} loom-fleet=${fleetHealth} fleet-db=${fleetDbHealth}`,
            pass: allHealthy,
        });
    } catch (e: any) {
        checks.push({
            name: "Container healthchecks all green",
            expected: "healthy",
            actual: "docker compose ps failed",
            pass: false,
            error: e?.message ?? String(e),
        });
    }

    // 4. Probe POST to :8082 must cause fleet-db to log POST /api/v1/PARITY/issues.
    //
    // We poll the log tail rather than sleeping a fixed 2s because CI can
    // take longer to flush stdout on busy runners — a fixed sleep produces
    // false-negatives that abort the whole suite. Success detection breaks
    // out of the poll as soon as the log entry lands; the total budget
    // (5s) is the fail threshold.
    try {
        const before = await fleetDBLogTail(5);
        const probe = await httpPost(`${PARITY_URLS.fleet}/api/issues`, {
            title: `parity-preflight-${Date.now()}`,
            issue_type: "task",
            priority: 3,
            description: "preflight routing probe — safe to delete",
        });
        const sawInboundPattern = (delta: string) =>
            /POST\s+\/api\/v1\/PARITY\/issues/.test(delta) ||
            /POST .*\/PARITY\/issues/.test(delta);

        const pollDeadline = Date.now() + 5000;
        let sawInbound = false;
        let lastDelta = "";
        while (Date.now() < pollDeadline) {
            const after = await fleetDBLogTail(30);
            lastDelta = after.slice(before.length);
            if (sawInboundPattern(lastDelta)) {
                sawInbound = true;
                break;
            }
            await new Promise((r) => setTimeout(r, 200));
        }
        checks.push({
            name: "Probe POST to :8082 shows up in fleet-db logs",
            expected: "yes",
            actual: sawInbound
                ? "yes — fleet-db received POST /api/v1/PARITY/issues"
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

    // 7. fleet-db has workspace PARITY
    try {
        const j = await httpJson(`${PARITY_URLS.fleetDB}/api/v1/admin/workspaces`);
        const keys: string[] = (j?.data ?? []).map((w: any) => w.key ?? w.Key);
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
    try {
        const j = await httpJson(`${PARITY_URLS.beads}/api/config`);
        const b = j.issue_backend ?? j.backend ?? "(missing)";
        checks.push({
            name: "Settings page shows 'beads' on :8081",
            expected: "yes",
            actual: b === "beads" ? "yes" : `no (${b})`,
            pass: b === "beads",
        });
    } catch (e: any) {
        checks.push({
            name: "Settings page shows 'beads' on :8081",
            expected: "yes",
            actual: "UNREACHABLE",
            pass: false,
            error: e?.message ?? String(e),
        });
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
            LOOM_BEADS_URL: PARITY_URLS.beads,
            LOOM_FLEET_URL: PARITY_URLS.fleet,
            FLEET_DB_URL: PARITY_URLS.fleetDB,
            PARITY_WORKSPACE: PARITY_URLS.workspace,
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

        if (inStep0 && /^\|\s.+\|\s.+\|\s.+\|\s.+\|/.test(line) && !/^\|\s*---/.test(line)) {
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
                // backticks and normalize quote style so "beads" and 'beads'
                // compare equal. The source table uses straight double quotes
                // while our check-name strings use straight single quotes.
                const norm = (s: string) =>
                    s.replace(/[`]/g, "")
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
    await writeStepZeroTable(result);

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
        .map((c) => `  - ${c.name}: expected=${c.expected} actual=${c.actual}${c.error ? ` (err=${c.error})` : ""}`)
        .join("\n");
    return new Error(
        `Parity preflight failed — cannot run suite.\n\n` +
            `Failing checks (${failed.length}/${r.checks.length}):\n${summary}\n\n` +
            `The docker-compose.parity.yml stack must be up and healthy. Run:\n` +
            `  docker compose -f test/parity/docker-compose.parity.yml up -d\n` +
            `  docker compose -f test/parity/docker-compose.parity.yml run --rm parity-seed\n\n` +
            `Then retry. Never stub out this preflight — see ui-test-plan.md §0.\n` +
            `Detailed JSON: ${PREFLIGHT_JSON}`,
    );
}
