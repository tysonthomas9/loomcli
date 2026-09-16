// The oracle stack, layers L1-L3. Precedence: server truth > differential > semantic.
// A lower layer never overrules a higher one; when the spec (L4) disagrees with a
// differential (L2), the spec is the suspect.

import type { Invariant, Ledger, Violation } from "./world.ts";

const v = (
  invariant: string,
  layer: "L1" | "L2" | "L3",
  l: Ledger,
  message: string,
  evidence: string,
): Violation => ({ invariant, layer, checkpoint: l.checkpoint, message, evidence });

function str(o: Record<string, unknown> | null, k: string): string | undefined {
  const x = o?.[k];
  return typeof x === "string" ? x : undefined;
}

// --- L1: tier-0 server truth -------------------------------------------------
// fleet-db replays its own event stream in memory and diffs it against the Redis
// read model (fleet-db/internal/recovery/verify.go). This is the strongest oracle
// available and it can fail for reasons no test author anticipated.

export const TIER0: Invariant = {
  id: "TIER0-VERIFY",
  layer: "L1",
  inputs: ["verify"],
  describe: "fleet-db's own event-fold must agree with its read model",
  run: (l) => {
    const out: Violation[] = [];
    const consistent = l.verify?.["consistent"];
    if (consistent === false) {
      // One violation PER DISCREPANCY, keyed by issue+field. Collapsing them into a
      // single "projection diverged" finding means a known divergence permanently
      // masks every new one -- the report looks identical either way.
      const ds = l.verify?.["discrepancies"];
      const list = Array.isArray(ds) ? ds : [];
      if (list.length === 0) {
        out.push(v("TIER0-VERIFY", "L1", l, "verify reported inconsistent with no discrepancies listed", ""));
      }
      for (const d of list) {
        const o = d as { issue_id?: string; field?: string; expected?: string; actual?: string };
        out.push(
          v(
            "TIER0-VERIFY",
            "L1",
            l,
            `event-fold and read model disagree on ${o.issue_id ?? "?"}.${o.field ?? "?"}`,
            `replay expected ${JSON.stringify(o.expected)}, read model has ${JSON.stringify(o.actual)}`,
          ),
        );
      }
    }
    if (consistent === undefined) {
      out.push(
        v("TIER0-VERIFY", "L1", l, "admin/verify returned no `consistent` field -- the tier-0 oracle is not answering",
          JSON.stringify(l.verify).slice(0, 300)),
      );
    }
    return out;
  },
};

// --- L2: differential (the seam) ---------------------------------------------
// The same fact observed twice, independently. This is what catches a
// wrong-but-well-shaped response, which neither schema conformance nor a golden
// file can see.

export const SEAM_PRESENCE: Invariant = {
  id: "SEAM-1",
  layer: "L2",
  inputs: ["issues"],
  describe: "an issue loom serves must exist in fleet-db's read model",
  run: (l) => {
    const out: Violation[] = [];
    for (const [id, e] of l.issues) {
      if (e.fromLoom && !e.fromFleet) {
        out.push(v("SEAM-1", "L2", l, `loom serves issue ${id} but fleet-db does not have it`, JSON.stringify(e.fromLoom).slice(0, 300)));
      }
    }
    return out;
  },
};

export const SEAM_FIELDS: Invariant = {
  id: "SEAM-2",
  layer: "L2",
  inputs: ["issues"],
  describe: "fields both observers carry must agree in value",
  run: (l) => {
    const out: Violation[] = [];
    const compared = ["title", "status", "priority", "assignee", "issue_type", "description"];
    for (const [id, e] of l.issues) {
      if (!e.fromLoom || !e.fromFleet) continue;
      for (const f of compared) {
        const a = e.fromLoom[f];
        const b = e.fromFleet[f];
        if (a === undefined || b === undefined) continue;
        if (JSON.stringify(a) !== JSON.stringify(b)) {
          out.push(v("SEAM-2", "L2", l, `issue ${id} field \`${f}\` disagrees across the seam`, `loom=${JSON.stringify(a)} fleetdb=${JSON.stringify(b)}`));
        }
      }
    }
    return out;
  },
};

export const SEAM_WRITE_DURABILITY: Invariant = {
  id: "SEAM-3",
  layer: "L2",
  inputs: ["issues", "events"],
  describe: "every tracked issue must have produced at least one event",
  run: (l) => {
    const out: Violation[] = [];
    const blob = JSON.stringify(l.events);
    for (const [id] of l.issues) {
      if (!blob.includes(id)) {
        out.push(v("SEAM-3", "L2", l, `issue ${id} exists but appears in no event -- a write that left no audit trail`, `events=${l.events.length}`));
      }
    }
    return out;
  },
};

// --- L3: semantic invariants -------------------------------------------------

export const CLOSED_AT: Invariant = {
  id: "INV-CLOSEDAT",
  layer: "L3",
  inputs: ["issues"],
  describe: "a closed issue carries closed_at; an open one does not",
  run: (l) => {
    const out: Violation[] = [];
    for (const [id, e] of l.issues) {
      const src = e.fromFleet ?? e.fromLoom;
      if (!src) continue;
      const status = str(src, "status");
      const closedAt = src["closed_at"];
      if (status === "closed" && (closedAt === null || closedAt === undefined || closedAt === "")) {
        out.push(v("INV-CLOSEDAT", "L3", l, `issue ${id} is closed but has no closed_at`, JSON.stringify(src).slice(0, 300)));
      }
      if (status === "open" && typeof closedAt === "string" && closedAt !== "") {
        out.push(v("INV-CLOSEDAT", "L3", l, `issue ${id} is open but carries closed_at=${closedAt}`, ""));
      }
    }
    return out;
  },
};

const FLEET_STATUSES = new Set([
  "open", "in_progress", "blocked", "deferred", "review", "closed", "tombstone", "pinned", "hooked",
]);

export const STATUS_DOMAIN: Invariant = {
  id: "INV-ENUM-STATUS",
  layer: "L3",
  inputs: ["issues"],
  describe: "status must be one of fleet-db's nine",
  run: (l) => {
    const out: Violation[] = [];
    for (const [id, e] of l.issues) {
      for (const [who, src] of [["loom", e.fromLoom], ["fleetdb", e.fromFleet]] as const) {
        const s = str(src, "status");
        if (s !== undefined && !FLEET_STATUSES.has(s)) {
          out.push(v("INV-ENUM-STATUS", "L3", l, `issue ${id} has status "${s}" from ${who}, outside the nine`, ""));
        }
      }
    }
    return out;
  },
};

export const TS_ORDER: Invariant = {
  id: "INV-TS-ORDER",
  layer: "L3",
  inputs: ["issues"],
  describe: "updated_at must not precede created_at",
  run: (l) => {
    const out: Violation[] = [];
    for (const [id, e] of l.issues) {
      const src = e.fromFleet ?? e.fromLoom;
      const c = str(src, "created_at");
      const u = str(src, "updated_at");
      if (!c || !u) continue;
      const cd = Date.parse(c);
      const ud = Date.parse(u);
      if (Number.isNaN(cd) || Number.isNaN(ud)) {
        out.push(v("INV-TS-ORDER", "L3", l, `issue ${id} has an unparseable timestamp`, `created_at=${c} updated_at=${u}`));
        continue;
      }
      if (ud < cd - 1000) {
        out.push(v("INV-TS-ORDER", "L3", l, `issue ${id} updated_at precedes created_at`, `created_at=${c} updated_at=${u}`));
      }
    }
    return out;
  },
};

export const ID_FORM: Invariant = {
  id: "INV-IDFORM",
  layer: "L3",
  inputs: ["issues"],
  describe: "ids follow the sequential <WS>-<n> scheme and never repeat",
  run: (l) => {
    const out: Violation[] = [];
    const seen = new Set<string>();
    for (const [id] of l.issues) {
      if (!/^[A-Za-z0-9_-]+-\d+$/.test(id)) {
        out.push(v("INV-IDFORM", "L3", l, `issue id "${id}" does not match the sequential scheme`, ""));
      }
      if (seen.has(id)) out.push(v("INV-IDFORM", "L3", l, `issue id "${id}" appeared twice`, ""));
      seen.add(id);
    }
    return out;
  },
};

export const ALL_INVARIANTS: Invariant[] = [
  TIER0,
  SEAM_PRESENCE,
  SEAM_FIELDS,
  SEAM_WRITE_DURABILITY,
  CLOSED_AT,
  STATUS_DOMAIN,
  TS_ORDER,
  ID_FORM,
];
