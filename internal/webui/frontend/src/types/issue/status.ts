/**
 * Issue status types.
 *
 * This file is the TypeScript half of one vocabulary. Go owns it — see
 * `builtinStatuses` in internal/types/enums.go — and the browser cannot import
 * Go, so the nine values are written out a second time here. That copy is
 * allowed; a copy that drifts is not. `TestFrontendStatusVocabulary` in
 * internal/types/status_frontend_parity_test.go reads this file and fails when
 * the two lists stop agreeing, values and order both, which is how the closed /
 * review pair came to be in a different order on each side without anyone
 * noticing. Add a status to Go and to this file in the same change.
 */

/**
 * Built-in issue statuses.
 * Order matches Go's canonical list (types.BuiltinStatuses).
 */
export type KnownStatus =
  | "open"
  | "in_progress"
  | "blocked"
  | "deferred"
  | "review"
  | "closed"
  | "tombstone"
  | "pinned"
  | "hooked";

/**
 * Status type that allows custom statuses.
 * Built-in statuses are type-checked, custom statuses are allowed via string.
 */
export type Status = KnownStatus | (string & {});

/**
 * Status constants for type-safe usage.
 * One per built-in status, named after Go's constants (types.StatusOpen, ...).
 */
export const StatusOpen: Status = "open";
export const StatusInProgress: Status = "in_progress";
export const StatusBlocked: Status = "blocked";
export const StatusDeferred: Status = "deferred";
export const StatusReview: Status = "review";
export const StatusClosed: Status = "closed";
export const StatusTombstone: Status = "tombstone";
export const StatusPinned: Status = "pinned";
export const StatusHooked: Status = "hooked";

/**
 * All known status values for validation.
 */
export const KNOWN_STATUSES: readonly KnownStatus[] = [
  "open",
  "in_progress",
  "blocked",
  "deferred",
  "review",
  "closed",
  "tombstone",
  "pinned",
  "hooked",
] as const;

/**
 * Statuses that users can select in the UI.
 * Excludes system-only statuses (tombstone, pinned, hooked).
 *
 * The Go counterpart is types.UserFacingStatuses(), the same cut of the same
 * list, and the parity test pins the two together. Order is load-bearing: it is
 * the order StatusDropdown renders its options in.
 */
export const USER_SELECTABLE_STATUSES: readonly KnownStatus[] = [
  "open",
  "in_progress",
  "blocked",
  "deferred",
  "review",
  "closed",
] as const;

/**
 * Type guard to check if a status is a known built-in status.
 */
export function isKnownStatus(status: string): status is KnownStatus {
  return KNOWN_STATUSES.includes(status as KnownStatus);
}

/**
 * Type guard to check if a value is a valid status (non-empty string).
 */
export function isValidStatus(status: unknown): status is Status {
  return typeof status === "string" && status.length > 0;
}
