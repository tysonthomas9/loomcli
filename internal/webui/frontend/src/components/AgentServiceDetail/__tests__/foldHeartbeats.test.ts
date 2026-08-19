import { describe, expect, it } from "vitest";

import type { RunEventDTO } from "@/api/agentServices";

import { foldHeartbeats } from "../AgentServiceDetail";

function event(
  id: string,
  action: string,
  actor = "executor-1",
  timestamp = "2026-08-14T23:39:22.442Z",
): RunEventDTO {
  return {
    id,
    timestamp,
    actor,
    action,
    entity_type: "driver_run",
    entity_id: "run-1",
    workspace_id: "LOCALMODE",
  };
}

describe("foldHeartbeats", () => {
  it("passes non-heartbeat events through in order", () => {
    const rows = foldHeartbeats([
      event("1", "driver_run.create", "api"),
      event("2", "driver_run.claim"),
      event("3", "driver_run.finish"),
    ]);
    expect(rows).toHaveLength(3);
    expect(rows.every((row) => row.kind === "event")).toBe(true);
  });

  it("folds consecutive same-actor heartbeats into one row", () => {
    const rows = foldHeartbeats([
      event("1", "driver_run.create", "api"),
      event("2", "driver_run.claim"),
      event("3", "driver_run.heartbeat", "executor-1", "2026-08-14T23:39:30Z"),
      event("4", "driver_run.heartbeat", "executor-1", "2026-08-14T23:40:30Z"),
      event("5", "driver_run.heartbeat", "executor-1", "2026-08-14T23:41:30Z"),
      event("6", "driver_run.finish"),
    ]);
    expect(rows).toHaveLength(4);
    const fold = rows[2];
    if (fold.kind !== "heartbeats") throw new Error("expected heartbeat fold");
    expect(fold.count).toBe(3);
    expect(fold.first.id).toBe("3");
    expect(fold.last.id).toBe("5");
  });

  it("does not fold across a different actor or an interleaved event", () => {
    const rows = foldHeartbeats([
      event("1", "driver_run.heartbeat", "executor-1"),
      event("2", "driver_run.heartbeat", "executor-2"),
      event("3", "driver_run.suspend"),
      event("4", "driver_run.heartbeat", "executor-2"),
    ]);
    expect(rows.map((row) => row.kind)).toEqual([
      "heartbeats",
      "heartbeats",
      "event",
      "heartbeats",
    ]);
  });
});
