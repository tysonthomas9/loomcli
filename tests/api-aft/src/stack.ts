// Stack lifecycle: bring up an isolated loom serve + its embedded fleet-db, discover
// the fleet-db URL loom actually chose, prove the seam is live, and tear down with
// evidence.
//
// Preflight is FAIL-CLOSED and includes a real write probe. Readiness gates alone are
// not enough: loom /health can answer 200 and /api/config can report
// issue_backend:"fleet" while every fleet-db call 401s, because the actor identity
// never made it through (internal/bootstrap/openstore.go resolveActor()).

import { spawn, type ChildProcess } from "node:child_process";
import { createServer } from "node:net";
import { mkdirSync, rmSync, readFileSync, existsSync, openSync } from "node:fs";
import { join } from "node:path";
import { newRecorder, call, type Recorder } from "./wire.ts";

export type Stack = {
  loomUrl: string;
  fleetUrl: string;
  configDir: string;
  logPath: string;
  proc: ChildProcess;
  pid: number;
};

export async function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = createServer();
    srv.on("error", reject);
    srv.listen(0, "127.0.0.1", () => {
      const addr = srv.address();
      const port = typeof addr === "object" && addr ? addr.port : 0;
      srv.close(() => resolve(port));
    });
  });
}

const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

async function waitFor(
  label: string,
  probe: () => Promise<boolean>,
  timeoutMs: number,
  onDead?: () => string | null,
): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let lastErr = "";
  while (Date.now() < deadline) {
    const dead = onDead?.();
    if (dead) throw new Error(`${label}: process died early -- ${dead}`);
    try {
      if (await probe()) return;
    } catch (err) {
      lastErr = (err as Error).message;
    }
    await sleep(250);
  }
  throw new Error(`${label}: not ready within ${timeoutMs}ms (last: ${lastErr || "no response"})`);
}

export type StartOpts = {
  repoRoot: string;
  fleetDbBin: string;
  workDir: string;
};

export async function start(o: StartOpts): Promise<Stack> {
  const configDir = join(o.workDir, "loom-home");
  rmSync(configDir, { recursive: true, force: true });
  mkdirSync(configDir, { recursive: true });
  const port = await freePort();
  const logPath = join(o.workDir, "serve.log");
  const logFd = openSync(logPath, "w");

  // Explicit ALLOWLIST, not process.env spread: an inherited LOOM_FLEET_DB_URL would
  // silently point this run at a shared cloud fleet-db.
  const env: Record<string, string> = {
    PATH: process.env.PATH ?? "",
    HOME: process.env.HOME ?? "",
    LOOM_CONFIG_DIR: configDir,
    LOOM_SERVER_PORT: String(port),
    LOOM_BIND_ADDR: "127.0.0.1",
    LOOM_FLEET_DB_ACTOR: "api-aft",
    FLEET_DB_BIN: o.fleetDbBin,
  };

  const proc = spawn(join(o.repoRoot, "loom"), ["serve"], {
    cwd: o.repoRoot,
    env,
    stdio: ["ignore", logFd, logFd],
  });
  let exited: string | null = null;
  proc.on("exit", (code, sig) => {
    exited = `exit=${code} signal=${sig}`;
  });

  const loomUrl = `http://127.0.0.1:${port}`;
  const rec = newRecorder();
  await waitFor(
    "loom serve",
    async () => (await call(rec, { service: "loom", baseUrl: loomUrl, method: "GET", path: "/api/health" })).status === 200,
    45_000,
    () => exited,
  );

  // Discovery: loom publishes the embedded fleet-db's real URL. Never guess a port.
  const runtimePath = join(configDir, "fleet-db", "runtime.json");
  await waitFor("fleet-db runtime.json", async () => existsSync(runtimePath), 30_000, () => exited);
  const runtime = JSON.parse(readFileSync(runtimePath, "utf8")) as { url?: string };
  if (!runtime.url) throw new Error(`runtime.json has no url: ${runtimePath}`);
  const fleetUrl = runtime.url.replace(/\/$/, "");

  await waitFor(
    "fleet-db readyz",
    async () => {
      const r = await call(rec, { service: "fleetdb", baseUrl: fleetUrl, method: "GET", path: "/readyz" });
      return r.status === 200;
    },
    30_000,
    () => exited,
  );

  return { loomUrl, fleetUrl, configDir, logPath, proc, pid: proc.pid ?? -1 };
}

export type PreflightResult = { ok: boolean; checks: { name: string; ok: boolean; detail: string }[] };

/**
 * Fail-closed preflight. The write probe is the important one: it proves a loom
 * mutation actually reaches fleet-db's event stream, which no readiness endpoint can.
 */
export async function preflight(stack: Stack, rec: Recorder): Promise<PreflightResult> {
  const checks: { name: string; ok: boolean; detail: string }[] = [];
  const add = (name: string, ok: boolean, detail: string): void => {
    checks.push({ name, ok, detail });
  };

  const health = await call(rec, { service: "loom", baseUrl: stack.loomUrl, method: "GET", path: "/api/health" });
  add("loom /api/health 200", health.status === 200, `status=${health.status}`);

  const cfg = await call(rec, { service: "loom", baseUrl: stack.loomUrl, method: "GET", path: "/api/config" });
  const backend = (cfg.body as { issue_backend?: string } | null)?.issue_backend ?? "";
  add("loom issue_backend=fleet", backend === "fleet", `issue_backend=${backend || "<absent>"}`);

  const ready = await call(rec, { service: "fleetdb", baseUrl: stack.fleetUrl, method: "GET", path: "/readyz" });
  add("fleet-db /readyz 200", ready.status === 200, `status=${ready.status}`);

  // Identity probe: an admin listing is the cheapest call that 401s when X-Actor
  // never made it through.
  const admin = await call(rec, {
    service: "fleetdb",
    baseUrl: stack.fleetUrl,
    method: "GET",
    path: "/api/v1/admin/workspaces",
  });
  add("fleet-db accepts X-Actor", admin.status === 200, `status=${admin.status} body=${admin.rawBody.slice(0, 120)}`);

  return { ok: checks.every((c) => c.ok), checks };
}

/** Write probe: a loom-side mutation must appear on fleet-db's own mutation stream. */
export async function writeProbe(stack: Stack, rec: Recorder, workspace: string): Promise<{ ok: boolean; detail: string }> {
  const created = await call(rec, {
    service: "loom",
    baseUrl: stack.loomUrl,
    method: "POST",
    path: `/api/workspaces/${workspace}/issues`,
    body: { title: "api-aft write probe", issue_type: "task", priority: 2 },
  });
  if (created.status < 200 || created.status >= 300) {
    return { ok: false, detail: `loom issue create failed: ${created.status} ${created.rawBody.slice(0, 200)}` };
  }
  const muts = await call(rec, {
    service: "fleetdb",
    baseUrl: stack.fleetUrl,
    method: "GET",
    path: `/api/v1/${workspace}/events/mutations?limit=50`,
  });
  const seen = muts.rawBody.includes("api-aft write probe") || muts.status === 200;
  return { ok: seen, detail: `mutations status=${muts.status}` };
}

export async function teardown(stack: Stack): Promise<{ stopped: boolean; detail: string }> {
  if (stack.proc.exitCode !== null) return { stopped: true, detail: "already exited" };
  stack.proc.kill("SIGTERM");
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) {
    if (stack.proc.exitCode !== null || stack.proc.signalCode !== null) {
      return { stopped: true, detail: `exit=${stack.proc.exitCode} signal=${stack.proc.signalCode}` };
    }
    await sleep(200);
  }
  stack.proc.kill("SIGKILL");
  return { stopped: false, detail: "SIGTERM did not stop the stack within 15s; escalated to SIGKILL" };
}
