/**
 * API E2E Test Infrastructure
 *
 * Provides typed API client and Playwright fixtures for API-level testing
 * against the loomcli web UI backend.
 *
 * Usage:
 *   import { test, expect } from '../api/api-client'
 *
 *   test('create issue via API', async ({ api }) => {
 *     const issue = await api.createIssue({ title: 'Test', issue_type: 'task', priority: 2 })
 *     expect(issue.id).toBeDefined()
 *   })
 */

import { test as base, expect, APIRequestContext } from '@playwright/test'

// =============================================================================
// Type Definitions - Request/Response interfaces matching Go backend
// =============================================================================

/** Issue type enum matching backend */
export type IssueType = 'bug' | 'feature' | 'task' | 'epic' | 'chore'

/** Issue status enum matching backend */
export type IssueStatus = 'open' | 'in_progress' | 'blocked' | 'deferred' | 'review' | 'closed' | 'tombstone' | 'pinned' | 'hooked'

/** Issue priority (0-4, where 0 is P0/critical) */
export type Priority = 0 | 1 | 2 | 3 | 4

/** Base Issue interface matching Go types.Issue */
export interface Issue {
  id: string
  title: string
  description?: string
  design?: string
  acceptance_criteria?: string
  notes?: string
  status?: IssueStatus
  priority: number
  issue_type?: IssueType
  assignee?: string
  owner?: string
  created_by?: string
  created_at: string
  updated_at: string
  closed_at?: string
  close_reason?: string
  closed_by_session?: string
  due_at?: string
  defer_until?: string
  external_ref?: string
  labels?: string[]
  parent?: string
  parent_title?: string
  pinned?: boolean
  is_template?: boolean
}

/** Issue with dependency counts (list response) */
export interface IssueWithCounts extends Issue {
  dependency_count: number
  dependent_count: number
}

/** Issue with parent info (list response) */
export interface IssueWithParent extends IssueWithCounts {
  parent?: string
  parent_title?: string
}

/** Dependency relationship */
export interface Dependency {
  issue_id: string
  depends_on_id: string
  type: string
  created_at: string
  created_by?: string
}

/** Issue with dependency metadata */
export interface IssueWithDependencyMetadata extends Issue {
  dependency_type: string
}

/** Full issue details (show response) */
export interface IssueDetails extends Issue {
  labels: string[]
  dependencies: IssueWithDependencyMetadata[]
  dependents: IssueWithDependencyMetadata[]
  comments: Comment[]
  parent?: string
}

/** Comment on an issue */
export interface Comment {
  id: number
  issue_id: string
  author: string
  text: string
  created_at: string
}

/** Blocked issue with blocker info */
export interface BlockedIssue extends Issue {
  blocked_by_count: number
  blocked_by: string[]
  blocked_by_details?: BlockerRef[]
}

/** Blocker reference */
export interface BlockerRef {
  id: string
  title: string
  priority: number
}

/** Graph issue for visualization */
export interface GraphIssue {
  id: string
  title: string
  status: string
  priority: number
  issue_type: string
  labels?: string[]
  dependencies?: GraphDependency[]
  defer_until?: string
  due_at?: string
}

/** Graph dependency */
export interface GraphDependency {
  depends_on_id: string
  type: string
}

// =============================================================================
// Request Types
// =============================================================================

/** Create issue request */
export interface IssueCreateRequest {
  title: string
  issue_type: IssueType
  priority: Priority
  id?: string
  parent?: string
  description?: string
  design?: string
  acceptance_criteria?: string
  notes?: string
  assignee?: string
  owner?: string
  created_by?: string
  external_ref?: string
  estimated_minutes?: number
  labels?: string[]
  dependencies?: string[]
  due_at?: string
  defer_until?: string
}

/** Patch issue request (all fields optional) */
export interface IssuePatchRequest {
  title?: string
  description?: string
  status?: IssueStatus
  priority?: Priority
  assignee?: string
  design?: string
  acceptance_criteria?: string
  notes?: string
  external_ref?: string
  estimated_minutes?: number
  issue_type?: IssueType
  add_labels?: string[]
  remove_labels?: string[]
  set_labels?: string[]
  pinned?: boolean
  parent?: string
  due_at?: string
  defer_until?: string
}

/** Close issue request */
export interface CloseRequest {
  reason?: string
  session?: string
  suggest_next?: boolean
  force?: boolean
}

/** Add comment request */
export interface AddCommentRequest {
  text: string
}

/** Add dependency request */
export interface AddDependencyRequest {
  depends_on_id: string
  dep_type?: string
}

/** List issues query params */
export interface ListIssuesParams {
  status?: IssueStatus
  type?: IssueType
  assignee?: string
  q?: string
  priority?: Priority
  labels?: string
  limit?: number
  title_contains?: string
  description_contains?: string
  notes_contains?: string
  created_after?: string
  created_before?: string
  updated_after?: string
  updated_before?: string
  empty_description?: boolean
  no_assignee?: boolean
  no_labels?: boolean
  pinned?: boolean
}

/** Ready issues query params */
export interface ReadyParams {
  assignee?: string
  type?: IssueType
  parent_id?: string
  mol_type?: 'swarm' | 'patrol' | 'work'
  sort?: 'hybrid' | 'priority' | 'oldest'
  unassigned?: boolean
  include_deferred?: boolean
  priority?: Priority
  limit?: number
  labels?: string
  labels_any?: string
}

/** Blocked issues query params */
export interface BlockedParams {
  parent_id?: string
  assignee?: string
  type?: IssueType
  priority?: Priority
  limit?: number
}

/** Graph query params */
export interface GraphParams {
  status?: 'all' | 'open' | 'closed'
  include_closed?: boolean
}

// =============================================================================
// Response Types
// =============================================================================

/** Standard API response wrapper */
export interface ApiResponse<T> {
  success: boolean
  data?: T
  error?: string
  code?: string
}

/** Health check response */
export interface HealthResponse {
  status: string
}

/** API health response with daemon status */
export interface ApiHealthResponse {
  status: string
  daemon: {
    connected: boolean
    status?: string
    uptime?: number
    version?: string
    error?: string
  }
  pool?: {
    active: number
    idle: number
    total: number
  }
}

/** Statistics response */
export interface StatsResponse {
  success: boolean
  data?: Statistics
  error?: string
}

/** Project statistics */
export interface Statistics {
  total_issues: number
  open_issues: number
  in_progress_issues: number
  closed_issues: number
  blocked_issues: number
  deferred_issues: number
  ready_issues: number
  tombstone_issues: number
  pinned_issues: number
  epics_eligible_for_closure: number
  average_lead_time_hours: number
}

/** Metrics response */
export interface MetricsResponse {
  success: boolean
  data?: SSEMetrics
  error?: string
}

/** SSE hub metrics */
export interface SSEMetrics {
  connected_clients: number
  dropped_mutations: number
  retry_queue_depth: number
  uptime_seconds: number
}

/** Graph response */
export interface GraphResponse {
  success: boolean
  data?: GraphIssue[]
  error?: string
}

/** Daemon status data - runtime configuration from the daemon */
export interface DaemonStatusData {
  version: string
  workspace_path: string
  database_path: string
  socket_path: string
  pid: number
  uptime_seconds: number
  last_activity_time: string
  exclusive_lock_active: boolean
  exclusive_lock_holder?: string
  // Configuration fields - these are what we test
  auto_commit: boolean
  auto_push: boolean
  auto_pull: boolean
  local_mode: boolean
  sync_interval: string
  daemon_mode: string
}

/** Daemon status response */
export interface DaemonStatusResponse {
  success: boolean
  data?: DaemonStatusData
  error?: string
}

// =============================================================================
// API Client Implementation
// =============================================================================

/**
 * Typed API client for loomcli web UI backend.
 *
 * All methods throw on non-2xx responses with descriptive error messages
 * including HTTP status and response body.
 *
 * Issue CRUD, dependency, stats, ready, blocked, daemon status, and graph
 * methods route through workspace-scoped endpoints (/api/workspaces/{ws}/...).
 * The API E2E suite intentionally has no legacy flat /api fallback for these
 * contracts.
 */
export class LoomApiClient {
  /** URL prefix for workspace-scoped endpoints. */
  readonly wsPrefix: string

  constructor(
    private request: APIRequestContext,
    private baseURL: string = '',
    workspaceId: string
  ) {
    if (!workspaceId) {
      throw new Error('LoomApiClient requires a workspaceId for fleet API E2E tests')
    }
    this.wsPrefix = `${baseURL}/api/workspaces/${workspaceId}`
  }

  // ===========================================================================
  // Health & Monitoring
  // ===========================================================================

  /** GET /health - Basic health check */
  async health(): Promise<HealthResponse> {
    const response = await this.request.get(`${this.baseURL}/health`)
    return this.parseResponse<HealthResponse>(response)
  }

  /** GET /api/health - Detailed health with daemon status */
  async apiHealth(): Promise<ApiHealthResponse> {
    const response = await this.request.get(`${this.baseURL}/api/health`)
    return this.parseResponse<ApiHealthResponse>(response)
  }

  /** GET /api/workspaces/{ws}/stats - Project statistics */
  async stats(): Promise<Statistics> {
    const response = await this.request.get(`${this.wsPrefix}/stats`)
    const result = await this.parseResponse<StatsResponse>(response)
    if (!result.success || !result.data) {
      throw new Error(`Stats request failed: ${result.error}`)
    }
    return result.data
  }

  /** GET /api/metrics - SSE hub metrics */
  async metrics(): Promise<SSEMetrics> {
    const response = await this.request.get(`${this.baseURL}/api/metrics`)
    const result = await this.parseResponse<MetricsResponse>(response)
    if (!result.success || !result.data) {
      throw new Error(`Metrics request failed: ${result.error}`)
    }
    return result.data
  }

  /** GET /api/workspaces/{ws}/daemon/status - Daemon runtime configuration */
  async daemonStatus(): Promise<DaemonStatusData> {
    const response = await this.request.get(`${this.wsPrefix}/daemon/status`)
    const result = await this.parseResponse<DaemonStatusResponse>(response)
    if (!result.success || !result.data) {
      throw new Error(`Daemon status request failed: ${result.error}`)
    }
    return result.data
  }

  // ===========================================================================
  // Issue CRUD
  // ===========================================================================

  /** GET /api/workspaces/{ws}/issues - List issues with optional filters */
  async listIssues(params?: ListIssuesParams): Promise<IssueWithParent[]> {
    const queryParams = this.buildQueryParams(params)
    const url = `${this.wsPrefix}/issues${queryParams}`
    const response = await this.request.get(url)
    const result = await this.parseResponse<ApiResponse<IssueWithParent[]>>(response)
    if (!result.success) {
      throw new Error(`List issues failed: ${result.error}`)
    }
    return result.data || []
  }

  /** GET /api/workspaces/{ws}/issues/{id} - Get single issue with full details */
  async getIssue(id: string): Promise<IssueDetails> {
    const response = await this.request.get(`${this.wsPrefix}/issues/${id}`)
    const result = await this.parseResponse<ApiResponse<IssueDetails>>(response)
    if (!result.success || !result.data) {
      throw new Error(`Get issue failed: ${result.error}`)
    }
    return result.data
  }

  /** POST /api/workspaces/{ws}/issues - Create new issue */
  async createIssue(data: IssueCreateRequest): Promise<Issue> {
    return this.withRetry(async () => {
      const response = await this.request.post(`${this.wsPrefix}/issues`, { data })
      const result = await this.parseResponse<ApiResponse<Issue>>(response)
      if (!result.success || !result.data) {
        throw new Error(`Create issue failed: ${result.error}`)
      }
      return result.data
    })
  }

  /** PATCH /api/workspaces/{ws}/issues/{id} - Update issue (partial) */
  async updateIssue(id: string, data: IssuePatchRequest): Promise<{ id: string; status: string }> {
    return this.withRetry(async () => {
      const response = await this.request.patch(`${this.wsPrefix}/issues/${id}`, { data })
      const result = await this.parseResponse<ApiResponse<{ id: string; status: string }>>(response)
      if (!result.success || !result.data) {
        throw new Error(`Update issue failed: ${result.error}`)
      }
      return result.data
    })
  }

  /** POST /api/workspaces/{ws}/issues/{id}/close - Close an issue */
  async closeIssue(id: string, data?: CloseRequest): Promise<Issue> {
    return this.withRetry(async () => {
      const response = await this.request.post(`${this.wsPrefix}/issues/${id}/close`, {
        data: data || {},
      })
      const result = await this.parseResponse<ApiResponse<Issue>>(response)
      if (!result.success || !result.data) {
        throw new Error(`Close issue failed: ${result.error}`)
      }
      return result.data
    })
  }

  // ===========================================================================
  // Comments
  // ===========================================================================

  /** POST /api/workspaces/{ws}/issues/{id}/comments - Add comment to issue */
  async addComment(id: string, data: AddCommentRequest): Promise<Comment> {
    return this.withRetry(async () => {
      const response = await this.request.post(`${this.wsPrefix}/issues/${id}/comments`, { data })
      const result = await this.parseResponse<ApiResponse<Comment>>(response)
      if (!result.success || !result.data) {
        throw new Error(`Add comment failed: ${result.error}`)
      }
      return result.data
    })
  }

  // ===========================================================================
  // Dependencies
  // ===========================================================================

  /** POST /api/workspaces/{ws}/issues/{id}/dependencies - Add dependency */
  async addDependency(id: string, data: AddDependencyRequest): Promise<void> {
    return this.withRetry(async () => {
      const response = await this.request.post(`${this.wsPrefix}/issues/${id}/dependencies`, { data })
      const result = await this.parseResponse<ApiResponse<null>>(response)
      if (!result.success) {
        throw new Error(`Add dependency failed: ${result.error}`)
      }
    })
  }

  /** DELETE /api/workspaces/{ws}/issues/{id}/dependencies/{depId} - Remove dependency */
  async removeDependency(id: string, depId: string): Promise<void> {
    return this.withRetry(async () => {
      const response = await this.request.delete(`${this.wsPrefix}/issues/${id}/dependencies/${depId}`)
      const result = await this.parseResponse<ApiResponse<null>>(response)
      if (!result.success) {
        throw new Error(`Remove dependency failed: ${result.error}`)
      }
    })
  }

  // ===========================================================================
  // Work Queries
  // ===========================================================================

  /** GET /api/workspaces/{ws}/ready - Get issues ready to work on */
  async ready(params?: ReadyParams): Promise<Issue[]> {
    const queryParams = this.buildQueryParams(params)
    const url = `${this.wsPrefix}/ready${queryParams}`
    const response = await this.request.get(url)
    const result = await this.parseResponse<ApiResponse<Issue[]>>(response)
    if (!result.success) {
      throw new Error(`Ready query failed: ${result.error}`)
    }
    return result.data || []
  }

  /** GET /api/workspaces/{ws}/blocked - Get blocked issues */
  async blocked(params?: BlockedParams): Promise<BlockedIssue[]> {
    const queryParams = this.buildQueryParams(params)
    const url = `${this.wsPrefix}/blocked${queryParams}`
    const response = await this.request.get(url)
    const result = await this.parseResponse<ApiResponse<BlockedIssue[]>>(response)
    if (!result.success) {
      throw new Error(`Blocked query failed: ${result.error}`)
    }
    return result.data || []
  }

  /** GET /api/workspaces/{ws}/issues/graph - Get dependency graph data */
  async graph(params?: GraphParams): Promise<GraphIssue[]> {
    const queryParams = this.buildQueryParams(params)
    const url = `${this.wsPrefix}/issues/graph${queryParams}`
    const response = await this.request.get(url)
    const result = await this.parseResponse<GraphResponse>(response)
    if (!result.success) {
      throw new Error(`Graph query failed: ${result.error}`)
    }
    return result.data || []
  }

  // ===========================================================================
  // Cleanup Helpers
  // ===========================================================================

  /** DELETE /api/workspaces/{ws}/issues/{id} - Permanently delete an issue */
  async deleteIssue(id: string): Promise<void> {
    const response = await this.request.delete(`${this.wsPrefix}/issues/${id}`)
    await this.parseResponse(response)
  }

  /**
   * Delete an issue, ignoring 404 errors (for cleanup in afterEach).
   * Retries on 429 rate limit errors up to 3 times.
   */
  async cleanupIssue(id: string): Promise<void> {
    for (let attempt = 0; attempt < 3; attempt++) {
      try {
        await this.deleteIssue(id)
        return
      } catch (err) {
        const msg = String(err)
        if (msg.includes('404') || msg.includes('not found')) return
        if (msg.includes('429') && attempt < 2) {
          await new Promise(resolve => setTimeout(resolve, 1500))
          continue
        }
        if (attempt === 2) console.warn(`Cleanup warning for ${id}:`, err)
      }
    }
  }

  // ===========================================================================
  // Internal Helpers
  // ===========================================================================

  /** Parse response JSON with error handling */
  private async parseResponse<T>(response: Awaited<ReturnType<APIRequestContext['get']>>): Promise<T> {
    const status = response.status()
    const body = await response.text()

    if (status >= 400) {
      throw new Error(`HTTP ${status}: ${body}`)
    }

    try {
      return JSON.parse(body) as T
    } catch {
      throw new Error(`Failed to parse response: ${body}`)
    }
  }

  /**
   * Execute a request function with automatic retry on 429 rate limit.
   * Retries up to 3 times with exponential backoff.
   */
  private async withRetry<T>(fn: () => Promise<T>): Promise<T> {
    for (let attempt = 0; attempt < 3; attempt++) {
      try {
        return await fn()
      } catch (err) {
        if (String(err).includes('429') && attempt < 2) {
          const delayMs = 1500 * (attempt + 1)
          await new Promise(resolve => setTimeout(resolve, delayMs))
          continue
        }
        throw err
      }
    }
    throw new Error('Unreachable')
  }

  /** Build query string from params object */
  private buildQueryParams<T extends object>(params?: T): string {
    if (!params) return ''

    const searchParams = new URLSearchParams()
    for (const [key, value] of Object.entries(params)) {
      if (value !== undefined && value !== null) {
        searchParams.set(key, String(value))
      }
    }

    const query = searchParams.toString()
    return query ? `?${query}` : ''
  }
}

// =============================================================================
// Playwright Fixture
// =============================================================================

/** Extended test fixtures with API client */
interface ApiFixtures {
  api: LoomApiClient
}

/**
 * Extended test with api fixture.
 *
 * Usage:
 *   import { test, expect } from '../api/api-client'
 *
 *   test('my api test', async ({ api }) => {
 *     const health = await api.health()
 *     expect(health.status).toBe('ok')
 *   })
 */
export const test = base.extend<ApiFixtures>({
  api: async ({ request, baseURL }, use) => {
    const url = baseURL || process.env.LOOM_BASE_URL || 'http://localhost:8080'
    // Resolve workspace ID for workspace-scoped routes.
    // Prefer the active workspace to avoid picking up ephemeral test workspaces.
    let workspaceId: string | undefined
    try {
      const wsResponse = await request.get(`${url}/api/workspaces`)
      if (wsResponse.ok()) {
        const body = await wsResponse.json() as { workspaces?: Array<{ id: string; active?: boolean; name?: string }> }
        const workspaces = body?.workspaces ?? []
        const active = workspaces.find(w => w.active)
        workspaceId = active?.id ?? workspaces[0]?.id
      } else {
        throw new Error(`[api-client] /api/workspaces returned ${wsResponse.status()}`)
      }
    } catch (err) {
      throw new Error(`[api-client] failed to resolve workspace for fleet API E2E tests: ${String(err)}`)
    }
    if (!workspaceId) {
      throw new Error('[api-client] no workspace available for fleet API E2E tests')
    }
    const api = new LoomApiClient(request, url, workspaceId)
    await use(api)
  },
})

// Re-export expect for convenience
export { expect }

// =============================================================================
// Test Utilities
// =============================================================================

/**
 * Check if integration tests are enabled.
 * API tests require RUN_INTEGRATION_TESTS=1 to be set because they use
 * the same Podman Compose stack as integration tests.
 *
 * Usage in test files:
 *   test.skip(!isIntegrationEnabled, 'API tests require RUN_INTEGRATION_TESTS=1')
 */
export const isIntegrationEnabled = !!process.env.RUN_INTEGRATION_TESTS

/**
 * Resolved API base URL matching Playwright config logic.
 * Self-contained mode uses a dedicated port; local mode uses 8080 or LOOM_BASE_URL.
 */
const _isLocalServer = !!process.env.LOOM_LOCAL_SERVER
const _selfContainedPort = 8090
export const resolvedApiBaseURL =
  isIntegrationEnabled && !_isLocalServer
    ? `http://localhost:${_selfContainedPort}`
    : process.env.LOOM_BASE_URL || 'http://localhost:8080'

/**
 * Generate unique test ID for test isolation.
 * Format: test-<timestamp>-<random>
 */
export function generateTestId(): string {
  return `test-${Date.now()}-${Math.random().toString(36).substring(2, 11)}`
}

/**
 * Wait for a condition to be true, polling at intervals.
 * Useful for waiting on SSE updates to propagate.
 */
export async function waitFor<T>(
  fn: () => Promise<T>,
  predicate: (result: T) => boolean,
  options: { timeout?: number; interval?: number } = {}
): Promise<T> {
  const { timeout = 10000, interval = 500 } = options
  const start = Date.now()

  while (Date.now() - start < timeout) {
    const result = await fn()
    if (predicate(result)) {
      return result
    }
    await new Promise(resolve => setTimeout(resolve, interval))
  }

  throw new Error(`Timeout waiting for condition after ${timeout}ms`)
}
