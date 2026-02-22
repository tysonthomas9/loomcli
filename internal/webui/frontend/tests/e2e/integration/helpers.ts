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
