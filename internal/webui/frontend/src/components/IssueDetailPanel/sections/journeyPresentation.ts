import { formatStatusLabel } from "@/utils/issue";

export type JourneyStage =
  | "Open"
  | "In progress"
  | "Stuck"
  | "Deferred"
  | "Review"
  | "Closed";

const JOURNEY_STAGE_BY_STATUS = {
  open: "Open",
  in_progress: "In progress",
  blocked: "Stuck",
  deferred: "Deferred",
  review: "Review",
  closed: "Closed",
} as const satisfies Readonly<Record<string, JourneyStage>>;

export const JOURNEY_DURATION_RESOLUTION_MS = 1_000;

function normalizedStatus(status: string | null | undefined): string {
  return status?.trim().toLowerCase() ?? "";
}

export function journeyStageForStatus(
  status: string | null | undefined,
): JourneyStage | null {
  const normalized = normalizedStatus(status);
  return (
    JOURNEY_STAGE_BY_STATUS[
      normalized as keyof typeof JOURNEY_STAGE_BY_STATUS
    ] ?? null
  );
}

/**
 * Panel-local status vocabulary. The shared formatter intentionally keeps the
 * dependency-oriented "Blocked" label used by the rest of the product.
 */
export function formatJourneyStatusLabel(status: string): string {
  const normalized = normalizedStatus(status);
  return normalized === "blocked"
    ? JOURNEY_STAGE_BY_STATUS.blocked
    : formatStatusLabel(status);
}

export function formatJourneyDuration(durationMs: number): string {
  const totalSeconds = Math.max(
    0,
    Math.floor(durationMs / JOURNEY_DURATION_RESOLUTION_MS),
  );
  if (totalSeconds < 60) return `${totalSeconds}s`;

  const totalMinutes = Math.floor(totalSeconds / 60);
  if (totalMinutes < 60) return `${totalMinutes}m`;

  const totalHours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  if (totalHours < 24) {
    return minutes === 0 ? `${totalHours}h` : `${totalHours}h ${minutes}m`;
  }

  const days = Math.floor(totalHours / 24);
  const hours = totalHours % 24;
  return hours === 0 ? `${days}d` : `${days}d ${hours}h`;
}

export function hasDisplayableJourneyDuration(durationMs: number): boolean {
  return formatJourneyDuration(durationMs) !== "0s";
}
