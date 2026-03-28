/**
 * Shared helpers for integration tests.
 * Provides auth-aware API helpers for creating/closing test issues.
 */

import * as fs from "fs";
import * as path from "path";
import * as os from "os";

/** Base URL for API calls (configurable via env var). */
export const BASE_URL = process.env.LOOM_BASE_URL || "http://localhost:8080";

/**
 * Resolve API key from environment or key file.
 * Priority: LOOM_API_KEY env var > ~/.loom/webui-api-key file > empty string.
 */
export function resolveApiKey(): string {
  if (process.env.LOOM_API_KEY) return process.env.LOOM_API_KEY;
  try {
    return fs
      .readFileSync(path.join(os.homedir(), ".loom", "webui-api-key"), "utf-8")
      .trim();
  } catch {
    return "";
  }
}

const API_KEY = resolveApiKey();

/**
 * Build headers with optional auth and extra headers.
 */
export function authHeaders(
  extra?: Record<string, string>,
): Record<string, string> {
  const headers: Record<string, string> = { ...extra };
  if (API_KEY) headers["Authorization"] = `Bearer ${API_KEY}`;
  return headers;
}

/**
 * Generate unique ID for test isolation.
 */
export function generateTestId(): string {
  return `test-${Date.now()}-${Math.random().toString(36).substring(2, 11)}`;
}

/**
 * Create a test issue via the API.
 */
export async function createTestIssue(
  title: string,
  options?: { priority?: number },
): Promise<string> {
  const response = await fetch(`${BASE_URL}/api/issues`, {
    method: "POST",
    headers: authHeaders({ "Content-Type": "application/json" }),
    body: JSON.stringify({
      title,
      issue_type: "task",
      priority: options?.priority ?? 2,
    }),
  });

  if (!response.ok) {
    const text = await response.text();
    throw new Error(`Failed to create issue: ${response.status} - ${text}`);
  }

  const result = await response.json();
  if (!result.success) {
    throw new Error(`API error: ${result.error}`);
  }

  return result.data.id;
}

/**
 * Update issue status via the API.
 */
export async function updateIssueStatus(
  id: string,
  status: string,
): Promise<void> {
  const response = await fetch(`${BASE_URL}/api/issues/${id}`, {
    method: "PATCH",
    headers: authHeaders({ "Content-Type": "application/json" }),
    body: JSON.stringify({ status }),
  });

  if (!response.ok) {
    const text = await response.text();
    throw new Error(`Failed to update issue: ${response.status} - ${text}`);
  }
}

/**
 * Close an issue via the API.
 */
export async function closeTestIssue(id: string): Promise<void> {
  try {
    const response = await fetch(`${BASE_URL}/api/issues/${id}/close`, {
      method: "POST",
      headers: authHeaders(),
    });
    if (!response.ok && response.status !== 404) {
      console.warn(`Failed to close issue ${id}: ${response.status}`);
    }
  } catch {
    // Ignore network errors during cleanup
  }
}

// ---------------------------------------------------------------------------
// Workspace API helpers
// ---------------------------------------------------------------------------

export interface WorkspaceSummary {
  id: string;
  name: string;
  path: string;
  active: boolean;
  repo_count: number;
  is_default: boolean;
  backend?: string;
}

export interface WorkspaceRepo {
  name: string;
  path: string;
  default_branch: string;
  remote: string;
  groups: string[];
}

export interface WorkspaceData {
  name: string;
  path: string;
  repos: WorkspaceRepo[];
  groups: string[];
  agents: Array<{
    name: string;
    repos: string[];
    repo_groups: string[];
    cross_repo: boolean;
  }>;
  workspaces: WorkspaceSummary[];
  workspace_order?: string[];
  default_workspace: string;
}

export interface WorkspaceResponse {
  success: boolean;
  data?: WorkspaceData;
  error?: string;
}

export interface WorkspaceListResponse {
  success: boolean;
  workspaces: Array<{
    id: string;
    name: string;
    path: string;
    active: boolean;
  }>;
}

/**
 * List all workspaces via GET /api/workspaces.
 */
export async function listWorkspaces(): Promise<WorkspaceListResponse> {
  const response = await fetch(`${BASE_URL}/api/workspaces`, {
    headers: authHeaders(),
  });
  if (!response.ok) {
    const text = await response.text();
    throw new Error(`Failed to list workspaces: ${response.status} - ${text}`);
  }
  return response.json();
}

/**
 * Get the active workspace topology via GET /api/workspaces/active.
 */
export async function getActiveWorkspace(): Promise<WorkspaceResponse> {
  const response = await fetch(`${BASE_URL}/api/workspaces/active`, {
    headers: authHeaders(),
  });
  if (!response.ok) {
    const text = await response.text();
    throw new Error(
      `Failed to get active workspace: ${response.status} - ${text}`,
    );
  }
  return response.json();
}

/**
 * Get a specific workspace by ID via GET /api/workspaces/{id}.
 */
export async function getWorkspaceById(id: string): Promise<WorkspaceResponse> {
  const response = await fetch(
    `${BASE_URL}/api/workspaces/${encodeURIComponent(id)}`,
    {
      headers: authHeaders(),
    },
  );
  if (!response.ok) {
    const text = await response.text();
    throw new Error(`Failed to get workspace: ${response.status} - ${text}`);
  }
  return response.json();
}

/**
 * Create a test workspace via POST /api/workspaces.
 * Returns the raw Response for status code assertions.
 *
 * Workspace creation can be slow (git init, config writes). If the server's
 * HTTP write timeout closes the connection before the handler responds, this
 * helper polls GET /api/workspaces until the workspace appears and synthesizes
 * a 201 response with the workspace data.
 */
export async function createTestWorkspace(
  name: string,
  options?: { type?: string; repos?: string[]; clone_urls?: string[] },
): Promise<Response> {
  try {
    return await fetch(`${BASE_URL}/api/workspaces`, {
      method: "POST",
      headers: authHeaders({ "Content-Type": "application/json" }),
      body: JSON.stringify({
        name,
        type: options?.type ?? "empty",
        repos: options?.repos,
        clone_urls: options?.clone_urls,
      }),
    });
  } catch (err) {
    // The server may drop the TCP connection (e.g. proxy timeout, idle
    // connection reset) before responding even though the create handler
    // succeeded on the backend. Poll the workspace list to check. Retry
    // for up to 60 seconds.
    const deadline = Date.now() + 60_000;
    while (Date.now() < deadline) {
      await new Promise((r) => setTimeout(r, 2_000));
      try {
        const active = await fetch(`${BASE_URL}/api/workspaces/active`, {
          headers: authHeaders(),
        });
        if (!active.ok) continue;
        const body = await active.json();
        const ws = body?.data?.workspaces?.find(
          (w: { name: string }) => w.name === name,
        );
        if (ws) {
          // Workspace was created — synthesize a 201 response
          return new Response(
            JSON.stringify({ success: true, data: body.data }),
            {
              status: 201,
              headers: { "Content-Type": "application/json" },
            },
          );
        }
      } catch {
        // Network error during poll — keep trying
      }
    }
    // Creation truly failed
    throw err;
  }
}

/**
 * Delete a test workspace via DELETE /api/workspaces/{id}. Swallows 404.
 */
export async function deleteTestWorkspace(id: string): Promise<void> {
  try {
    const response = await fetch(
      `${BASE_URL}/api/workspaces/${encodeURIComponent(id)}`,
      {
        method: "DELETE",
        headers: authHeaders(),
      },
    );
    if (!response.ok && response.status !== 404) {
      console.warn(`Failed to delete workspace ${id}: ${response.status}`);
    }
  } catch {
    // Ignore network errors during cleanup
  }
}

/**
 * Set the default workspace via PUT /api/workspaces/default.
 */
export async function setDefaultWorkspace(name: string): Promise<Response> {
  return fetch(`${BASE_URL}/api/workspaces/default`, {
    method: "PUT",
    headers: authHeaders({ "Content-Type": "application/json" }),
    body: JSON.stringify({ name }),
  });
}

/**
 * Clear the default workspace via DELETE /api/workspaces/default.
 */
export async function clearDefaultWorkspace(): Promise<Response> {
  return fetch(`${BASE_URL}/api/workspaces/default`, {
    method: "DELETE",
    headers: authHeaders(),
  });
}

/**
 * Rename a workspace via PATCH /api/workspaces/{id}/name.
 */
export async function renameWorkspace(
  id: string,
  newName: string,
): Promise<Response> {
  return fetch(`${BASE_URL}/api/workspaces/${encodeURIComponent(id)}/name`, {
    method: "PATCH",
    headers: authHeaders({ "Content-Type": "application/json" }),
    body: JSON.stringify({ new_name: newName }),
  });
}

/**
 * Reorder workspaces via PUT /api/workspaces/order.
 */
export async function reorderWorkspaces(order: string[]): Promise<Response> {
  return fetch(`${BASE_URL}/api/workspaces/order`, {
    method: "PUT",
    headers: authHeaders({ "Content-Type": "application/json" }),
    body: JSON.stringify({ order }),
  });
}

/**
 * Update workspace backend config via PATCH /api/workspaces/{id}/config/backend.
 */
export async function updateWorkspaceBackend(
  id: string,
  backend: string,
): Promise<Response> {
  return fetch(
    `${BASE_URL}/api/workspaces/${encodeURIComponent(id)}/config/backend`,
    {
      method: "PATCH",
      headers: authHeaders({ "Content-Type": "application/json" }),
      body: JSON.stringify({ backend }),
    },
  );
}
