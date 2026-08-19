import type {
  AgentServiceBindingDTO,
  AgentServiceDTO,
} from "@/api/agentServices";

const CRON_NICKNAME_LABELS: Readonly<Record<string, string>> = {
  "@yearly": "Yearly",
  "@annually": "Yearly",
  "@monthly": "Monthly",
  "@weekly": "Weekly",
  "@daily": "Daily",
  "@midnight": "Daily at midnight",
  "@hourly": "Hourly",
};

/** Human-readable cadence for common cron descriptors and five-field crons. */
export function describeCronSchedule(expr: string | undefined): string {
  const raw = (expr ?? "").trim();
  const nickname = CRON_NICKNAME_LABELS[raw.toLowerCase()];
  if (nickname) return nickname;
  if (raw.toLowerCase().startsWith("@every ")) {
    return `Every ${raw.slice(7).trim()}`;
  }

  const parts = raw.split(/\s+/);
  if (parts.length !== 5) return raw;
  const [minute, hour, dayOfMonth, month, dayOfWeek] = parts as [
    string,
    string,
    string,
    string,
    string,
  ];
  const wildcardDate = dayOfMonth === "*" && month === "*" && dayOfWeek === "*";

  const everyMinute = /^\*\/(\d+)$/.exec(minute);
  if (everyMinute && hour === "*" && wildcardDate) {
    return `Every ${everyMinute[1]} min`;
  }
  if (minute === "0" && hour === "*" && wildcardDate) return "Hourly";
  const everyHour = /^\*\/(\d+)$/.exec(hour);
  if (minute === "0" && everyHour && wildcardDate) {
    return `Every ${everyHour[1]} h`;
  }
  if (/^\d{1,2}$/.test(minute) && /^\d{1,2}$/.test(hour) && wildcardDate) {
    return `Daily at ${hour.padStart(2, "0")}:${minute.padStart(2, "0")}`;
  }
  return raw;
}

export function bindingCadenceLabel(binding: AgentServiceBindingDTO): string {
  if (
    binding.sourceKind.trim().toLowerCase() === "cron" &&
    binding.schedule.trim() !== ""
  ) {
    return describeCronSchedule(binding.schedule);
  }
  const sourceKind = binding.sourceKind.trim();
  return sourceKind ? `${sourceKind} trigger` : binding.routeKey.trim();
}

export function firstEnabledCronBinding(
  service: AgentServiceDTO,
): AgentServiceBindingDTO | undefined {
  return service.bindings.find(
    (binding) =>
      binding.enabled &&
      binding.sourceKind.trim().toLowerCase() === "cron" &&
      binding.schedule.trim() !== "",
  );
}

export function agentServiceCadenceLabel(service: AgentServiceDTO): string {
  const binding = firstEnabledCronBinding(service);
  return binding ? bindingCadenceLabel(binding) : "No enabled schedule";
}

/** Compact local date-time, or an empty string for absent/invalid instants. */
export function formatFireTime(iso: string | null | undefined): string {
  if (!iso) return "";
  const date = new Date(iso);
  const timestamp = date.getTime();
  if (Number.isNaN(timestamp) || timestamp <= 0) return "";
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export type AgentServiceDotState =
  | "idle"
  | "running"
  | "off"
  | "warn"
  | "failing"
  | "unknown";

export function agentServiceDotState(
  service: AgentServiceDTO,
): AgentServiceDotState {
  if (service.errors.length > 0) return "unknown";
  if (!service.enabled) return "off";
  if (service.consecutiveFailures >= 2) return "failing";
  if (service.consecutiveFailures === 1 || service.lastRunStatus === "failed") {
    return "warn";
  }
  if (
    service.lastRunStatus === "queued" ||
    service.lastRunStatus === "running" ||
    service.lastRunStatus === "suspended_awaiting_event"
  ) {
    return "running";
  }
  return "idle";
}

export function agentServiceHealthLabel(service: AgentServiceDTO): string {
  switch (agentServiceDotState(service)) {
    case "unknown":
      return "Health unavailable";
    case "off":
      return "Disabled";
    case "failing":
      return "Failing";
    case "warn":
      return "Last run failed";
    case "running":
      return service.lastRunStatus === "queued"
        ? "Queued"
        : service.lastRunStatus === "suspended_awaiting_event"
          ? "Suspended"
          : "Running";
    default:
      return "Enabled";
  }
}

export function agentServiceDotTooltip(service: AgentServiceDTO): string {
  const state = agentServiceDotState(service);
  if (state === "unknown") {
    return `Health unavailable — ${service.errors.join("; ")}`;
  }
  if (state === "off") return "Disabled";
  if (state === "failing") {
    return `Failing — ${service.consecutiveFailures} consecutive runs failed`;
  }
  if (state === "warn") return "Last run failed";
  if (state === "running") return agentServiceHealthLabel(service);

  const nextFire = formatFireTime(service.nextFireAt);
  if (nextFire) return `Enabled · next fire ${nextFire}`;
  return firstEnabledCronBinding(service)
    ? "Enabled"
    : "Enabled · no schedule configured";
}
