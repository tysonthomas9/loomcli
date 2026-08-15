import { describe, expect, it } from "vitest";

import type { AgentServiceDTO } from "@/api/agentServices";

import {
  agentServiceCadenceLabel,
  agentServiceDotState,
  agentServiceDotTooltip,
  describeCronSchedule,
  formatFireTime,
} from "../bindingDisplay";

function service(overrides: Partial<AgentServiceDTO> = {}): AgentServiceDTO {
  return {
    id: "scout",
    name: "Scout",
    triggerKind: "cron",
    enabled: true,
    behavior: {
      roleName: "scout",
      roleDisplayName: "Scout",
      workflowName: "scout",
      scripted: true,
    },
    bindings: [
      {
        id: "binding-scout-weekly",
        sourceKind: "cron",
        schedule: "@weekly",
        enabled: true,
        routeKey: "cron.scout.weekly",
      },
    ],
    nextFireAt: "2026-08-17T00:00:00Z",
    lastRunStatus: "succeeded",
    consecutiveFailures: 0,
    errors: [],
    createdAt: "2026-08-14T00:00:00Z",
    updatedAt: "2026-08-14T00:00:00Z",
    ...overrides,
  };
}

describe("binding display", () => {
  it("humanizes cron nicknames and common five-field schedules", () => {
    expect(describeCronSchedule("@weekly")).toBe("Weekly");
    expect(describeCronSchedule("*/10 * * * *")).toBe("Every 10 min");
    expect(describeCronSchedule("0 * * * *")).toBe("Hourly");
    expect(describeCronSchedule("30 9 * * *")).toBe("Daily at 09:30");
    expect(describeCronSchedule("15 3 * * 1")).toBe("15 3 * * 1");
  });

  it("uses the first enabled cron binding for the service cadence", () => {
    const record = service({
      bindings: [
        {
          id: "disabled",
          sourceKind: "cron",
          schedule: "@daily",
          enabled: false,
          routeKey: "cron.disabled",
        },
        ...service().bindings,
      ],
    });

    expect(agentServiceCadenceLabel(record)).toBe("Weekly");
  });

  it("derives disabled, running, failure, and unknown dot states", () => {
    expect(agentServiceDotState(service({ enabled: false }))).toBe("off");
    expect(agentServiceDotState(service({ lastRunStatus: "running" }))).toBe(
      "running",
    );
    expect(agentServiceDotState(service({ consecutiveFailures: 1 }))).toBe(
      "warn",
    );
    expect(agentServiceDotState(service({ consecutiveFailures: 2 }))).toBe(
      "failing",
    );
    expect(
      agentServiceDotState(service({ errors: ["health read failed"] })),
    ).toBe("unknown");
    expect(
      agentServiceDotTooltip(service({ errors: ["health read failed"] })),
    ).toContain("health read failed");
  });

  it("formats valid fire times and rejects absent or zero times", () => {
    expect(formatFireTime(null)).toBe("");
    expect(formatFireTime("nonsense")).toBe("");
    expect(formatFireTime("0001-01-01T00:00:00Z")).toBe("");
    expect(formatFireTime("2026-08-17T00:00:00Z")).not.toBe("");
  });
});
