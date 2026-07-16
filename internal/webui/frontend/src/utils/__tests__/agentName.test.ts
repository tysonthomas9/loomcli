import { describe, expect, it } from "vitest";

import {
  normalizeStoredAgentName,
  validateStoredAgentName,
} from "@/utils/agentName";

describe("agentName", () => {
  it("normalizes mixed case to lowercase", () => {
    expect(normalizeStoredAgentName("  Test-lead  ")).toBe("test-lead");
  });

  it("accepts valid normalized names", () => {
    expect(validateStoredAgentName("Test-lead")).toBeNull();
    expect(validateStoredAgentName("planner")).toBeNull();
  });

  it("rejects names that start with punctuation", () => {
    expect(validateStoredAgentName("-lead")).toMatch(/cannot start or end/i);
  });

  it("rejects empty names", () => {
    expect(validateStoredAgentName("   ")).toBe("Agent name is required");
  });
});
