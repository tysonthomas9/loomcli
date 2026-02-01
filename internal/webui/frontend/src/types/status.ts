/**
 * Issue status types.
 */

/**
 * Built-in issue statuses.
 */
export type KnownStatus =
  | 'open'
  | 'in_progress'
  | 'blocked'
  | 'deferred'
  | 'closed'
  | 'review'
  | 'tombstone'
  | 'pinned'
  | 'hooked';

/**
 * Status type that allows custom statuses.
 * Built-in statuses are type-checked, custom statuses are allowed via string.
 */
export type Status = KnownStatus | (string & {});

/**
 * Status constants for type-safe usage.
 */
export const StatusOpen: Status = 'open';
export const StatusInProgress: Status = 'in_progress';
export const StatusBlocked: Status = 'blocked';
export const StatusDeferred: Status = 'deferred';
export const StatusClosed: Status = 'closed';
export const StatusReview: Status = 'review';
export const StatusTombstone: Status = 'tombstone';
export const StatusPinned: Status = 'pinned';
export const StatusHooked: Status = 'hooked';

/**
 * All known status values for validation.
 */
export const KNOWN_STATUSES: readonly KnownStatus[] = [
  'open',
  'in_progress',
  'blocked',
  'deferred',
  'closed',
  'review',
  'tombstone',
  'pinned',
  'hooked',
] as const;

/**
 * Statuses that users can select in the UI.
 * Excludes system-only statuses (tombstone, pinned, hooked).
 */
export const USER_SELECTABLE_STATUSES: readonly KnownStatus[] = [
  'open',
  'in_progress',
  'blocked',
  'deferred',
  'review',
  'closed',
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
  return typeof status === 'string' && status.length > 0;
}
