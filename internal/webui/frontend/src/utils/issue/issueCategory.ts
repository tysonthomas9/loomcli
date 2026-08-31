/**
 * Consolidated issue categorization predicates.
 * Single source of truth for determining issue status, review type, and workflow stage.
 *
 * SYNC: Function names and logic match Go predicates in internal/cli/taskfilter.go
 */

// --- Constants (match Go taskfilter.go) ---

export const NEEDS_REVISION_LABEL = "needs-revision";

/**
 * Reserved label that parks an issue for a human: it stays open and workable
 * by a person, but no agent may select it. fleet-db enforces this server-side;
 * the client mirrors it so the parked state is visible on the board.
 *
 * Exact, lowercase string equality — no prefix matching, no case folding, so
 * "operator-notes" and "Operator" are ordinary labels.
 * SYNC: taskfilter.go OperatorLabel / HasOperatorLabel.
 */
export const OPERATOR_LABEL = "operator";

// --- Types ---

export type OpenStatus = "needs_plan" | "ready";

export type ReviewType = "plan" | "code" | "help";

// --- Predicate interfaces ---

interface OpenStatusCheckable {
  design?: string;
  design_artifact_id?: string;
  has_design?: boolean;
  labels?: string[];
}

interface ReviewCheckable {
  title: string;
  status?: string;
  notes?: string;
  external_ref?: string | null;
}

// --- Simple predicates ---

export function hasNeedsRevision(issue: { labels?: string[] }): boolean {
  return issue.labels?.includes(NEEDS_REVISION_LABEL) ?? false;
}

/**
 * Whether the issue is parked for an operator (agents will not select it).
 *
 * SYNC: Must match taskfilter.go HasOperatorLabel()
 */
export function hasOperatorLabel(issue: { labels?: string[] }): boolean {
  return issue.labels?.includes(OPERATOR_LABEL) ?? false;
}

/**
 * Whether an issue has a design, including collection responses that omit the
 * hydrated design body and expose only has_design.
 */
export function hasDesign(issue: OpenStatusCheckable): boolean {
  return (
    !!issue.design || !!issue.design_artifact_id || issue.has_design === true
  );
}

// --- Open status (was openStatus.ts — now checks labels) ---

/**
 * Get the open status for an issue.
 * Issues with a non-empty design AND no needs-revision label are "ready";
 * all others are "needs_plan".
 *
 * SYNC: Must match taskfilter.go NeedsPlan() / ReadyToImplement()
 */
export function getOpenStatus(issue: OpenStatusCheckable): OpenStatus {
  if (hasDesign(issue) && !hasNeedsRevision(issue)) {
    return "ready";
  }
  return "needs_plan";
}

// --- Review type (was reviewType.ts — moved here unchanged) ---

/**
 * Check if a reference URL points to a pull request.
 */
export function isPRUrl(ref?: string | null): boolean {
  if (!ref) return false;
  try {
    const url = new URL(ref);
    if (url.protocol !== "https:" && url.protocol !== "http:") return false;
    return url.pathname.includes("/pull/") || url.pathname.includes("/pulls/");
  } catch {
    return false;
  }
}

/** Normalize a PR URL for matching issue external_ref to GitHub list entries. */
export function normalizePrUrl(ref?: string | null): string | null {
  if (!isPRUrl(ref)) return null;
  try {
    const url = new URL(ref!);
    const path = url.pathname.replace(/\/$/, "").toLowerCase();
    return `${url.origin.toLowerCase()}${path}`;
  } catch {
    return ref?.trim().toLowerCase() ?? null;
  }
}

/**
 * Stable join key for a PR reference: "owner/repo#number".
 * Robust to URL variants that break exact-string matching (http vs https,
 * www host, trailing ".git", sub-paths like /pull/42/files, trailing slash).
 */
export function prKeyFromRef(ref?: string | null): string | null {
  if (!ref) return null;
  try {
    const url = new URL(ref);
    if (url.protocol !== "https:" && url.protocol !== "http:") return null;
    const match = url.pathname.match(
      /^\/([^/]+)\/([^/]+?)(?:\.git)?\/pulls?\/(\d+)(?:\/|$)/i,
    );
    if (!match) return null;
    return `${match[1]!.toLowerCase()}/${match[2]!.toLowerCase()}#${match[3]!}`;
  } catch {
    return null;
  }
}

/**
 * Get the review type for an issue based on status, external_ref, and notes.
 * Returns null if the issue doesn't need review.
 */
export function getReviewType(issue: ReviewCheckable): ReviewType | null {
  const isReviewStatus = issue.status === "review";
  const isBlockedWithNotes = issue.status === "blocked" && !!issue.notes;
  const hasExternalPR = isPRUrl(issue.external_ref);

  // Code review: status=review AND external_ref is a PR URL
  if (isReviewStatus && hasExternalPR) {
    return "code";
  }

  // Plan review: status=review AND no PR URL
  if (isReviewStatus) {
    return "plan";
  }

  // Needs help: Blocked with notes
  if (isBlockedWithNotes) {
    return "help";
  }

  return null;
}
