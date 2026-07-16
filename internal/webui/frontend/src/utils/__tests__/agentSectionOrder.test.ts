import { describe, expect, it } from "vitest";
import { arrayMove } from "@dnd-kit/sortable";

import type { LoomAgentStatus } from "@/types";

import {
  applyAgentSectionOrder,
  mergeAgentSectionOrder,
  parseStoredAgentSectionOrder,
  reorderAgentGroup,
} from "../agentSectionOrder";

function agent(name: string): LoomAgentStatus {
  return {
    name,
    branch: "",
    status: "idle",
    ahead: 0,
    behind: 0,
    workspace: "default",
  };
}

describe("mergeAgentSectionOrder", () => {
  it("keeps stored order for known agents and appends new ones", () => {
    expect(
      mergeAgentSectionOrder(["b", "c", "a"], ["a", "b", "stale"]),
    ).toEqual(["a", "b", "c"]);
  });
});

describe("applyAgentSectionOrder", () => {
  it("sorts agents by stored order", () => {
    expect(
      applyAgentSectionOrder([agent("b"), agent("a")], ["a", "b"]).map(
        (item) => item.name,
      ),
    ).toEqual(["a", "b"]);
  });
});

describe("reorderAgentGroup", () => {
  it("reorders only the targeted group", () => {
    const fullOrder = ["lead-b", "lead-a", "planner-a", "task-a"];
    const regular = ["lead-b", "lead-a"];

    expect(
      reorderAgentGroup(fullOrder, regular, "lead-a", "lead-b", arrayMove),
    ).toEqual(["lead-a", "lead-b", "planner-a", "task-a"]);
  });
});

describe("parseStoredAgentSectionOrder", () => {
  it("parses a JSON string array", () => {
    expect(parseStoredAgentSectionOrder('["a","b"]')).toEqual(["a", "b"]);
  });

  it("returns null for invalid payloads", () => {
    expect(parseStoredAgentSectionOrder("{")).toBeNull();
    expect(parseStoredAgentSectionOrder('{"a":1}')).toBeNull();
  });
});
