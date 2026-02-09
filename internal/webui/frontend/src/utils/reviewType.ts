/**
 * Shared review type utility.
 * Used by both IssueCard and IssueDetailPanel to determine if an issue needs human review.
 */

/**
 * Review type for issues that need human attention.
 */
export type ReviewType = 'plan' | 'code' | 'help';

/**
 * Minimal fields needed to determine review type.
 * Accepts both Issue and IssueDetails.
 */
interface ReviewCheckable {
  title: string;
  status?: string;
  notes?: string;
  external_ref?: string | null;
}

/**
 * Check if a string is a GitHub PR URL.
 */
function isPRUrl(ref?: string | null): boolean {
  if (!ref) return false;
  return ref.includes('/pull/') || ref.includes('/pulls/');
}

/**
 * Get the review type for an issue based on status, external_ref, and notes.
 * Returns null if the issue doesn't need review.
 *
 * Detection logic:
 * - Code review: status=review AND external_ref is a PR URL
 * - Plan review: status=review AND no PR URL in external_ref
 * - Needs help: status=blocked AND has notes
 */
export function getReviewType(issue: ReviewCheckable): ReviewType | null {
  const isReviewStatus = issue.status === 'review';
  const isBlockedWithNotes = issue.status === 'blocked' && !!issue.notes;
  const hasExternalPR = isPRUrl(issue.external_ref);

  // Code review: status=review AND external_ref is a PR URL
  if (isReviewStatus && hasExternalPR) {
    return 'code';
  }

  // Plan review: status=review AND no PR URL
  if (isReviewStatus) {
    return 'plan';
  }

  // Needs help: Blocked with notes
  if (isBlockedWithNotes) {
    return 'help';
  }

  return null;
}
