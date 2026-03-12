/**
 * API functions for git diff endpoints.
 * Interfaces with GET /api/agents/{name}/diff/* endpoints.
 */

import { get, ApiError } from "./client";

// ============= Types =============

export interface DiffCommit {
  hash: string;
  short_hash: string;
  subject: string;
  author: string;
  author_email: string;
  date: string; // ISO 8601 string from backend
}

export interface DiffFile {
  path: string;
  status: "M" | "A" | "D" | "R";
  old_path?: string; // present for renames
}

export interface DiffFileContent {
  old_content: string;
  new_content: string;
  is_binary: boolean;
  too_large: boolean;
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
  agentName: string,
  limit?: number,
): Promise<DiffCommit[]> {
  let url = `/api/agents/${encodeURIComponent(agentName)}/diff/commits`;
  if (limit !== undefined) {
    url += `?limit=${limit}`;
  }
  const response = await get<ApiResult<{ commits: DiffCommit[] }>>(url);
  return unwrap(response).commits;
}

/**
 * Fetch changed files between two commits.
 * GET /api/agents/{name}/diff/files?from=X&to=Y
 */
export async function fetchDiffFiles(
  agentName: string,
  from: string,
  to: string,
): Promise<DiffFile[]> {
  const url = `/api/agents/${encodeURIComponent(agentName)}/diff/files?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`;
  const response = await get<ApiResult<{ files: DiffFile[] }>>(url);
  return unwrap(response).files;
}

/**
 * Fetch diff content for a single file between two commits.
 * GET /api/agents/{name}/diff/file?path=X&from=Y&to=Z
 */
export async function fetchDiffFile(
  agentName: string,
  path: string,
  from: string,
  to: string,
): Promise<DiffFileContent> {
  const url = `/api/agents/${encodeURIComponent(agentName)}/diff/file?path=${encodeURIComponent(path)}&from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`;
  const response = await get<ApiResult<DiffFileContent>>(url);
  return unwrap(response);
}
