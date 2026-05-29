/**
 * Unit tests for the agent status helpers in types/agent/agent.ts:
 * - effectiveAgentStatus: prefers fleet-db's derived live_status, but only to
 *   flip an *idle-like* lock-derived status (never to mask done/review/error/...).
 * - isAgentActive: the shared active-agent predicate the AgentsSidebar count and
 *   the AgentCard badge both resolve through, so they can never disagree.
 */

import { describe, it, expect } from "vitest";

import type { LoomAgentStatus } from "@/types";
import { effectiveAgentStatus, isAgentActive } from "@/types";

function makeAgent(overrides: Partial<LoomAgentStatus> = {}): LoomAgentStatus {
  return {
    name: "falcon",
    branch: "webui/falcon",
    status: "idle",
    ahead: 0,
    behind: 0,
    ...overrides,
  };
}

describe("effectiveAgentStatus", () => {
  it("keeps a daemon-mode working status verbatim (preserving duration)", () => {
    expect(
      effectiveAgentStatus(
        makeAgent({ status: "working: loom-1 (5m)", live_status: "idle" }),
      ),
    ).toBe("working: loom-1 (5m)");
  });

  it("keeps a daemon-mode planning status verbatim", () => {
    expect(
      effectiveAgentStatus(makeAgent({ status: "planning: loom-2 (2m)" })),
    ).toBe("planning: loom-2 (2m)");
  });

  it("flips idle -> working from live_status with the task id", () => {
    expect(
      effectiveAgentStatus(
        makeAgent({
          status: "idle",
          live_status: "working",
          active_task_id: "loom-3",
        }),
      ),
    ).toBe("working: loom-3");
  });

  it("flips ready -> working from live_status (ready is idle-like)", () => {
    expect(
      effectiveAgentStatus(
        makeAgent({
          status: "ready",
          live_status: "working",
          active_task_id: "loom-4",
        }),
      ),
    ).toBe("working: loom-4");
  });

  it("flips an empty/unknown status -> working from live_status", () => {
    expect(
      effectiveAgentStatus(
        makeAgent({
          status: "",
          live_status: "working",
          active_task_id: "loom-5",
        }),
      ),
    ).toBe("working: loom-5");
  });

  it("uses the planning prefix for a plan-role agent", () => {
    expect(
      effectiveAgentStatus(
        makeAgent({
          status: "idle",
          live_status: "working",
          active_task_id: "loom-6",
          role: "plan",
        }),
      ),
    ).toBe("planning: loom-6");
  });

  it("uses the planning prefix when active_phase is planning", () => {
    expect(
      effectiveAgentStatus(
        makeAgent({
          status: "idle",
          live_status: "working",
          active_task_id: "loom-7",
          active_phase: "planning",
        }),
      ),
    ).toBe("planning: loom-7");
  });

  it("synthesizes a bare working prefix when working with no task id", () => {
    expect(
      effectiveAgentStatus(
        makeAgent({ status: "idle", live_status: "working" }),
      ),
    ).toBe("working");
  });

  it("returns the raw status when live_status is idle", () => {
    expect(
      effectiveAgentStatus(makeAgent({ status: "idle", live_status: "idle" })),
    ).toBe("idle");
  });

  // Finding #3: a more specific lock-derived status is NOT masked by live_status.
  it.each([
    "review: loom-8 (3m)",
    "done: loom-9 (1m)",
    "error: loom-10",
    "dirty",
    "2 changes",
  ])("does not let live_status override a specific status (%s)", (status) => {
    expect(
      effectiveAgentStatus(
        makeAgent({ status, live_status: "working", active_task_id: "loom-x" }),
      ),
    ).toBe(status);
  });
});

describe("isAgentActive", () => {
  it("counts a daemon-mode working agent", () => {
    expect(isAgentActive(makeAgent({ status: "working: loom-1 (5m)" }))).toBe(
      true,
    );
  });

  it("counts a daemon-mode planning agent", () => {
    expect(isAgentActive(makeAgent({ status: "planning: loom-2 (2m)" }))).toBe(
      true,
    );
  });

  it("counts a live_status-working agent flipped from idle", () => {
    expect(
      isAgentActive(
        makeAgent({
          status: "idle",
          live_status: "working",
          active_task_id: "loom-3",
        }),
      ),
    ).toBe(true);
  });

  // Finding #2: a bare "working" (no task id) is counted, matching the badge.
  it("counts a bare working agent with no task id (matches the badge)", () => {
    expect(
      isAgentActive(makeAgent({ status: "idle", live_status: "working" })),
    ).toBe(true);
  });

  it("does not count an idle agent", () => {
    expect(
      isAgentActive(makeAgent({ status: "idle", live_status: "idle" })),
    ).toBe(false);
  });

  it("does not count ready/done/review/error/dirty agents", () => {
    expect(isAgentActive(makeAgent({ status: "ready" }))).toBe(false);
    expect(isAgentActive(makeAgent({ status: "done: x (1m)" }))).toBe(false);
    expect(isAgentActive(makeAgent({ status: "review: x (1m)" }))).toBe(false);
    expect(isAgentActive(makeAgent({ status: "error: x" }))).toBe(false);
    expect(isAgentActive(makeAgent({ status: "dirty" }))).toBe(false);
  });

  // Finding #3 interaction: a done status with a stale live_status="working" is
  // still not counted, because effectiveAgentStatus won't override it.
  it("does not count a done agent even when live_status is working", () => {
    expect(
      isAgentActive(
        makeAgent({
          status: "done: x (1m)",
          live_status: "working",
          active_task_id: "y",
        }),
      ),
    ).toBe(false);
  });
});
