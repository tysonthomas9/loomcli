/**
 * API functions for agent worktree file operations.
 * Interfaces with GET/PUT /api/agents/{name}/files/* endpoints.
 */

import { get, put } from "./client";

// ============= Types =============

export interface FileEntry {
  name: string;
  is_dir: boolean;
  size: number;
  mod_time: string; // RFC3339
}

export interface DirListData {
  path: string;
  entries: FileEntry[];
}

export interface FileReadData {
  path: string;
  content?: string; // omitted when binary is true
  size: number;
  binary: boolean;
}

// ============= API Functions =============

/**
 * List files in an agent worktree directory (one level).
 * GET /api/agents/{name}/files/tree?path=
 */
export async function listWorktreeDir(
  agentName: string,
  path?: string,
): Promise<DirListData> {
  let url = `/api/agents/${encodeURIComponent(agentName)}/files/tree`;
  if (path !== undefined && path !== "") {
    url += `?path=${encodeURIComponent(path)}`;
  }
  return get<DirListData>(url);
}

/**
 * Read a file from an agent worktree.
 * GET /api/agents/{name}/files?path=
 */
export async function readWorktreeFile(
  agentName: string,
  path: string,
): Promise<FileReadData> {
  const url = `/api/agents/${encodeURIComponent(agentName)}/files?path=${encodeURIComponent(path)}`;
  return get<FileReadData>(url);
}

/**
 * Write a file to an agent worktree (atomic write).
 * PUT /api/agents/{name}/files?path=
 */
export async function writeWorktreeFile(
  agentName: string,
  path: string,
  content: string,
): Promise<void> {
  const url = `/api/agents/${encodeURIComponent(agentName)}/files?path=${encodeURIComponent(path)}`;
  await put<{ success: boolean }>(url, { content });
}
