import { ApiError } from "@/types/common";

export const SERVICE_ID_PATTERN = /^[a-z0-9][a-z0-9-]{0,63}$/;
export const SERVICE_ID_RULE =
  "Use 1–64 lowercase letters, numbers, or hyphens; start with a letter or number.";
export const FILE_ACCESS_MESSAGE =
  "Agent changes require workspace file write access. Open Loom from its configured local frontend or ask a workspace editor.";
export const SCHEDULE_PRESETS = ["@hourly", "@daily", "@weekly"] as const;

export function serviceIDError(value: string): string | null {
  return SERVICE_ID_PATTERN.test(value.trim()) ? null : SERVICE_ID_RULE;
}

export function scheduleError(value: string): string | null {
  return value.trim() ? null : "Schedule is required.";
}

export function agentServiceMutationError(
  error: unknown,
  fallback: string,
): string {
  if (error instanceof ApiError && error.status === 403) {
    return FILE_ACCESS_MESSAGE;
  }
  if (error instanceof Error && error.message) return error.message;
  return fallback;
}
