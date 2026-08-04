/**
 * Common types shared across the frontend.
 */

/**
 * ISO 8601 date string (e.g., "2024-01-15T10:30:00Z").
 * All timestamps from the API are in this format.
 */
export type ISODateString = string;

/**
 * Priority levels from 0 (critical/P0) to 4 (backlog/P4).
 * Note: 0 is a valid priority (P0 = critical), not "unset".
 */
export type Priority = 0 | 1 | 2 | 3 | 4;

/**
 * Duration in nanoseconds (matches Go time.Duration JSON serialization).
 */
export type Duration = number;
