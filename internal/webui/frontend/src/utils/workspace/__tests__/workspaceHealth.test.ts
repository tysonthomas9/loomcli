/**
 * Unit tests for workspaceHealth utility.
 */

import { describe, it, expect } from "vitest";

import type { LoomAgentStatus } from "@/types";

import { computeRepoHealth, worstHealthColor } from "../workspaceHealth";

function makeAgent(status: string): LoomAgentStatus {
  return {
    name: "test-agent",
    branch: "main",
    status,
    ahead: 0,
    behind: 0,
  };
}

describe("computeRepoHealth", () => {
  it("returns green for empty agent list", () => {
    const result = computeRepoHealth([]);
    expect(result).toEqual({
      totalAgents: 0,
      activeCount: 0,
      errorCount: 0,
      healthColor: "green",
    });
  });

  it("returns green for all-ready agents", () => {
    const agents = [makeAgent("ready"), makeAgent("ready")];
    const result = computeRepoHealth(agents);
    expect(result.healthColor).toBe("green");
    expect(result.totalAgents).toBe(2);
    expect(result.activeCount).toBe(0);
    expect(result.errorCount).toBe(0);
  });

  it("returns yellow for working agent", () => {
    const agents = [makeAgent("ready"), makeAgent("working: loom-123 (5m)")];
    const result = computeRepoHealth(agents);
    expect(result.healthColor).toBe("yellow");
    expect(result.activeCount).toBe(1);
  });

  it("returns yellow for planning agent", () => {
    const agents = [makeAgent("planning: loom-456 (2m)")];
    const result = computeRepoHealth(agents);
    expect(result.healthColor).toBe("yellow");
    expect(result.activeCount).toBe(1);
  });

  it("returns yellow for dirty agent", () => {
    const agents = [makeAgent("dirty")];
    const result = computeRepoHealth(agents);
    expect(result.healthColor).toBe("yellow");
    expect(result.activeCount).toBe(1);
  });

  it("returns yellow for agent with changes", () => {
    const agents = [makeAgent("2 changes")];
    const result = computeRepoHealth(agents);
    expect(result.healthColor).toBe("yellow");
    expect(result.activeCount).toBe(1);
  });

  it("returns red for error agent", () => {
    const agents = [makeAgent("error: something failed (1m)")];
    const result = computeRepoHealth(agents);
    expect(result.healthColor).toBe("red");
    expect(result.errorCount).toBe(1);
  });

  it("returns red when error mixed with working", () => {
    const agents = [
      makeAgent("working: loom-123 (5m)"),
      makeAgent("error: crash (1m)"),
    ];
    const result = computeRepoHealth(agents);
    expect(result.healthColor).toBe("red");
    expect(result.activeCount).toBe(1);
    expect(result.errorCount).toBe(1);
  });

  it("returns red when error mixed with ready", () => {
    const agents = [makeAgent("ready"), makeAgent("error: fail (3m)")];
    const result = computeRepoHealth(agents);
    expect(result.healthColor).toBe("red");
    expect(result.totalAgents).toBe(2);
    expect(result.errorCount).toBe(1);
  });

  it("returns green for idle agents", () => {
    const agents = [makeAgent("idle: no tasks (10m)")];
    const result = computeRepoHealth(agents);
    expect(result.healthColor).toBe("green");
    expect(result.activeCount).toBe(0);
  });

  it("returns green for done agents", () => {
    const agents = [makeAgent("done: loom-789 (1m)")];
    const result = computeRepoHealth(agents);
    expect(result.healthColor).toBe("green");
    expect(result.activeCount).toBe(0);
  });

  it("returns green for review agents", () => {
    const agents = [makeAgent("review: loom-101 (3m)")];
    const result = computeRepoHealth(agents);
    expect(result.healthColor).toBe("green");
    expect(result.activeCount).toBe(0);
  });

  it("counts multiple active agents correctly", () => {
    const agents = [
      makeAgent("working: loom-1 (1m)"),
      makeAgent("planning: loom-2 (2m)"),
      makeAgent("dirty"),
      makeAgent("ready"),
    ];
    const result = computeRepoHealth(agents);
    expect(result.healthColor).toBe("yellow");
    expect(result.totalAgents).toBe(4);
    expect(result.activeCount).toBe(3);
    expect(result.errorCount).toBe(0);
  });
});

describe("worstHealthColor", () => {
  it("returns green for empty array", () => {
    expect(worstHealthColor([])).toBe("green");
  });

  it("returns green for all green", () => {
    expect(worstHealthColor(["green", "green"])).toBe("green");
  });

  it("returns yellow when yellow present", () => {
    expect(worstHealthColor(["green", "yellow"])).toBe("yellow");
  });

  it("returns red when red present", () => {
    expect(worstHealthColor(["green", "red"])).toBe("red");
  });

  it("returns red over yellow", () => {
    expect(worstHealthColor(["yellow", "red", "green"])).toBe("red");
  });

  it("returns red immediately (short-circuit)", () => {
    expect(worstHealthColor(["red", "green", "green"])).toBe("red");
  });

  it("returns yellow for single yellow", () => {
    expect(worstHealthColor(["yellow"])).toBe("yellow");
  });

  it("returns green for single green", () => {
    expect(worstHealthColor(["green"])).toBe("green");
  });
});
