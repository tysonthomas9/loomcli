// Triage: turn a deviation into a verdict a human can act on.
//
// A harness that emits 200 undifferentiated diffs has negative value -- nobody
// triages them, so it reads as noise and gets switched off. Every finding therefore
// arrives pre-classified, with the rule that classified it named in the output.

import type { ConformanceIssue } from "./conformance.ts";
import type { Violation } from "./world.ts";

export type Verdict = "IMPL-BUG" | "SPEC-BUG" | "SPEC-GAP" | "KNOWN-DEVIATION" | "HARNESS";

export type Finding = {
  id: string;
  verdict: Verdict;
  severity: "high" | "medium" | "low";
  title: string;
  detail: string;
  evidence: string;
  rule: string;
};

// Deviations already recorded in tests/aft/FINDINGS.md or a script comment. Pinned so
// a known issue does not resurface as noise -- and so it FAILS LOUDLY if it is fixed,
// which is how a stale pin gets noticed.
const KNOWN: { match: (c: ConformanceIssue) => boolean; note: string }[] = [
  {
    match: (c) => c.operationId === "listWorkspaces" && c.detail.includes("data"),
    note: "loom answers {success, workspaces}; the spec declares {success, data}. Already recorded the hard way in tests/aft/scripts/live-sweep.sh.",
  },
];

export function triageConformance(c: ConformanceIssue): Finding {
  const base = { evidence: `${c.method} ${c.pathname} -> ${c.status} (${c.service}, power=${c.power})`, detail: c.detail };
  const known = KNOWN.find((k) => k.match(c));
  if (known) {
    return { id: c.operationId, verdict: "KNOWN-DEVIATION", severity: "low", title: `${c.operationId}: known deviation`, rule: "T1 known-deviation registry", ...base, detail: `${c.detail}\n\n${known.note}` };
  }
  switch (c.kind) {
    case "UNROUTED":
      return { id: c.operationId, verdict: "SPEC-GAP", severity: "low", title: `${c.method} ${c.pathname} is served but undocumented`, rule: "T5 live route, no operation", ...base };
    case "NO_SCHEMA":
      return { id: c.operationId, verdict: "SPEC-GAP", severity: "low", title: `${c.operationId} 2xx has no schema`, rule: "T4 no declared schema -> zero oracle power", ...base };
    case "UNDECLARED_STATUS":
      return { id: c.operationId, verdict: "SPEC-GAP", severity: "medium", title: `${c.operationId} returned an undeclared ${c.status}`, rule: "T6 status outside the declared set", ...base };
    case "UNDOCUMENTED_FIELD":
      return { id: c.operationId, verdict: "SPEC-BUG", severity: "low", title: `${c.operationId} returns fields the spec omits`, rule: "T20 strict twin only; spec is behind the impl", ...base };
    case "SCHEMA_VIOLATION": {
      // A missing REQUIRED property is the sharp case: a generated client breaks.
      const required = /must have required property/.test(c.detail);
      const enumMiss = /must be equal to one of the allowed values/.test(c.detail);
      return {
        id: c.operationId,
        verdict: "SPEC-BUG",
        severity: required || enumMiss ? "high" : "medium",
        title: `${c.operationId}: response contradicts the spec`,
        rule: required ? "T16 required property absent" : enumMiss ? "T15 value outside declared enum" : "T17 schema violation",
        ...base,
      };
    }
  }
}

export function triageViolation(v: Violation): Finding {
  const severity = v.layer === "L1" ? "high" : v.layer === "L2" ? "high" : "medium";
  return {
    id: v.invariant,
    verdict: "IMPL-BUG",
    severity,
    title: `${v.invariant}: ${v.message}`,
    detail: v.message,
    evidence: `checkpoint=${v.checkpoint} ${v.evidence}`.trim(),
    rule: `${v.layer} oracle -- derived from the system, not from a spec`,
  };
}
