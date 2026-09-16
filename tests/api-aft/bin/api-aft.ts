#!/usr/bin/env node
// api-aft CLI.
//
//   doctor  -- compile both specs and report their oracle strength. No stack, no
//              network; already finds real spec defects.
//   run     -- stand up an isolated loom + fleet-db, drive the scenarios, run the
//              oracle stack at every checkpoint, triage, and write artifacts.

import { mkdirSync } from "node:fs";
import { join, resolve } from "node:path";
import { compile, matchOperation, power, type ContractIndex } from "../src/contract.ts";
import { newRecorder } from "../src/wire.ts";
import { start, preflight, teardown, type Stack } from "../src/stack.ts";
import { createWorld, type World } from "../src/world.ts";
import { ALL_INVARIANTS } from "../src/invariants.ts";
import { checkExchange } from "../src/conformance.ts";
import { triageConformance, triageViolation, type Finding } from "../src/triage.ts";
import { writeReport } from "../src/report.ts";
import { plan, family, summarize, type Planned } from "../src/plan.ts";
import { readSweep, POSTCONDITIONS } from "../src/sweep.ts";
import { readdirSync } from "node:fs";
import { pathToFileURL } from "node:url";

type Scenario = { name: string; run: (w: World) => Promise<void> };

/** Every scenarios/*.scn.ts is loaded. Adding a pack needs no CLI edit. */
async function loadScenarios(only?: string): Promise<Scenario[]> {
  const dir = join(HERE, "..", "scenarios");
  const out: Scenario[] = [];
  const files = readdirSync(dir)
    .filter((f) => f.endsWith(".scn.ts"))
    .filter((f) => !only || f.includes(only))
    .sort();
  for (const f of files) {
    const mod = (await import(pathToFileURL(join(dir, f)).href)) as Partial<Scenario>;
    if (typeof mod.run !== "function") continue;
    out.push({ name: mod.name ?? f, run: mod.run });
  }
  return out;
}

const HERE = new URL(".", import.meta.url).pathname;
const LOOM_ROOT = resolve(HERE, "../../..");
const FLEET_REPO = process.env.FLEET_DB_REPO ?? resolve(LOOM_ROOT, "../fleet-db");

function specs(): ContractIndex[] {
  return [
    compile(join(LOOM_ROOT, "api/openapi.yaml"), "loom"),
    compile(join(FLEET_REPO, "api/openapi.yaml"), "fleetdb"),
  ];
}

function doctor(): number {
  const indexes = specs();
  let dangling = 0;
  for (const idx of indexes) {
    const s = idx.stats;
    console.log(`\n=== ${idx.service} :: ${idx.specPath}`);
    console.log(`  operations                 ${s.operations}`);
    console.log(`  responses with a schema    ${s.responsesWithSchema}`);
    console.log(`  bare \`type: object\` 2xx    ${s.bareObjectResponses}  (zero oracle power)`);
    console.log(`  properties                 ${s.totalProps}`);
    console.log(`  ... enum-constrained       ${s.enumConstrainedProps}`);
    console.log(`  ... pattern-constrained    ${s.patternProps}`);
    console.log(`  closed objects             ${s.closedObjects}  (additionalProperties/unevaluatedProperties: false)`);

    const dist = [0, 0, 0, 0, 0];
    for (const op of idx.operations) {
      for (const [, schema] of op.responses) dist[power(schema)]++;
    }
    console.log(`  oracle power  0:${dist[0]}  1:${dist[1]}  2:${dist[2]}  3:${dist[3]}  4:${dist[4]}`);

    if (idx.danglingRefs.length > 0) {
      dangling += idx.danglingRefs.length;
      console.log(`  DANGLING $refs             ${idx.danglingRefs.length}`);
      for (const d of idx.danglingRefs.slice(0, 5)) console.log(`    - ${d.ref}  at ${d.at}`);
    }
  }
  console.log(
    `\nInterpretation: a response at power 0-1 cannot be meaningfully checked against the\n` +
      `spec, which is why conformance is an advisory lane here and the invariant oracles\n` +
      `(L1-L3) carry the run.`,
  );
  if (dangling > 0) {
    console.log(`\nFINDING (no stack required): ${dangling} dangling $ref(s). The spec is not dereferenceable;`);
    console.log(`any generated client or validator silently loses those constraints.`);
  }
  return 0;
}

function matchOperationFor(idx: ContractIndex, method: string, pathname: string): string | null {
  const m = matchOperation(idx, method, pathname);
  return m ? m.op.operationId : null;
}

async function run(only?: string): Promise<number> {
  const runId = new Date().toISOString().replace(/[:.]/g, "-");
  const reportDir = join(HERE, "..", "_reports", runId);
  // Per-process so concurrent runs (parallel scenario authoring) cannot collide on
  // the loom config dir. Ports are already dynamic.
  const workDir = join(HERE, "..", "_stack", String(process.pid));
  mkdirSync(workDir, { recursive: true });
  const started = performance.now();
  const indexes = specs();
  const rec = newRecorder();
  const findings: Finding[] = [];

  console.log(`api-aft run ${runId}`);
  console.log(`  loom repo    ${LOOM_ROOT}`);
  console.log(`  fleet-db     ${FLEET_REPO}`);

  let stack: Stack | null = null;
  let teardownResult = { stopped: false, detail: "never started" };
  let checkpoints: string[] = [];
  let planned: Planned[] = [];
  let sweepSkipped: { operationId: string; missing: string[] }[] = [];
  let preflightChecks: { name: string; ok: boolean; detail: string }[] = [];

  try {
    stack = await start({
      repoRoot: LOOM_ROOT,
      fleetDbBin: process.env.FLEET_DB_BIN ?? join(FLEET_REPO, "bin/fleet-db"),
      workDir,
    });
    console.log(`  loom         ${stack.loomUrl}`);
    console.log(`  fleet-db     ${stack.fleetUrl}  (discovered via runtime.json)`);

    const pf = await preflight(stack, rec);
    preflightChecks = pf.checks;
    for (const c of pf.checks) console.log(`  preflight ${c.ok ? "ok  " : "FAIL"} ${c.name} -- ${c.detail}`);
    if (!pf.ok) throw new Error("preflight failed -- refusing to report findings against a stack that is not proven live");

    const workspace = "APIAFT";
    const world = createWorld(stack, rec, workspace, ALL_INVARIANTS);
    const scenarios = await loadScenarios(only);
    console.log(`\n  ${scenarios.length} scenario(s)`);
    for (const sc of scenarios) {
      const before = world.violations.length;
      try {
        await sc.run(world);
        console.log(`    ok   ${sc.name} (+${world.violations.length - before} violations)`);
      } catch (err) {
        // One failing pack must not lose the other packs' coverage or findings.
        console.log(`    ERR  ${sc.name} -- ${(err as Error).message}`);
        findings.push({
          id: "SCENARIO",
          verdict: "HARNESS",
          severity: "medium",
          title: `scenario "${sc.name}" threw: ${(err as Error).message}`,
          detail: String((err as Error).stack ?? err),
          evidence: sc.name,
          rule: "scenario aborted; other packs continued",
        });
      }
    }
    checkpoints = world.checkpoints;

    for (const v of world.violations) findings.push(triageViolation(v));
    console.log(`  checkpoints: ${checkpoints.join(", ")}`);
    console.log(`  invariant violations: ${world.violations.length}`);

    // Breadth lane: replay every safe GET whose path params the scenarios bound.
    planned = plan(indexes);
    const sweep = await readSweep(stack, rec, planned, world.bindings);
    console.log(`  read sweep: ${sweep.swept} swept, ${sweep.skipped.length} unbindable, ${sweep.nonOk.length} non-2xx`);
    sweepSkipped = sweep.skipped;

    // Universal postconditions over every swept exchange -- cheap per operation, so
    // they scale with breadth instead of needing a check written per endpoint.
    for (const ex of rec.exchanges.filter((e) => e.checkpoint === "read-sweep")) {
      for (const pc of POSTCONDITIONS) {
        const msg = pc.check(ex.status, ex.body, ex.rawBody);
        if (!msg) continue;
        findings.push({
          id: pc.id,
          verdict: "IMPL-BUG",
          severity: pc.id === "PC-NO-5XX" ? "high" : "medium",
          title: `${pc.id}: ${msg}`,
          detail: msg,
          evidence: `${ex.method} ${ex.pathname} -> ${ex.status}`,
          rule: "universal postcondition over the read sweep",
        });
      }
    }
  } catch (err) {
    findings.push({
      id: "HARNESS",
      verdict: "HARNESS",
      severity: "high",
      title: `harness error: ${(err as Error).message}`,
      detail: String((err as Error).stack ?? err),
      evidence: stack ? `see ${stack.logPath}` : "stack never started",
      rule: "run aborted",
    });
    console.error(`  ERROR ${(err as Error).message}`);
  } finally {
    if (stack) teardownResult = await teardown(stack);
  }

  // Advisory conformance lane over every exchange the run actually made.
  for (const ex of rec.exchanges) {
    const idx = indexes.find((i) => i.service === ex.service);
    if (!idx) continue;
    for (const c of checkExchange(idx, ex)) findings.push(triageConformance(c));
  }

  const wallMs = performance.now() - started;
  writeReport(reportDir, {
    runId,
    startedAt: new Date().toISOString(),
    wallMs,
    checkpoints,
    preflight: preflightChecks,
    teardown: teardownResult,
    findings,
    exchanges: rec.exchanges,
  }, indexes);

  // Report DISTINCT findings, matching the report file. One defect seen 24 times is
  // one defect; a raw-observation headline reads as noise.
  const distinct = new Map<string, Finding>();
  for (const f of findings) distinct.set(`${f.verdict}::${f.title}`, f);
  // Coverage against the classified plan, not against the raw 401.
  if (planned.length > 0) {
    const exercised = new Set<string>();
    const checked = new Set<string>();
    for (const ex of rec.exchanges) {
      const idx = indexes.find((i) => i.service === ex.service);
      if (!idx) continue;
      const m = matchOperationFor(idx, ex.method, ex.pathname);
      if (!m) continue;
      exercised.add(`${ex.service}:${m}`);
      if (ex.status >= 200 && ex.status < 300) checked.add(`${ex.service}:${m}`);
    }
    console.log("");
    for (const line of summarize(planned, { exercised, checked })) console.log(`  ${line}`);
    const bySkip = new Map<string, number>();
    for (const s2 of sweepSkipped) {
      const key = s2.missing.join(",");
      bySkip.set(key, (bySkip.get(key) ?? 0) + 1);
    }
    if (bySkip.size > 0) {
      console.log("\n  unbindable path params (the work list -- each needs a scenario to create the entity):");
      for (const [k, n] of [...bySkip.entries()].sort((a, b) => b[1] - a[1]).slice(0, 12)) {
        console.log(`    ${String(n).padStart(4)}  {${k}}`);
      }
    }
  }

  const high = [...distinct.values()].filter((f) => f.severity === "high" && f.verdict !== "HARNESS").length;
  const harness = findings.filter((f) => f.verdict === "HARNESS").length;
  console.log(`\n  exchanges    ${rec.exchanges.length}`);
  console.log(`  findings     ${distinct.size} distinct (${high} high) from ${findings.length} observations`);
  console.log(`  teardown     ${teardownResult.stopped ? "clean" : "ESCALATED"} -- ${teardownResult.detail}`);
  console.log(`  report       ${reportDir}/candidate-findings.md`);
  console.log(`  wall         ${(wallMs / 1000).toFixed(1)}s`);
  return harness > 0 ? 1 : 0;
}

function planCmd(): number {
  const planned = plan(specs());
  const empty = { exercised: new Set<string>(), checked: new Set<string>() };
  console.log(summarize(planned, empty).join("\n"));
  const fams = new Map<string, number>();
  for (const p of planned) {
    if (p.klass !== "safe-read" && p.klass !== "mutating") continue;
    const k = `${p.service}/${family(p)}`;
    fams.set(k, (fams.get(k) ?? 0) + 1);
  }
  console.log("\nin-scope families (safe-read + mutating), largest first:");
  for (const [k, n] of [...fams.entries()].sort((a, b) => b[1] - a[1])) {
    console.log(`  ${String(n).padStart(4)}  ${k}`);
  }
  return 0;
}

const cmd = process.argv[2] ?? "doctor";
if (cmd === "plan") process.exit(planCmd());
else if (cmd === "doctor") process.exit(doctor());
else if (cmd === "run") process.exit(await run(process.argv[3]));
else {
  console.error(`usage: api-aft <plan|doctor|run [scenario-name-substring]>`);
  process.exit(2);
}
