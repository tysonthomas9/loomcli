// Reporting. Artifacts, not scrollback: a transcript for every exchange, findings in
// a shape that pastes into tests/aft/FINDINGS.md, and a coverage file that always
// carries TWO numbers so "covered" can never be read as "checked".

import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { normalize, type Exchange } from "./wire.ts";
import type { Finding } from "./triage.ts";
import type { ContractIndex } from "./contract.ts";
import { matchOperation, power } from "./contract.ts";

export type RunSummary = {
  runId: string;
  startedAt: string;
  wallMs: number;
  checkpoints: string[];
  preflight: { name: string; ok: boolean; detail: string }[];
  teardown: { stopped: boolean; detail: string };
  findings: Finding[];
  exchanges: Exchange[];
};

export function writeReport(dir: string, s: RunSummary, indexes: ContractIndex[]): void {
  mkdirSync(dir, { recursive: true });

  // Transcripts: raw and normalized, side by side.
  const transcript = s.exchanges.map((ex) => ({
    seq: ex.seq,
    checkpoint: ex.checkpoint,
    service: ex.service,
    request: `${ex.method} ${ex.pathname}`,
    requestBody: ex.requestBody,
    status: ex.status,
    durationMs: Math.round(ex.durationMs),
    raw: ex.body,
    normalized: normalize(ex.body),
    parseError: ex.parseError,
  }));
  writeFileSync(join(dir, "transcript.json"), JSON.stringify(transcript, null, 2));

  // Coverage: exercised vs actually-checkable. Two numbers, always.
  const seen = new Set<string>();
  let checkable = 0;
  for (const ex of s.exchanges) {
    for (const idx of indexes) {
      if (idx.service !== ex.service) continue;
      const m = matchOperation(idx, ex.method, ex.pathname);
      if (!m) continue;
      const key = `${idx.service}:${m.op.operationId}`;
      if (!seen.has(key)) {
        seen.add(key);
        if (power(m.op.responses.get(String(ex.status))) >= 2) checkable++;
      }
    }
  }
  const coverage = {
    runId: s.runId,
    operationsExercised: seen.size,
    operationsWithUsableSchema: checkable,
    totalOperations: indexes.reduce((n, i) => n + i.operations.length, 0),
    note: "operationsExercised counts calls made. operationsWithUsableSchema counts those the spec could actually check (power >= 2). The gap is unverified surface.",
  };
  writeFileSync(join(dir, "coverage.json"), JSON.stringify(coverage, null, 2));

  // Findings, FINDINGS.md-shaped.
  //
  // DEDUPLICATE FIRST. One defect observed 24 times is one defect. Counting raw
  // observations produces a scary headline number that is mostly the same finding,
  // which is exactly how a capture-and-diff harness earns a reputation for noise and
  // gets switched off.
  const distinct = new Map<string, { f: Finding; count: number }>();
  for (const f of s.findings) {
    const key = `${f.verdict}::${f.title}`;
    const hit = distinct.get(key);
    if (hit) hit.count++;
    else distinct.set(key, { f, count: 1 });
  }
  const bySeverity = { high: 0, medium: 0, low: 0 };
  for (const { f } of distinct.values()) bySeverity[f.severity]++;
  const lines: string[] = [
    `# api-aft candidate findings -- run ${s.runId}`,
    "",
    `Started ${s.startedAt} - wall ${(s.wallMs / 1000).toFixed(1)}s - checkpoints: ${s.checkpoints.join(", ") || "none"}`,
    `${distinct.size} distinct findings (${bySeverity.high} high, ${bySeverity.medium} medium, ${bySeverity.low} low)`,
    `from ${s.findings.length} raw observations across ${s.exchanges.length} exchanges`,
    "",
    "Verdicts are pre-assigned by src/triage.ts and name the rule that assigned them.",
    "IMPL-BUG comes from an oracle derived from the system itself (L1-L3) and is the",
    "strongest class here. SPEC-BUG / SPEC-GAP come from the advisory conformance lane.",
    "",
  ];
  const order: Finding["verdict"][] = ["IMPL-BUG", "SPEC-BUG", "SPEC-GAP", "KNOWN-DEVIATION", "HARNESS"];
  const sevRank = { high: 0, medium: 1, low: 2 };
  for (const verdict of order) {
    const group = [...distinct.values()]
      .filter((d) => d.f.verdict === verdict)
      .sort((a, b) => sevRank[a.f.severity] - sevRank[b.f.severity]);
    if (group.length === 0) continue;
    lines.push(`## ${verdict} (${group.length})`, "");
    for (const { f, count } of group) {
      lines.push(`### ${f.title}`);
      lines.push(`- **severity** ${f.severity}`);
      lines.push(`- **rule** ${f.rule}`);
      lines.push(`- **observed** ${count}x this run`);
      lines.push(`- **evidence** \`${f.evidence}\``);
      lines.push(`- **detail** ${f.detail.replace(/\n/g, "\n  ")}`);
      lines.push("");
    }
  }
  writeFileSync(join(dir, "candidate-findings.md"), lines.join("\n"));

  // Ledger: one accounting line per run. Never fails a run.
  const ledger = [
    `run=${s.runId}`,
    `wall_s=${(s.wallMs / 1000).toFixed(1)}`,
    `exchanges=${s.exchanges.length}`,
    `checkpoints=${s.checkpoints.length}`,
    `distinct_findings=${distinct.size}`,
    `high=${bySeverity.high}`,
    `preflight=${s.preflight.every((p) => p.ok) ? "ok" : "FAIL"}`,
    `teardown=${s.teardown.stopped ? "clean" : "ESCALATED"}`,
  ].join(" ");
  writeFileSync(join(dir, "ledger.log"), ledger + "\n");
  writeFileSync(join(dir, "last-run.json"), JSON.stringify({ ...coverage, distinctFindings: distinct.size, rawObservations: s.findings.length, bySeverity }, null, 2));
}
