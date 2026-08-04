/**
 * Status bucket helpers shared by the Open Queue rail, epic detail, and
 * other status-distribution surfaces.
 */

/** The five buckets used by filter pills and distribution bars. */
export type StatusBucket =
  | "in_progress"
  | "open"
  | "review"
  | "blocked"
  | "done";

/** Collapse an issue status into one of the filter/distribution buckets. */
export function statusBucket(status: string): StatusBucket {
  const s = status.toLowerCase();
  if (s === "in_progress" || s === "active") return "in_progress";
  if (s === "closed" || s === "done") return "done";
  if (s === "blocked") return "blocked";
  if (s === "review") return "review";
  return "open";
}
