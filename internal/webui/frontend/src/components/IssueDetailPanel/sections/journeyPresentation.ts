export type JourneyStage =
  | "Open"
  | "In progress"
  | "Deferred"
  | "Review"
  | "Closed";

const JOURNEY_STAGE_BY_STATUS = {
  open: "Open",
  in_progress: "In progress",
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

export function formatJourneyDuration(durationMs: number): string {
  const totalSeconds = Math.max(
    0,
    Math.floor(durationMs / JOURNEY_DURATION_RESOLUTION_MS),
  );
  if (totalSeconds < 60) return `${totalSeconds}s`;

  const totalMinutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  if (totalMinutes < 60) {
    return `${totalMinutes}m ${String(seconds).padStart(2, "0")}s`;
  }

  const totalHours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  if (totalHours < 24) {
    return `${totalHours}h ${String(minutes).padStart(2, "0")}m ${String(seconds).padStart(2, "0")}s`;
  }

  const days = Math.floor(totalHours / 24);
  const hours = totalHours % 24;
  return `${days}d ${String(hours).padStart(2, "0")}h ${String(minutes).padStart(2, "0")}m`;
}

export function formatJourneyClock(iso: string): string {
  const parsed = Date.parse(iso);
  if (!Number.isFinite(parsed)) return "";

  const date = new Date(parsed);
  return [date.getHours(), date.getMinutes(), date.getSeconds()]
    .map((part) => String(part).padStart(2, "0"))
    .join(":");
}
