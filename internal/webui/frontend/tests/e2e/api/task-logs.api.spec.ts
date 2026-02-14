/**
 * Task Logs API E2E Tests
 *
 * Endpoints Under Test:
 * - GET /api/tasks/{id}/logs - List available phases
 * - GET /api/tasks/{id}/logs/{phase} - Snapshot log content
 */

import { test, expect, isIntegrationEnabled, generateTestId } from './api-client'

// Skip if integration tests not enabled
test.skip(!isIntegrationEnabled, 'API E2E tests require RUN_INTEGRATION_TESTS=1')

const BASE_URL = 'http://localhost:8081'

interface TaskPhasesResponse {
  success: boolean
  data?: {
    phases: string[]
  }
  error?: string
}

interface LogContentResponse {
  success: boolean
  data?: {
    lines: string[]
    line_count: number
  }
  error?: string
}

test.describe('Task Logs API', () => {
  const testTaskId = 'bd-test-task-logs'
  const invalidTaskId = '../../../etc/passwd'

  test.describe('Phase Listing', () => {
    test('GET /api/tasks/:id/logs lists available phases', async ({ request }) => {
      const response = await request.get(`${BASE_URL}/api/tasks/${testTaskId}/logs`)

      expect(response.ok()).toBe(true)

      const body = (await response.json()) as TaskPhasesResponse
      expect(body.success).toBe(true)
      expect(Array.isArray(body.data?.phases)).toBe(true)

      if (body.data?.phases && body.data.phases.length > 0) {
        for (const phase of body.data.phases) {
          expect(['planning', 'implementation']).toContain(phase)
        }
      }
    })

    test('returns empty phases array for task with no logs', async ({ request }) => {
      const noLogsTaskId = `no-logs-${generateTestId()}`
      const response = await request.get(`${BASE_URL}/api/tasks/${noLogsTaskId}/logs`)

      expect(response.ok()).toBe(true)

      const body = (await response.json()) as TaskPhasesResponse
      expect(body.success).toBe(true)
      expect(body.data?.phases).toEqual([])
    })
  })

  test.describe('Snapshot Logs', () => {
    test('GET /api/tasks/:id/logs/:phase returns log snapshot', async ({ request }) => {
      const response = await request.get(`${BASE_URL}/api/tasks/${testTaskId}/logs/planning`)

      if (response.ok()) {
        const body = (await response.json()) as LogContentResponse
        expect(body.success).toBe(true)
        expect(Array.isArray(body.data?.lines)).toBe(true)
        expect(typeof body.data?.line_count).toBe('number')
      } else {
        expect(response.status()).toBe(404)
        const body = (await response.json()) as LogContentResponse
        expect(body.success).toBe(false)
        expect(body.error).toContain('log file not found')
      }
    })

    test('phase endpoint supports ?lines=N parameter', async ({ request }) => {
      const response = await request.get(`${BASE_URL}/api/tasks/${testTaskId}/logs/implementation?lines=50`)

      if (response.ok()) {
        const body = (await response.json()) as LogContentResponse
        expect(body.success).toBe(true)
        expect(body.data?.lines.length).toBeLessThanOrEqual(50)
      } else {
        expect(response.status()).toBe(404)
      }
    })

    test('invalid phase name returns 400', async ({ request }) => {
      const response = await request.get(`${BASE_URL}/api/tasks/${testTaskId}/logs/invalid-phase`)

      expect(response.status()).toBe(400)
      const body = (await response.json()) as LogContentResponse
      expect(body.success).toBe(false)
      expect(body.error).toContain('invalid phase')
    })
  })

  test.describe('Input Validation', () => {
    test('invalid task ID returns 400', async ({ request }) => {
      const response = await request.get(
        `${BASE_URL}/api/tasks/${encodeURIComponent(invalidTaskId)}/logs`
      )

      expect(response.status()).toBe(400)
      const body = (await response.json()) as TaskPhasesResponse
      expect(body.success).toBe(false)
      expect(body.error).toContain('invalid task ID')
    })

    test('invalid task ID on phase endpoint returns 400', async ({ request }) => {
      const response = await request.get(
        `${BASE_URL}/api/tasks/${encodeURIComponent(invalidTaskId)}/logs/planning`
      )

      expect(response.status()).toBe(400)
      const body = (await response.json()) as LogContentResponse
      expect(body.success).toBe(false)
      expect(body.error).toContain('invalid task ID')
    })
  })
})
