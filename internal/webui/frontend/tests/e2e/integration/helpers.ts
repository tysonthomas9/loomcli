/**
 * Shared helpers for integration tests.
 * Provides auth-aware API helpers for creating/closing test issues.
 */

import * as fs from 'fs'
import * as path from 'path'
import * as os from 'os'

/** Base URL for API calls (configurable via env var). */
export const BASE_URL = process.env.LOOM_BASE_URL || 'http://localhost:8080'

/**
 * Resolve API key from environment or key file.
 * Priority: LOOM_API_KEY env var > ~/.loom/webui-api-key file > empty string.
 */
export function resolveApiKey(): string {
  if (process.env.LOOM_API_KEY) return process.env.LOOM_API_KEY
  try {
    return fs.readFileSync(path.join(os.homedir(), '.loom', 'webui-api-key'), 'utf-8').trim()
  } catch {
    return ''
  }
}

const API_KEY = resolveApiKey()

/**
 * Build headers with optional auth and extra headers.
 */
export function authHeaders(extra?: Record<string, string>): Record<string, string> {
  const headers: Record<string, string> = { ...extra }
  if (API_KEY) headers['Authorization'] = `Bearer ${API_KEY}`
  return headers
}

/**
 * Generate unique ID for test isolation.
 */
export function generateTestId(): string {
  return `test-${Date.now()}-${Math.random().toString(36).substring(2, 11)}`
}

/**
 * Create a test issue via the API.
 */
export async function createTestIssue(title: string, options?: { priority?: number }): Promise<string> {
  const response = await fetch(`${BASE_URL}/api/issues`, {
    method: 'POST',
    headers: authHeaders({ 'Content-Type': 'application/json' }),
    body: JSON.stringify({
      title,
      issue_type: 'task',
      priority: options?.priority ?? 2,
    }),
  })

  if (!response.ok) {
    const text = await response.text()
    throw new Error(`Failed to create issue: ${response.status} - ${text}`)
  }

  const result = await response.json()
  if (!result.success) {
    throw new Error(`API error: ${result.error}`)
  }

  return result.data.id
}

/**
 * Update issue status via the API.
 */
export async function updateIssueStatus(id: string, status: string): Promise<void> {
  const response = await fetch(`${BASE_URL}/api/issues/${id}`, {
    method: 'PATCH',
    headers: authHeaders({ 'Content-Type': 'application/json' }),
    body: JSON.stringify({ status }),
  })

  if (!response.ok) {
    const text = await response.text()
    throw new Error(`Failed to update issue: ${response.status} - ${text}`)
  }
}

/**
 * Close an issue via the API.
 */
export async function closeTestIssue(id: string): Promise<void> {
  try {
    const response = await fetch(`${BASE_URL}/api/issues/${id}/close`, {
      method: 'POST',
      headers: authHeaders(),
    })
    if (!response.ok && response.status !== 404) {
      console.warn(`Failed to close issue ${id}: ${response.status}`)
    }
  } catch {
    // Ignore network errors during cleanup
  }
}

// =============================================================================
// Workspace API Types
// =============================================================================

/** Workspace summary item in the workspace list. */
export interface WorkspaceSummary {
  name: string
  path: string
  active: boolean
  repo_count: number
  is_default: boolean
  backend?: string
}

/** Repo entry within a workspace. */
export interface WorkspaceRepo {
  name: string
  path: string
  default_branch: string
  remote: string
  groups: string[]
}

/** Agent entry within a workspace. */
export interface WorkspaceAgent {
  name: string
  repos: string[]
  repo_groups: string[]
  cross_repo: boolean
}

/** Full workspace response from GET /api/workspace. */
export interface WorkspaceResponse {
  success: boolean
  data?: {
    name: string
    path: string
    repos: WorkspaceRepo[]
    groups: string[]
    agents: WorkspaceAgent[]
    workspaces: WorkspaceSummary[]
    workspace_order?: string[]
    default_workspace: string
  }
  error?: string
}

// =============================================================================
// Workspace API Helpers
// =============================================================================

/**
 * Create a test workspace via the API.
 * POST /api/workspace/create
 */
export async function createTestWorkspace(
  name: string,
  options?: { type?: string; repos?: string[] },
): Promise<Response> {
  return fetch(`${BASE_URL}/api/workspace/create`, {
    method: 'POST',
    headers: authHeaders({ 'Content-Type': 'application/json' }),
    body: JSON.stringify({
      name,
      type: options?.type ?? 'empty',
      repos: options?.repos ?? [],
    }),
  })
}

/**
 * Delete a test workspace via the API. Swallows 404 errors for cleanup safety.
 * DELETE /api/workspace/{name}
 */
export async function deleteTestWorkspace(name: string): Promise<void> {
  try {
    const response = await fetch(`${BASE_URL}/api/workspace/${encodeURIComponent(name)}`, {
      method: 'DELETE',
      headers: authHeaders(),
    })
    if (!response.ok && response.status !== 404) {
      console.warn(`Failed to delete workspace ${name}: ${response.status}`)
    }
  } catch {
    // Ignore network errors during cleanup
  }
}

/**
 * Get workspace info. Optionally override the Workspace header.
 * GET /api/workspace
 */
export async function getWorkspace(
  workspaceHeader?: string,
): Promise<Response> {
  const hdrs = authHeaders({ 'Content-Type': 'application/json' })
  if (workspaceHeader) {
    hdrs['Workspace'] = workspaceHeader
  }
  return fetch(`${BASE_URL}/api/workspace`, { headers: hdrs })
}

/**
 * Set the default workspace.
 * PUT /api/workspace/default
 */
export async function setDefaultWorkspace(name: string): Promise<Response> {
  return fetch(`${BASE_URL}/api/workspace/default`, {
    method: 'PUT',
    headers: authHeaders({ 'Content-Type': 'application/json' }),
    body: JSON.stringify({ name }),
  })
}

/**
 * Clear the default workspace.
 * DELETE /api/workspace/default
 */
export async function clearDefaultWorkspace(): Promise<Response> {
  return fetch(`${BASE_URL}/api/workspace/default`, {
    method: 'DELETE',
    headers: authHeaders(),
  })
}

/**
 * Rename a workspace.
 * PATCH /api/workspace/rename
 */
export async function renameWorkspace(
  oldName: string,
  newName: string,
): Promise<Response> {
  return fetch(`${BASE_URL}/api/workspace/rename`, {
    method: 'PATCH',
    headers: authHeaders({ 'Content-Type': 'application/json' }),
    body: JSON.stringify({ old_name: oldName, new_name: newName }),
  })
}

/**
 * Reorder workspaces.
 * PUT /api/workspace/order
 */
export async function reorderWorkspaces(order: string[]): Promise<Response> {
  return fetch(`${BASE_URL}/api/workspace/order`, {
    method: 'PUT',
    headers: authHeaders({ 'Content-Type': 'application/json' }),
    body: JSON.stringify({ order }),
  })
}

/**
 * Update workspace backend config.
 * PATCH /api/workspace/{name}/config/backend
 */
export async function updateWorkspaceBackend(
  name: string,
  backend: string,
): Promise<Response> {
  return fetch(`${BASE_URL}/api/workspace/${encodeURIComponent(name)}/config/backend`, {
    method: 'PATCH',
    headers: authHeaders({ 'Content-Type': 'application/json' }),
    body: JSON.stringify({ backend }),
  })
}
