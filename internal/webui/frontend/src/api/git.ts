/**
 * API functions for git status endpoints.
 * Interfaces with GET /api/agents/{name}/git/status endpoint.
 */

import { get } from "./client";

// ============= Types =============

export interface GitStatus {
  branch: string;
  target_branch: string;
  is_clean: boolean;
  ahead: number;
  behind: number;
  changed_files: string[];
  conflicted_files: string[];
  has_conflicts: boolean;
  stash_count: number;
}

// ============= API Functions =============

/**
 * Fetch git status for an agent's worktree.
 * GET /api/agents/{name}/git/status
 */
export async function fetchGitStatus(agentName: string): Promise<GitStatus> {
  const url = `/api/agents/${encodeURIComponent(agentName)}/git/status`;
  return get<GitStatus>(url);
}
