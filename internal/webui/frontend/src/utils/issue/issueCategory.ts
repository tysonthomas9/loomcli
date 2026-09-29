/**
 * Consolidated issue categorization predicates.
 * Single source of truth for determining issue status, review type, and workflow stage.
 *
 * SYNC: Function names and logic match Go predicates in internal/cli/taskfilter.go
 */

// --- Constants (match Go taskfilter.go) ---

export const NEEDS_REVISION_LABEL = "needs-revision";

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

const PR_KEY_PREFIX = "github:";
const PR_KEY_SEGMENT = /^[A-Za-z0-9_.-]+$/;

function validPrKeySegment(segment: string): boolean {
  return segment !== "." && segment !== ".." && PR_KEY_SEGMENT.test(segment);
}

/**
 * Canonical PR identity key: "github:<owner>/<repo>#<number>", lowercased,
 * built from the PR's base (registered) repository. Mirrors Go's
 * `internal/prref.Format`; returns null for an invalid reference.
 */
export function formatPrKey(
  owner: string,
  repo: string,
  number: number,
): string | null {
  const o = owner.trim();
  const r = repo.trim();
  if (!validPrKeySegment(o) || !validPrKeySegment(r)) return null;
  if (!Number.isInteger(number) || number <= 0) return null;
  return `${PR_KEY_PREFIX}${o.toLowerCase()}/${r.toLowerCase()}#${number}`;
}

/**
 * Parse a canonical ("github:owner/repo#N") or legacy ("owner/repo#N") PR key
 * into its lowercased parts. Mirrors Go's `internal/prref.Parse`.
 */
export function parsePrKey(
  key?: string | null,
): { owner: string; repo: string; number: number } | null {
  if (!key) return null;
  let raw = key.trim();
  if (raw.toLowerCase().startsWith(PR_KEY_PREFIX)) {
    raw = raw.slice(PR_KEY_PREFIX.length);
  }
  const match = /^([^/#]+)\/([^/#]+)#([1-9]\d*)$/.exec(raw);
  if (!match?.[1] || !match[2] || !match[3]) return null;
  if (!validPrKeySegment(match[1]) || !validPrKeySegment(match[2])) {
    return null;
  }
  return {
    owner: match[1].toLowerCase(),
    repo: match[2].toLowerCase(),
    number: Number.parseInt(match[3], 10),
  };
}

/**
 * Canonical PR key ("github:owner/repo#number") for a PR web URL.
 * Robust to URL variants that break exact-string matching (http vs https,
 * www host, trailing ".git", sub-paths like /pull/42/files, trailing slash).
 */
export function prKeyFromRef(ref?: string | null): string | null {
  if (!ref) return null;
  try {
    const url = new URL(ref);
    if (url.protocol !== "https:" && url.protocol !== "http:") return null;
    const host = url.hostname.toLowerCase().replace(/^www\./, "");
    if (host !== "github.com") return null;
    const match = url.pathname.match(
      /^\/([^/]+)\/([^/]+?)(?:\.git)?\/pulls?\/(\d+)(?:\/|$)/i,
    );
    if (!match) return null;
    return formatPrKey(match[1]!, match[2]!, Number.parseInt(match[3]!, 10));
  } catch {
    return null;
  }
}

/**
 * Canonical key for a listed PR: the server-issued `pr_key` when present,
 * otherwise derived from its URL (older servers, stub rows).
 */
export function pullRequestKey(pr: {
  pr_key?: string | null;
  url?: string | null;
}): string | null {
  const parsed = parsePrKey(pr.pr_key);
  if (parsed) return formatPrKey(parsed.owner, parsed.repo, parsed.number);
  return prKeyFromRef(pr.url);
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
