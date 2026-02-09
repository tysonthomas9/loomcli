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
}

/**
 * Get the review type for an issue based on title patterns, status, and notes.
 * Returns null if the issue doesn't need review.
 */
export function getReviewType(issue: ReviewCheckable): ReviewType | null {
  const hasNeedReview = issue.title?.includes('[Need Review]') ?? false;
  const isReviewStatus = issue.status === 'review';
  const isBlockedWithNotes = issue.status === 'blocked' && !!issue.notes;

  // Plan review: Title contains [Need Review]
  if (hasNeedReview) {
    return 'plan';
  }

  // Code review: Status is review AND no [Need Review] in title
  if (isReviewStatus && !hasNeedReview) {
    return 'code';
  }

  // Needs help: Blocked with notes
  if (isBlockedWithNotes) {
    return 'help';
  }

  return null;
}
