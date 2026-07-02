import { describe, expect, it } from "vitest";

import type { TriggerBinding } from "@/api";

import {
  bindingCadenceLabel,
  bindingDotState,
  bindingDotTooltip,
  bindingHealth,
  bindingKindLabel,
  describeCronSchedule,
  formatFireTime,
} from "../bindingDisplay";

function binding(overrides: Partial<TriggerBinding> = {}): TriggerBinding {
  return {
    workspace_key: "SANDBOX",
    binding_id: "s2-review-loop",
    name: "cron:s2-review-loop",
    source_kind: "cron",
    route_key: "cron:s2-review-loop",
    driver_id: "review-loop-agent",
    driver_version_id: "review-loop-agent-v-1",
    enabled: true,
    schedule: "*/10 * * * *",
    ...overrides,
  };
}

describe("describeCronSchedule", () => {
  it("humanizes every-N-minutes", () => {
    expect(describeCronSchedule("*/10 * * * *")).toBe("Every 10 min");
    expect(describeCronSchedule("*/5 * * * *")).toBe("Every 5 min");
  });

  it("humanizes hourly and every-N-hours", () => {
    expect(describeCronSchedule("0 * * * *")).toBe("Hourly");
    expect(describeCronSchedule("0 */2 * * *")).toBe("Every 2 h");
  });

  it("humanizes daily-at-HH:MM", () => {
    expect(describeCronSchedule("30 9 * * *")).toBe("Daily at 09:30");
  });

  it("falls back to the raw expression for anything else", () => {
    expect(describeCronSchedule("15 3 * * 1")).toBe("15 3 * * 1");
    expect(describeCronSchedule("not a cron")).toBe("not a cron");
    expect(describeCronSchedule(undefined)).toBe("");
  });
});

describe("bindingCadenceLabel", () => {
  it("uses the humanized schedule for cron bindings", () => {
    expect(bindingCadenceLabel(binding())).toBe("Every 10 min");
  });

  it("uses event patterns for event-driven bindings", () => {
    expect(
      bindingCadenceLabel(
        binding({
          source_kind: "github",
          schedule: undefined,
          event_type_patterns: ["pull_request.opened", "pull_request.synchronize"],
        }),
      ),
    ).toBe("pull_request.opened, pull_request.synchronize");
  });

  it("falls back to a source-kind label when nothing else is present", () => {
    expect(
      bindingCadenceLabel(
        binding({ source_kind: "internal", schedule: undefined }),
      ),
    ).toBe("internal trigger");
  });
});

describe("bindingKindLabel", () => {
  it("maps cron to Scheduled and titlecases others", () => {
    expect(bindingKindLabel(binding())).toBe("Scheduled");
    expect(bindingKindLabel(binding({ source_kind: "github" }))).toBe("Github");
    expect(bindingKindLabel(binding({ source_kind: "" }))).toBe("Event");
  });
});

describe("bindingDotState / tooltip", () => {
  it("is idle when enabled+healthy and off when disabled", () => {
    expect(bindingDotState(binding({ enabled: true }))).toBe("idle");
    expect(bindingDotState(binding({ enabled: false }))).toBe("off");
  });

  it("reflects failure health (Decision 7): 1 → warn, 2+ → failing", () => {
    expect(
      bindingDotState(binding({ enabled: true, consecutive_failures: 0 })),
    ).toBe("idle");
    expect(
      bindingDotState(binding({ enabled: true, consecutive_failures: 1 })),
    ).toBe("warn");
    expect(
      bindingDotState(binding({ enabled: true, consecutive_failures: 2 })),
    ).toBe("failing");
    expect(
      bindingDotState(binding({ enabled: true, consecutive_failures: 5 })),
    ).toBe("failing");
  });

  it("a disabled binding is off regardless of failures", () => {
    expect(
      bindingDotState(binding({ enabled: false, consecutive_failures: 3 })),
    ).toBe("off");
  });

  it("tooltip reflects enabled + next fire, and failure state", () => {
    expect(bindingDotTooltip(binding({ enabled: false }))).toBe("Disabled");
    expect(bindingDotTooltip(binding({ enabled: true, next_fire_at: undefined }))).toBe(
      "Enabled",
    );
    expect(
      bindingDotTooltip(
        binding({ enabled: true, next_fire_at: "2026-07-02T02:10:00Z" }),
      ),
    ).toContain("Enabled · next fire ");
    expect(
      bindingDotTooltip(binding({ enabled: true, consecutive_failures: 1 })),
    ).toBe("Last run failed");
    expect(
      bindingDotTooltip(binding({ enabled: true, consecutive_failures: 3 })),
    ).toContain("Failing");
  });
});

describe("bindingHealth", () => {
  it("derives the detail-header pill state + label from failure health", () => {
    expect(bindingHealth(binding({ enabled: true })).state).toBe("idle");
    expect(bindingHealth(binding({ enabled: true })).label).toBe("Enabled");
    expect(bindingHealth(binding({ enabled: false })).label).toBe("Disabled");
    expect(
      bindingHealth(binding({ enabled: true, consecutive_failures: 1 })).state,
    ).toBe("warn");
    const failing = bindingHealth(
      binding({ enabled: true, consecutive_failures: 2 }),
    );
    expect(failing.state).toBe("failing");
    expect(failing.label).toBe("Failing");
  });
});

describe("formatFireTime", () => {
  it("returns empty string for missing/invalid input", () => {
    expect(formatFireTime(undefined)).toBe("");
    expect(formatFireTime(null)).toBe("");
    expect(formatFireTime("nonsense")).toBe("");
  });

  it("treats the Go zero time (unset time.Time) as absent", () => {
    expect(formatFireTime("0001-01-01T00:00:00Z")).toBe("");
  });

  it("formats a valid ISO instant", () => {
    expect(formatFireTime("2026-07-02T02:10:00Z")).not.toBe("");
  });
});
