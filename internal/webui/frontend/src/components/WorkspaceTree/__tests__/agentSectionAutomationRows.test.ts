import { describe, expect, it } from "vitest";

import type { AgentRecordSummary, TriggerBinding } from "@/api";

import {
  buildAgentAutomationRows,
  durableRecordCadence,
  durableRecordDotState,
  selectedDurableRecordID,
} from "../agentSectionAutomationRows";

function record(id: string, name: string, enabled = true): AgentRecordSummary {
  return {
    id,
    name,
    kind: "prompt",
    enabled,
    behavior: { role_name: "documentation" },
    workspace_key: "WS",
  };
}

function binding(bindingId: string, targetAgentId?: string): TriggerBinding {
  return {
    workspace_key: "WS",
    binding_id: bindingId,
    name: bindingId,
    source_kind: "internal",
    route_key: bindingId,
    driver_id: "prompt-agent",
    driver_version_id: "v1",
    ...(targetAgentId ? { target_agent_service_id: targetAgentId } : {}),
    enabled: true,
  };
}

describe("agent-section automation rows", () => {
  it("uses each durable record ID once, retains orphans, and separates only legacy bindings", () => {
    const rows = buildAgentAutomationRows(
      [
        record("record-with-triggers", "Documentation reviewer"),
        record("orphan-record", "Unconfigured reviewer", false),
      ],
      [
        binding("review-trigger", "record-with-triggers"),
        binding("schedule-trigger", "record-with-triggers"),
        binding("legacy-trigger"),
      ],
    );

    expect(rows.durableRecords).toHaveLength(2);
    expect(rows.durableRecords[0]).toMatchObject({
      id: "record-with-triggers",
      record: { id: "record-with-triggers" },
    });
    expect(
      rows.durableRecords[0]?.bindings.map((item) => item.binding_id),
    ).toEqual(["review-trigger", "schedule-trigger"]);
    expect(durableRecordCadence(rows.durableRecords[0]!)).toBe("2 triggers");

    expect(rows.durableRecords[1]).toMatchObject({
      id: "orphan-record",
      record: { id: "orphan-record" },
      bindings: [],
    });
    expect(durableRecordCadence(rows.durableRecords[1]!)).toBe(
      "No triggers configured",
    );
    expect(durableRecordDotState(rows.durableRecords[1]!)).toBe("off");

    expect(rows.legacyBindings).toEqual([
      {
        id: "legacy-trigger",
        binding: expect.objectContaining({ binding_id: "legacy-trigger" }),
      },
    ]);
  });

  it("keeps a durable record selected while inspecting one attached trigger", () => {
    const bindings = [
      binding("review-trigger", "record-with-triggers"),
      binding("schedule-trigger", "record-with-triggers"),
      binding("legacy-trigger"),
    ];

    expect(selectedDurableRecordID("review-trigger", bindings)).toBe(
      "record-with-triggers",
    );
    expect(selectedDurableRecordID("record-with-triggers", bindings)).toBe(
      "record-with-triggers",
    );
    expect(selectedDurableRecordID("legacy-trigger", bindings)).toBe(
      "legacy-trigger",
    );
  });
});
