/**
 * API functions for git diff endpoints.
 * Uses the untyped fetch wrapper because spec responses are Record<string, never>.
 */

import { get, ApiError, wsUrl } from "@/api/common";

// ============= Types =============

export interface DiffCommit {
  hash: string;
  short_hash: string;
  subject: string;
  author: string;
  email: string;
  date: string; // ISO 8601 string from backend
}

export interface DiffFile {
  path: string;
  status: "M" | "A" | "D" | "R";
  old_path?: string; // present for renames
  additions: number;
  deletions: number;
}

export interface DiffFilePatch {
  patch: string;
  is_binary: boolean;
  is_too_large: boolean;
  additions: number;
  deletions: number;
}

// ============= Response Types =============

interface ApiSuccess<T> {
  success: true;
  data: T;
}

interface ApiFailure {
  success: false;
  error: string;
}

type ApiResult<T> = ApiSuccess<T> | ApiFailure;

// ============= Helpers =============

function unwrap<T>(response: ApiResult<T>): T {
  if (!response.success) {
    throw new ApiError(0, response.error);
  }
  return response.data;
}

// ============= API Functions =============

/**
 * Fetch recent commits for an agent's worktree.
 * GET /api/agents/{name}/diff/commits?limit=N
 */
export async function fetchDiffCommits(
  workspaceId: string,
  agentName: string,
  limit?: number,
): Promise<DiffCommit[]> {
  let url = wsUrl(
    workspaceId,
    `/agents/${encodeURIComponent(agentName)}/diff/commits`,
  );
  if (limit !== undefined) {
    url += `?limit=${limit}`;
  }
  const response = await get<ApiResult<{ commits: DiffCommit[] }>>(url);
  return unwrap(response).commits;
}

/**
 * Fetch changed files between two commits.
 * GET /api/agents/{name}/diff/files?to=Y or ?from=X&to=Y
 */
export async function fetchDiffFiles(
  workspaceId: string,
  agentName: string,
  to: string,
  from?: string,
): Promise<DiffFile[]> {
  let url = wsUrl(
    workspaceId,
    `/agents/${encodeURIComponent(agentName)}/diff/files?to=${encodeURIComponent(to)}`,
  );
  if (from !== undefined) {
    url += `&from=${encodeURIComponent(from)}`;
  }
  const response = await get<ApiResult<{ files: DiffFile[] }>>(url);
  return unwrap(response).files;
}

/**
 * Fetch unified diff patch for a single file between two commits.
 * GET /api/agents/{name}/diff/file?path=X&to=Z or ?path=X&from=Y&to=Z
 */
export async function fetchDiffFile(
  workspaceId: string,
  agentName: string,
  path: string,
  to: string,
  from?: string,
): Promise<DiffFilePatch> {
  let url = wsUrl(
    workspaceId,
    `/agents/${encodeURIComponent(agentName)}/diff/file?path=${encodeURIComponent(path)}&to=${encodeURIComponent(to)}`,
  );
  if (from !== undefined) {
    url += `&from=${encodeURIComponent(from)}`;
  }
  const response = await get<ApiResult<DiffFilePatch>>(url);
  return unwrap(response);
}
