/**
 * Display helpers for trigger-binding "agents" (workflow-plane agents).
 *
 * A binding is surfaced as a first-class agent in the unified UI (sidebar row +
 * detail page). These pure helpers turn a binding's schedule / event patterns
 * into human-readable labels so the sidebar meta line and the detail info pane
 * read the same way, and format the computed next-fire instant.
 */

import type { TriggerBinding } from "@/api";

/**
 * Human-readable cadence for a 5-field cron expression. Covers the common
 * shapes (every-N-minutes, hourly, every-N-hours, daily-at-HH:MM) and falls
 * back to the raw expression verbatim — never a fabricated description.
 */
export function describeCronSchedule(expr: string | undefined): string {
  const raw = (expr ?? "").trim();
  const parts = raw.split(/\s+/);
  if (parts.length !== 5) return raw;
  const [min, hour, dom, mon, dow] = parts as [
    string,
    string,
    string,
    string,
    string,
  ];
  const wildcardDate = dom === "*" && mon === "*" && dow === "*";

  const everyMin = /^\*\/(\d+)$/.exec(min);
  if (everyMin && hour === "*" && wildcardDate) {
    return `Every ${everyMin[1]} min`;
  }
  if (min === "0" && hour === "*" && wildcardDate) return "Hourly";
  const everyHour = /^\*\/(\d+)$/.exec(hour);
  if (min === "0" && everyHour && wildcardDate) return `Every ${everyHour[1]} h`;
  if (/^\d{1,2}$/.test(min) && /^\d{1,2}$/.test(hour) && wildcardDate) {
    return `Daily at ${hour.padStart(2, "0")}:${min.padStart(2, "0")}`;
  }
  return raw;
}

/**
 * The primary cadence/trigger label for a binding's meta line: humanized cron
 * for schedule bindings, the event-type patterns for event-driven bindings,
 * else the source kind or route as a last honest resort.
 */
export function bindingCadenceLabel(b: TriggerBinding): string {
  if (b.source_kind === "cron" && (b.schedule ?? "").trim() !== "") {
    return describeCronSchedule(b.schedule);
  }
  const patterns = b.event_type_patterns ?? [];
  if (patterns.length > 0) return patterns.join(", ");
  if ((b.source_kind ?? "").trim() !== "") return `${b.source_kind} trigger`;
  return (b.route_key ?? "").trim();
}

/** Short "kind" tag for a binding (cron / event source name). */
export function bindingKindLabel(b: TriggerBinding): string {
  const kind = (b.source_kind ?? "").trim();
  if (kind === "cron") return "Scheduled";
  if (kind === "") return "Event";
  return kind.charAt(0).toUpperCase() + kind.slice(1);
}

/**
 * Format an ISO instant as a compact local date-time, or "" when absent or
 * invalid. Go zero times ("0001-01-01T00:00:00Z" — how the backend serializes
 * an unset time.Time, e.g. started_at on a still-queued run) count as absent:
 * any pre-epoch instant is not a real event time.
 */
export function formatFireTime(iso: string | null | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  const t = d.getTime();
  if (Number.isNaN(t) || t <= 0) return "";
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/**
 * Sidebar status-dot state for a binding (Decision 7). A disabled binding is
 * "off"; an enabled one reflects recent run health from the list-computed
 * consecutive_failures: 2+ consecutive failures → "failing" (red), exactly 1 →
 * "warn" (amber), else "idle" (green, waiting for its next fire).
 */
export type BindingDotState = "idle" | "off" | "warn" | "failing";

export function bindingDotState(b: TriggerBinding): BindingDotState {
  if (!b.enabled) return "off";
  const failures = b.consecutive_failures ?? 0;
  if (failures >= 2) return "failing";
  if (failures === 1) return "warn";
  return "idle";
}

/** Tooltip text for a binding's sidebar status dot. */
export function bindingDotTooltip(b: TriggerBinding): string {
  if (!b.enabled) return "Disabled";
  const failures = b.consecutive_failures ?? 0;
  if (failures >= 2) return `Failing — ${failures} consecutive runs failed`;
  if (failures === 1) return "Last run failed";
  const next = formatFireTime(b.next_fire_at);
  return next ? `Enabled · next fire ${next}` : "Enabled";
}

/**
 * Health descriptor for the WorkflowAgentDetail header pill: `state` drives the
 * pill color (shared with the sidebar dot), `label` the text, `tooltip` the
 * hover detail. One source of truth so the sidebar and detail agree.
 */
export interface BindingHealth {
  state: BindingDotState;
  label: string;
  tooltip: string;
}

export function bindingHealth(b: TriggerBinding): BindingHealth {
  const state = bindingDotState(b);
  const tooltip = bindingDotTooltip(b);
  const label =
    state === "off"
      ? "Disabled"
      : state === "failing"
        ? "Failing"
        : state === "warn"
          ? "Last run failed"
          : "Enabled";
  return { state, label, tooltip };
}
