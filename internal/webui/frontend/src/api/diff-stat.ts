/**
 * API function for issue diff-stat endpoint.
 * Interfaces with GET /api/issues/{id}/git/diff-stat.
 */

import { get } from "./client";

// ============= Types =============

export interface IssueDiffStat {
  branch: string;
  added: number;
  removed: number;
}

// ============= API Functions =============

/**
 * Fetch diff statistics for an issue's agent worktree.
 * GET /api/issues/{id}/git/diff-stat
 */
export async function fetchIssueDiffStat(
  issueId: string,
): Promise<IssueDiffStat> {
  const url = `/api/issues/${encodeURIComponent(issueId)}/git/diff-stat`;
  return get<IssueDiffStat>(url);
}
