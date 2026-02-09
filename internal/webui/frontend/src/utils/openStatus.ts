/**
 * Shared open status utility.
 * Used by IssueCard to distinguish whether an open issue needs planning or is ready for implementation.
 */

/**
 * Open status for issues in the Open column.
 */
export type OpenStatus = 'needs_plan' | 'ready';

/**
 * Minimal fields needed to determine open status.
 */
interface OpenStatusCheckable {
  design?: string;
}

/**
 * Get the open status for an issue based on whether it has a design.
 * Issues with a non-empty design are "ready" for implementation;
 * those without are "needs_plan".
 */
export function getOpenStatus(issue: OpenStatusCheckable): OpenStatus {
  return issue.design ? 'ready' : 'needs_plan';
}
