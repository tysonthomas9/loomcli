// The World: what a scenario drives.
//
// Scenarios contain NO assertions (enforced by scripts/check-no-asserts.mjs). They move
// state and call checkpoint(). At each checkpoint the harness snapshots both observers
// -- loom's API and fleet-db's read model -- folds them into an entity ledger, and runs
// EVERY registered invariant whose declared inputs the ledger now holds.
//
// That dispatch is the yield multiplier: a 40-line scenario picks up every invariant
// its author never wrote, and adding one invariant retroactively strengthens every
// scenario already in the suite.

import { call, newRecorder, type Exchange, type Recorder } from "./wire.ts";
import type { Stack } from "./stack.ts";

export type LedgerIssue = {
  id: string;
  fromLoom: Record<string, unknown> | null;
  fromFleet: Record<string, unknown> | null;
};

export type Ledger = {
  workspace: string;
  issues: Map<string, LedgerIssue>;
  events: unknown[];
  verify: Record<string, unknown> | null;
  checkpoint: string;
};

export type Violation = {
  invariant: string;
  layer: "L1" | "L2" | "L3" | "L4";
  checkpoint: string;
  message: string;
  evidence: string;
};

export type Invariant = {
  id: string;
  layer: "L1" | "L2" | "L3";
  inputs: ("issues" | "events" | "verify")[];
  describe: string;
  run: (l: Ledger) => Violation[] | Promise<Violation[]>;
};

export type World = {
  stack: Stack;
  rec: Recorder;
  workspace: string;
  violations: Violation[];
  checkpoints: string[];
  trackedIssues: Set<string>;
  /** Path-parameter bindings the read sweep may use. Only ever real, created state. */
  bindings: Map<string, string[]>;
  bind: (param: string, value: string) => void;
  loom: (method: string, path: string, body?: unknown) => Promise<Exchange>;
  fleet: (method: string, path: string, body?: unknown) => Promise<Exchange>;
  track: (id: string) => void;
  checkpoint: (name: string) => Promise<void>;
};

function asObj(v: unknown): Record<string, unknown> | null {
  return v !== null && typeof v === "object" && !Array.isArray(v) ? (v as Record<string, unknown>) : null;
}

/** Unwrap loom's several response envelopes: {data}, {issue}, {issues}, or bare. */
export function unwrap(body: unknown, ...keys: string[]): unknown {
  const o = asObj(body);
  if (!o) return body;
  for (const k of [...keys, "data"]) {
    if (o[k] !== undefined) return o[k];
  }
  return body;
}

export function createWorld(stack: Stack, rec: Recorder, workspace: string, invariants: Invariant[]): World {
  const w: World = {
    stack,
    rec,
    workspace,
    violations: [],
    checkpoints: [],
    trackedIssues: new Set<string>(),
    bindings: new Map<string, string[]>([
      ["ws", [workspace]],
      ["workspace", [workspace]],
    ]),
    bind: (param, value) => {
      if (!value) return;
      const cur = w.bindings.get(param) ?? [];
      if (!cur.includes(value)) cur.push(value);
      w.bindings.set(param, cur);
    },
    loom: (method, path, body) => call(rec, { service: "loom", baseUrl: stack.loomUrl, method, path, body }),
    fleet: (method, path, body) => call(rec, { service: "fleetdb", baseUrl: stack.fleetUrl, method, path, body }),
    track: (id) => {
      if (!id) return;
      w.trackedIssues.add(id);
      // Family-qualified: an issue id is only ever substituted into an issues path.
      for (const p of ["issues.id", "issues.issueId", "issues.issue_id"]) w.bind(p, id);
    },
    checkpoint: async (name: string) => {
      rec.currentCheckpoint = name;
      w.checkpoints.push(name);
      const ledger = await snapshot(w, name);
      for (const inv of invariants) {
        const satisfied = inv.inputs.every((i) =>
          i === "issues" ? ledger.issues.size > 0 : i === "events" ? ledger.events.length > 0 : ledger.verify !== null,
        );
        if (!satisfied) continue;
        const vs = await inv.run(ledger);
        w.violations.push(...vs);
      }
      rec.currentCheckpoint = null;
    },
  };
  return w;
}

/**
 * Quiesce, then observe the same facts twice -- once through loom, once through
 * fleet-db. Never assert a write from loom's own status code: update_compat.go can
 * return 200 having silently dropped a field (tests/aft/FINDINGS.md 1.13).
 */
async function snapshot(w: World, checkpoint: string): Promise<Ledger> {
  const ledger: Ledger = {
    workspace: w.workspace,
    issues: new Map(),
    events: [],
    verify: null,
    checkpoint,
  };

  for (const id of w.trackedIssues) {
    const loomRes = await w.loom("GET", `/api/workspaces/${w.workspace}/issues/${id}`);
    const fleetRes = await w.fleet("GET", `/api/v1/${w.workspace}/issues/${id}`);
    ledger.issues.set(id, {
      id,
      fromLoom: asObj(unwrap(loomRes.body, "issue")),
      fromFleet: asObj(unwrap(fleetRes.body, "issue")),
    });
  }

  // fleet-db exposes the event log as /events/mutations (internal/api/mutations.go:55).
  // There is no bare /events -- only this and the SSE /events/stream.
  const ev = await w.fleet("GET", `/api/v1/${w.workspace}/events/mutations?limit=200`);
  const evList = unwrap(ev.body, "mutations", "events");
  if (Array.isArray(evList)) ledger.events = evList;

  // Tier-0: fleet-db replays its own event log and diffs it against the read model.
  const verify = await w.fleet("GET", `/api/v1/${w.workspace}/admin/verify`);
  ledger.verify = asObj(verify.body);

  return ledger;
}

export { newRecorder };
