/**
 * API function for issue diff-stat endpoint.
 * Uses legacy fetch wrapper (spec responses are untyped Record<string, never>).
 */

import { get, wsUrl } from "./client";

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
  workspaceId: string,
  issueId: string,
): Promise<IssueDiffStat> {
  const url = wsUrl(
    workspaceId,
    `/issues/${encodeURIComponent(issueId)}/git/diff-stat`,
  );
  return get<IssueDiffStat>(url);
}

/**
 * Fetch diff statistics for an agent's worktree by agent name.
 * GET /api/agents/{name}/git/diff-stat
 */
export async function fetchAgentDiffStat(
  workspaceId: string,
  agentName: string,
): Promise<IssueDiffStat> {
  const url = wsUrl(
    workspaceId,
    `/agents/${encodeURIComponent(agentName)}/git/diff-stat`,
  );
  return get<IssueDiffStat>(url);
}
