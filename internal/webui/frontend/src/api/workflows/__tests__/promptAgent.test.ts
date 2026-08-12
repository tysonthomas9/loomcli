import { describe, expect, it } from "vitest";

import { promptAgentRoleName, type TriggerBinding } from "../workflows";

function binding(sourceConfigRef?: string): TriggerBinding {
  return {
    workspace_key: "WS",
    binding_id: "b1",
    name: "b1",
    source_kind: "cron",
    route_key: "cron:b1",
    driver_id: "prompt-agent",
    driver_version_id: "v1",
    enabled: true,
    ...(sourceConfigRef !== undefined
      ? { source_config_ref: sourceConfigRef }
      : {}),
  };
}

describe("promptAgentRoleName", () => {
  it("reads roleName from the run-input JSON on source_config_ref", () => {
    expect(
      promptAgentRoleName(
        binding(`{"roleName":"docs-assistant","backend":"codex"}`),
      ),
    ).toBe("docs-assistant");
  });

  it("returns '' when there is no source_config_ref", () => {
    expect(promptAgentRoleName(binding())).toBe("");
  });

  it("returns '' for a non-JSON source_config_ref (a real webhook ref)", () => {
    expect(promptAgentRoleName(binding("source-config-abc"))).toBe("");
  });

  it("returns '' when the JSON carries no roleName", () => {
    expect(promptAgentRoleName(binding(`{"backend":"codex"}`))).toBe("");
  });

  it("returns '' for malformed JSON rather than throwing", () => {
    expect(promptAgentRoleName(binding(`{"roleName":`))).toBe("");
  });
});
