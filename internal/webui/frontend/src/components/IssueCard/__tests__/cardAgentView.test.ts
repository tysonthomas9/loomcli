/**
 * Pure unit tests for the card agent view-model resolver. No React / jsdom —
 * the join + precedence logic is tested directly on data. This is where the
 * active-first precedence and the live-claimant-beats-stale-failed-assignee
 * regression are pinned.
 */

import { describe, it, expect } from "vitest";

import type { LoomAgentStatus } from "@/types";
import { resolveAgentForTask } from "@/types";

import { resolveCardAgent } from "../cardAgentView";

function agent(overrides: Partial<LoomAgentStatus> = {}): LoomAgentStatus {
  return {
    name: "nova",
    branch: "feature/x",
    status: "working: T (5m)",
    ahead: 0,
    behind: 0,
    ...overrides,
  };
}

const TASK = "task-T";

describe("resolveAgentForTask", () => {
  it("matches by active_task_id", () => {
    const a = agent({ name: "a", active_task_id: TASK });
    expect(resolveAgentForTask([a], TASK)).toBe(a);
  });

  it("matches by current_task_id when there is no active match", () => {
    const a = agent({ name: "a", current_task_id: TASK });
    expect(resolveAgentForTask([a], TASK)).toBe(a);
  });

  it("returns undefined when nothing matches", () => {
    expect(resolveAgentForTask([agent({ active_task_id: "other" })], TASK)).toBeUndefined();
  });

  it("prefers an active_task_id match over a current_task_id match, regardless of array order (immortal-lock guard)", () => {
    // B holds a stale lock (current_task_id) and appears FIRST; A is live on the
    // task via active_task_id. The live one must win.
    const staleLockHolder = agent({ name: "stale-B", current_task_id: TASK });
    const liveSessionHolder = agent({ name: "live-A", active_task_id: TASK });
    expect(resolveAgentForTask([staleLockHolder, liveSessionHolder], TASK)).toBe(
      liveSessionHolder,
    );
  });
});

describe("resolveCardAgent", () => {
  it("returns none for columns other than in_progress/review", () => {
    expect(
      resolveCardAgent([], { id: TASK, assignee: "nova" }, "done"),
    ).toEqual({ kind: "none" });
    expect(
      resolveCardAgent([], { id: TASK, assignee: "nova" }, undefined),
    ).toEqual({ kind: "none" });
  });

  it("returns none for in_progress without an assignee", () => {
    expect(resolveCardAgent([], { id: TASK }, "in_progress")).toEqual({
      kind: "none",
    });
  });

  it("returns review for a review column with an assignee, regardless of agents", () => {
    const claimant = agent({ active_task_id: TASK });
    expect(
      resolveCardAgent([claimant], { id: TASK, assignee: "nova" }, "review"),
    ).toEqual({ kind: "review", displayName: "nova" });
  });

  it("returns claimed when an agent matches by current_task_id", () => {
    const a = agent({ name: "nova", status: "working: T (5m)", current_task_id: TASK });
    const view = resolveCardAgent([a], { id: TASK, assignee: "nova" }, "in_progress");
    expect(view.kind).toBe("claimed");
    if (view.kind === "claimed") {
      expect(view.status.type).toBe("working");
      expect(view.displayName).toBe("nova");
    }
  });

  it("returns claimed via the derived active_task_id on the serve path (empty current_task_id)", () => {
    const a = agent({
      name: "jack-worker",
      status: "idle",
      current_task_id: "",
      active_task_id: TASK,
      live_status: "working",
    });
    const view = resolveCardAgent([a], { id: TASK, assignee: "oleh" }, "in_progress");
    expect(view.kind).toBe("claimed");
    // effectiveAgentStatus upgrades the idle raw status using live_status.
    if (view.kind === "claimed") expect(view.status.type).toBe("working");
  });

  it("returns missing with no errorClass when nobody claims the task and no same-named agent exists", () => {
    const view = resolveCardAgent([], { id: TASK, assignee: "ghost" }, "in_progress");
    expect(view).toEqual({ kind: "missing", displayName: "ghost", errorClass: undefined });
  });

  it("returns missing with the assignee-named agent's last_error_class when orphaned", () => {
    const idleFailed = agent({
      name: "ghost",
      current_task_id: "",
      active_task_id: "",
      last_error_class: "SpawnFailure",
    });
    const view = resolveCardAgent([idleFailed], { id: TASK, assignee: "ghost" }, "in_progress");
    expect(view).toEqual({
      kind: "missing",
      displayName: "ghost",
      errorClass: "SpawnFailure",
    });
  });

  it("a live claimant wins over a separate, same-named, idle-failed assignee agent (regression guard)", () => {
    const liveClaimant = agent({
      name: "jack-worker",
      active_task_id: TASK,
      live_status: "working",
    });
    const staleAssignee = agent({
      name: "oleh",
      status: "idle",
      current_task_id: "",
      active_task_id: "",
      live_status: "idle",
      last_error_class: "RateLimited",
    });
    const view = resolveCardAgent(
      [liveClaimant, staleAssignee],
      { id: TASK, assignee: "oleh" },
      "in_progress",
    );
    // Resolves to claimed (the live worker), NOT missing — and carries no error.
    expect(view.kind).toBe("claimed");
    expect(view).not.toHaveProperty("errorClass");
  });

  it("keeps the [H] prefix in displayName but strips it for the by-name error lookup", () => {
    const idleFailed = agent({
      name: "Alice",
      current_task_id: "",
      active_task_id: "",
      last_error_class: "AuthFailure",
    });
    const view = resolveCardAgent(
      [idleFailed],
      { id: TASK, assignee: "[H] Alice" },
      "in_progress",
    );
    expect(view).toEqual({
      kind: "missing",
      displayName: "[H] Alice",
      errorClass: "AuthFailure",
    });
  });
});
