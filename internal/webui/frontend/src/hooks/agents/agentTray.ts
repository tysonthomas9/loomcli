// The Lead chat's agent tray (DF1, Tyson's decisions 2026-10-04): the
// children that are working, and the finished ones whose result still waits
// for the Lead. A child leaves the tray once its result is delivered; its
// started and finished markers stay in the timeline.

import type { Agent, WaitingMessage } from "@/api/agentsv1";
import type { ChatItem, TaskCompleted } from "./agentChatModel";

export type TrayStatus = "running" | "waiting_for_lead";

export interface TrayRow {
  id: string;
  name: string;
  harness: string;
  model: string | null;
  /** The child's state, as the roster has it. */
  state: string;
  status: TrayStatus;
  /** The attempt the row shows, counted from 0 (the chip shows from 1). */
  attempt: number;
  branch: string | null;
  /** The waiting result's record: its outcome, head and summary. */
  record?: TaskCompleted;
  /** When the attempt started: the child's creation, or its last record. */
  startedAt: string;
  /** Set while the result waits: the Lead has not read it yet. */
  unread: boolean;
  /** The started marker the child was in, oldest 0; -1 when unknown. */
  wave: number;
}

export interface TrayWave {
  wave: number;
  /** When its started marker was saved, else its first row's start. */
  at: string;
  rows: TrayRow[];
}

const ENDED = new Set(["finished", "archived"]);

/**
 * The tray's rows, oldest wave first: each child of leadId in the roster
 * whose result waits in the Lead's waiting messages (matched on the record
 * the server names), else that has not ended. Others are left out.
 */
export function trayRows(
  leadId: string,
  roster: Iterable<Agent>,
  items: readonly ChatItem[],
  waiting: readonly WaitingMessage[],
): TrayRow[] {
  const waves = new Map<string, number>();
  const records = new Map<string, TaskCompleted[]>();
  let wave = 0;
  for (const item of items) {
    if (item.kind === "started") {
      item.children.forEach((c) => waves.set(c.child, wave));
      wave++;
    } else if (item.kind === "completion") {
      const r = item.record;
      records.set(r.child, [...(records.get(r.child) ?? []), r]);
    }
  }
  const waitingOn = new Map<string, number>();
  for (const w of waiting)
    for (const c of w.completions ?? [])
      waitingOn.set(c.child, Math.max(waitingOn.get(c.child) ?? -1, c.attempt));

  const rows: TrayRow[] = [];
  for (const a of roster) {
    if (a.parent_agent_id !== leadId || a.deleted_at) continue;
    const recs = records.get(a.agent_id) ?? [];
    const last = recs.reduce<TaskCompleted | undefined>(
      (m, r) => (!m || r.attempt > m.attempt ? r : m),
      undefined,
    );
    const base = {
      id: a.agent_id,
      name: a.name,
      harness: a.harness,
      model: a.model,
      state: a.state,
      branch: a.branch,
      wave: waves.get(a.agent_id) ?? -1,
    };
    const held = waitingOn.get(a.agent_id);
    if (held !== undefined) {
      const record = recs.find((r) => r.attempt === held);
      rows.push({
        ...base,
        status: "waiting_for_lead",
        attempt: held,
        ...(record ? { record } : {}),
        startedAt: a.created_at,
        unread: true,
      });
    } else if (!ENDED.has(a.state)) {
      // The roster's attempt can lag a retry; a saved record cannot.
      const attempt = Math.max(a.attempt, last ? last.attempt + 1 : 0);
      rows.push({
        ...base,
        status: "running",
        attempt,
        startedAt: a.created_at,
        unread: false,
      });
    }
  }
  return rows.sort(
    (x, y) => x.wave - y.wave || x.startedAt.localeCompare(y.startedAt),
  );
}

/** Rows by wave, newest wave first, as the expanded tray lists them. */
export function trayWaves(
  rows: readonly TrayRow[],
  items: readonly ChatItem[],
): TrayWave[] {
  const at = items
    .filter((i) => i.kind === "started")
    .map((i) => (i.kind === "started" ? i.at : ""));
  const out = new Map<number, TrayWave>();
  for (const r of rows) {
    const w = out.get(r.wave) ?? {
      wave: r.wave,
      at: at[r.wave] || r.startedAt,
      rows: [],
    };
    w.rows.push(r);
    out.set(r.wave, w);
  }
  return [...out.values()].sort((x, y) => y.wave - x.wave);
}

export interface TrayCounts {
  running: number;
  done: number;
  failed: number;
}

/** A waiting result that did not complete counts as failed. */
export function trayCounts(rows: readonly TrayRow[]): TrayCounts {
  const c = { running: 0, done: 0, failed: 0 };
  for (const r of rows) {
    if (r.status === "running") c.running++;
    else if (!r.record || r.record.outcome === "completed") c.done++;
    else c.failed++;
  }
  return c;
}

/** The collapsed header's counts, short when narrow ("1 run · 1 fail"). */
export function trayLabel(c: TrayCounts, narrow: boolean): string {
  const parts = [
    c.running && `${c.running} ${narrow ? "run" : "running"}`,
    c.done && `${c.done} done`,
    c.failed && `${c.failed} ${narrow ? "fail" : "failed"}`,
  ];
  return parts.filter(Boolean).join(" · ");
}

/** "0m 21s" for a row's elapsed time; "2:14" (m:ss) for the header. */
export function elapsed(ms: number, clock = false): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  const m = Math.floor(s / 60);
  const rest = s % 60;
  return clock
    ? `${m}:${String(rest).padStart(2, "0")}`
    : `${m}m ${String(rest).padStart(2, "0")}s`;
}

/** A wave divider's age: "now" under a minute, else "9m ago" or "2h ago". */
export function startedAgo(at: string, now: number): string {
  const t = Date.parse(at);
  if (Number.isNaN(t)) return "earlier";
  const m = Math.floor((now - t) / 60000);
  if (m < 1) return "now";
  if (m < 60) return `${m}m ago`;
  return `${Math.floor(m / 60)}h ago`;
}
