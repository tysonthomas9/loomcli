// Coverage planning.
//
// "401 operations" is not a usable denominator. Some are SSE streams that never
// return a JSON body; some spawn PTYs or push git refs and must stay gated; some need
// a GitHub PR that does not exist in a hermetic run. Classifying first means the
// coverage number states what it actually means -- and that the gap between
// "reachable" and "covered" is a work list rather than an accusation.

import type { ContractIndex, Operation } from "./contract.ts";

export type Klass =
  | "safe-read"      // GET, no side effects: the read-sweep lane covers these in bulk
  | "mutating"       // POST/PUT/PATCH that a scenario must drive
  | "destructive"    // deletes, kills, restarts: needs an isolated scenario + cleanup
  | "streaming"      // SSE / WebSocket: needs a stream reader, not a request/response check
  | "host-effecting" // spawns processes, touches git remotes, writes the host
  | "external";      // needs GitHub or another third party

export type Planned = {
  service: string;
  operationId: string;
  method: string;
  pathTemplate: string;
  klass: Klass;
  workspaceScoped: boolean;
  params: string[];
  reason: string;
};

const STREAM_RE = /\/(stream|ws|watch|events)$|\/events\/stream|\/terminal\/ws|\/awaits\b/;
const HOST_RE = /\/(spawn|restart|kill|terminal|tmux|seed|setup|checkouts)\b|\/git\/(push|pull|sync|reset|pr)|push-all/;
const EXTERNAL_RE = /pull-requests|\/git\/pr\b|github|webhook/;
const DESTRUCTIVE_RE = /\/(close-all|purge|reset|tombstone)\b/;

export function classify(op: Operation): { klass: Klass; reason: string } {
  const p = op.pathTemplate;
  // The spec's own prose is more reliable than a path regex: loom's
  // /api/workspaces/{ws}/events is SSE and says so, while looking like a plain list.
  if (op.streamingDeclared) return { klass: "streaming", reason: "spec declares a stream or long-poll" };
  if (STREAM_RE.test(p)) return { klass: "streaming", reason: "SSE/WebSocket: no request/response body to conform" };
  if (EXTERNAL_RE.test(p)) return { klass: "external", reason: "needs GitHub or another third party" };
  if (HOST_RE.test(p)) return { klass: "host-effecting", reason: "spawns processes or touches git remotes" };
  if (op.method === "DELETE" || DESTRUCTIVE_RE.test(p)) return { klass: "destructive", reason: "removes state; needs isolation and cleanup" };
  if (op.method === "GET") return { klass: "safe-read", reason: "read-only; coverable by the read sweep" };
  return { klass: "mutating", reason: "state change a scenario must drive" };
}

export function plan(indexes: ContractIndex[]): Planned[] {
  const out: Planned[] = [];
  for (const idx of indexes) {
    for (const op of idx.operations) {
      const { klass, reason } = classify(op);
      out.push({
        service: idx.service,
        operationId: op.operationId,
        method: op.method,
        pathTemplate: op.pathTemplate,
        klass,
        workspaceScoped: op.paramNames.includes("ws") || op.paramNames.includes("workspace"),
        params: op.paramNames,
        reason,
      });
    }
  }
  return out;
}

/** Group by the leading resource segment, which is how scenarios get assigned to authors. */
export function family(p: Planned): string {
  const seg = p.pathTemplate.split("/").filter((s) => s && !s.startsWith("{"));
  if (p.service === "fleetdb") return seg[2] ?? seg[1] ?? "root";
  // loom: /api/workspaces/{ws}/<family>/...
  if (seg[1] === "workspaces" && seg.length > 2) return seg[2];
  return seg[1] ?? "root";
}

export type CoverageState = {
  exercised: Set<string>;
  checked: Set<string>;
};

export function summarize(planned: Planned[], state: CoverageState): string[] {
  const lines: string[] = [];
  const byKlass = new Map<Klass, Planned[]>();
  for (const p of planned) {
    const k = byKlass.get(p.klass) ?? [];
    k.push(p);
    byKlass.set(p.klass, k);
  }
  const key = (p: Planned): string => `${p.service}:${p.operationId}`;
  lines.push("class            total  exercised  checked");
  for (const [k, ops] of [...byKlass.entries()].sort((a, b) => b[1].length - a[1].length)) {
    const ex = ops.filter((o) => state.exercised.has(key(o))).length;
    const ch = ops.filter((o) => state.checked.has(key(o))).length;
    lines.push(`${k.padEnd(16)} ${String(ops.length).padStart(5)}  ${String(ex).padStart(9)}  ${String(ch).padStart(7)}`);
  }
  const total = planned.length;
  const ex = planned.filter((o) => state.exercised.has(key(o))).length;
  const ch = planned.filter((o) => state.checked.has(key(o))).length;
  lines.push(`${"TOTAL".padEnd(16)} ${String(total).padStart(5)}  ${String(ex).padStart(9)}  ${String(ch).padStart(7)}`);
  return lines;
}
