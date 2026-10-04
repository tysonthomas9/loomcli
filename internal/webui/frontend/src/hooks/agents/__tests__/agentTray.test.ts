import { describe, expect, it } from "vitest";

import type { Agent, WaitingMessage } from "@/api/agentsv1";
import type { ChatItem } from "../agentChatModel";
import {
  elapsed,
  startedAgo,
  trayCounts,
  trayLabel,
  trayRows,
  trayWaves,
} from "../agentTray";

const kid = (id: string, over: Partial<Agent> = {}): Agent =>
  ({
    agent_id: id,
    name: id,
    harness: "claude",
    model: "sonnet",
    branch: `loom/${id}`,
    parent_agent_id: "L",
    state: "active",
    attempt: 0,
    created_at: `2026-10-04T02:0${id.length}:00Z`,
    deleted_at: null,
    ...over,
  }) as Agent;

const started = (at: string, ...ids: string[]): ChatItem => ({
  key: `s:${ids[0]}`,
  kind: "started",
  children: ids.map((child) => ({ child, name: child })),
  at,
});

const done = (
  child: string,
  attempt: number,
  outcome = "completed",
): ChatItem => ({
  key: `task_completed:${child}:${attempt}`,
  kind: "completion",
  name: "",
  record: { child, attempt, outcome, summary: `${child} result` },
  at: "",
});

const waits = (child: string, attempt: number): WaitingMessage => ({
  sender: `agent:${child}`,
  text: "…",
  since: "",
  message: "",
  completions: [{ child, attempt }],
});

describe("trayRows", () => {
  it("shows only working children and results waiting for the Lead", () => {
    const roster = [
      kid("run"),
      kid("held", { state: "finished" }),
      kid("read", { state: "finished" }),
      kid("other", { parent_agent_id: "X" }),
      kid("gone", { deleted_at: "t" }),
    ];
    const items = [
      started("", "run", "held", "read"),
      done("held", 0),
      done("read", 0),
    ];
    const rows = trayRows("L", roster, items, [waits("held", 0)]);
    expect(rows.map((r) => [r.id, r.status, r.unread])).toEqual([
      ["run", "running", false],
      ["held", "waiting_for_lead", true],
    ]);
    expect(rows[1].record?.summary).toBe("held result");
    // Once delivered, the result leaves: the tray empties when all are read.
    const after = trayRows(
      "L",
      roster.map((a) => ({ ...a, state: "finished" })),
      items,
      [],
    );
    expect(after).toEqual([]);
  });

  it("shows a retry's attempt from its last record when the roster lags", () => {
    const rows = trayRows("L", [kid("db")], [done("db", 0, "failed")], []);
    expect(rows[0]).toMatchObject({ status: "running", attempt: 1 });
  });

  it("groups rows by started wave, newest first, with counts and short labels", () => {
    const roster = [kid("a"), kid("bb", { state: "finished" }), kid("ccc")];
    const items = [
      started("2026-10-04T02:00:00Z", "a", "bb"),
      done("bb", 0, "failed"),
      started("2026-10-04T02:09:00Z", "ccc"),
    ];
    const rows = trayRows("L", roster, items, [waits("bb", 0)]);
    const waves = trayWaves(rows, items);
    expect(waves.map((w) => [w.wave, w.rows.map((r) => r.id)])).toEqual([
      [1, ["ccc"]],
      [0, ["a", "bb"]],
    ]);
    const now = Date.parse("2026-10-04T02:09:30Z");
    expect(startedAgo(waves[0].at, now)).toBe("now");
    expect(startedAgo(waves[1].at, now)).toBe("9m ago");
    const c = trayCounts(rows);
    expect(c).toEqual({ running: 2, done: 0, failed: 1 });
    expect(trayLabel(c, false)).toBe("2 running · 1 failed");
    expect(trayLabel(c, true)).toBe("2 run · 1 fail");
  });

  it("formats elapsed time for rows and the header", () => {
    expect(elapsed(112_000)).toBe("1m 52s");
    expect(elapsed(134_000, true)).toBe("2:14");
  });
});
