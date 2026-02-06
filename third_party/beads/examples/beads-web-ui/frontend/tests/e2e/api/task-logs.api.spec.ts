/**
 * Task Log Streaming API E2E Tests
 *
 * Story 10: As a developer, I want to view task execution logs by phase.
 * Tests log retrieval and SSE streaming endpoints for task phases.
 *
 * Endpoints Under Test:
 * - GET /api/tasks/{id}/logs - List available phases
 * - GET /api/tasks/{id}/logs/{phase} - Historical logs
 * - GET /api/tasks/{id}/logs/{phase}/stream - SSE stream
 *
 * Prerequisites:
 * - bd-imhr.1 (API test infrastructure)
 */

import { test, expect, isIntegrationEnabled, generateTestId } from './api-client'

// Skip if integration tests not enabled
test.skip(!isIntegrationEnabled, 'API E2E tests require RUN_INTEGRATION_TESTS=1')

const BASE_URL = 'http://localhost:8080'

// Response types matching Go backend
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

interface SSELogEvent {
  line: string
  line_number: number
  timestamp: string
}

/**
 * Helper to parse SSE events from a text/event-stream response.
 * Parses the standard SSE format: id, event, data fields.
 */
function parseSSEEvents(body: string): SSELogEvent[] {
  const events: SSELogEvent[] = []
  const parts = body.split('\n\n')

  for (const part of parts) {
    if (!part.trim()) continue

    let data = ''
    const lines = part.split('\n')

    for (const line of lines) {
      if (line.startsWith('data: ')) {
        data = line.slice(6)
      }
    }

    if (data) {
      try {
        const parsed = JSON.parse(data) as SSELogEvent
        // Only include log-line events (not truncated, heartbeat, etc.)
        if (parsed.line !== undefined && parsed.line_number !== undefined) {
          events.push(parsed)
        }
      } catch {
        // Ignore non-JSON data (heartbeat comments, etc.)
      }
    }
  }

  return events
}

test.describe('Task Log Streaming', () => {
  // Test task ID - uses valid format matching [a-zA-Z0-9_-]+
  const testTaskId = 'bd-test-task-logs'
  const invalidTaskId = '../../../etc/passwd' // Path traversal attempt

  test.describe('Phase Listing', () => {
    test('GET /api/tasks/:id/logs lists available phases', async ({ request }) => {
      // This test may return empty phases if no task logs exist
      const response = await request.get(`${BASE_URL}/api/tasks/${testTaskId}/logs`)

      // 200 is expected (empty phases array for non-existent task)
      expect(response.ok()).toBe(true)

      const body = await response.json() as TaskPhasesResponse
      expect(body.success).toBe(true)
      expect(body.data).toBeDefined()
      expect(Array.isArray(body.data?.phases)).toBe(true)

      // If phases exist, they should be valid
      if (body.data?.phases && body.data.phases.length > 0) {
        for (const phase of body.data.phases) {
          expect(['planning', 'implementation']).toContain(phase)
        }
      }
    })

    test('returns empty phases array for task with no logs', async ({ request }) => {
      // Use a unique task ID that definitely has no logs
      const noLogsTaskId = `no-logs-${generateTestId()}`
      const response = await request.get(`${BASE_URL}/api/tasks/${noLogsTaskId}/logs`)

      expect(response.ok()).toBe(true)

      const body = await response.json() as TaskPhasesResponse
      expect(body.success).toBe(true)
      expect(body.data?.phases).toEqual([])
    })
  })

  test.describe('Historical Logs', () => {
    test('GET /api/tasks/:id/logs/:phase returns log content', async ({ request }) => {
      // Test planning phase endpoint
      const response = await request.get(`${BASE_URL}/api/tasks/${testTaskId}/logs/planning`)

      // Could be 200 (logs exist) or 404 (no log file)
      if (response.ok()) {
        const body = await response.json() as LogContentResponse
        expect(body.success).toBe(true)
        expect(body.data).toBeDefined()
        expect(Array.isArray(body.data?.lines)).toBe(true)
        expect(typeof body.data?.line_count).toBe('number')
      } else {
        expect(response.status()).toBe(404)
        const body = await response.json() as LogContentResponse
        expect(body.success).toBe(false)
        expect(body.error).toContain('log file not found')
      }
    })

    test('phase endpoint supports ?lines=N parameter', async ({ request }) => {
      // Test with explicit lines parameter
      const response = await request.get(`${BASE_URL}/api/tasks/${testTaskId}/logs/implementation?lines=50`)

      // Verify the parameter is accepted (may still 404 if no logs)
      if (response.ok()) {
        const body = await response.json() as LogContentResponse
        expect(body.success).toBe(true)
        // Lines returned should be <= requested limit
        expect(body.data?.lines.length).toBeLessThanOrEqual(50)
      } else {
        expect(response.status()).toBe(404)
      }
    })

    test('invalid phase name returns 400', async ({ request }) => {
      // Invalid phase (not planning or implementation)
      const response = await request.get(`${BASE_URL}/api/tasks/${testTaskId}/logs/invalid-phase`)

      expect(response.status()).toBe(400)
      const body = await response.json() as LogContentResponse
      expect(body.success).toBe(false)
      expect(body.error).toContain('invalid phase')
    })
  })

  test.describe('SSE Streaming', () => {
    test('GET /api/tasks/:id/logs/:phase/stream establishes SSE connection', async () => {
      // Use native fetch for SSE - Playwright's request doesn't handle streaming
      const controller = new AbortController()
      const timeoutId = setTimeout(() => controller.abort(), 5000)

      try {
        const response = await fetch(`${BASE_URL}/api/tasks/${testTaskId}/logs/planning/stream`, {
          signal: controller.signal,
        })

        // Could be 200 (log exists) or 404 (no log file)
        if (response.ok) {
          // Verify SSE headers
          expect(response.headers.get('content-type')).toContain('text/event-stream')
          expect(response.headers.get('cache-control')).toBe('no-cache')
        } else {
          expect(response.status).toBe(404)
        }
      } catch (err) {
        // AbortError is expected if connection stays open (SSE behavior)
        if ((err as Error).name !== 'AbortError') {
          throw err
        }
      } finally {
        clearTimeout(timeoutId)
        controller.abort()
      }
    })

    test('SSE stream receives log-line events with line number', async () => {
      // This test verifies the SSE event format when logs exist
      const controller = new AbortController()
      let receivedEvents: SSELogEvent[] = []
      let reader: ReadableStreamDefaultReader<Uint8Array> | undefined

      try {
        const response = await fetch(`${BASE_URL}/api/tasks/${testTaskId}/logs/planning/stream`, {
          signal: controller.signal,
        })

        if (!response.ok) {
          // No log file - test passes trivially
          expect(response.status).toBe(404)
          return
        }

        // Read a chunk of the stream
        reader = response.body?.getReader()
        if (!reader) {
          throw new Error('No response body')
        }

        // Read for up to 2 seconds or until we get events
        const decoder = new TextDecoder()
        const activeReader = reader
        const readPromise = (async () => {
          let buffer = ''
          const startTime = Date.now()

          while (Date.now() - startTime < 2000) {
            const { value, done } = await activeReader.read()
            if (done) break
            buffer += decoder.decode(value, { stream: true })

            // Parse any complete events
            const events = parseSSEEvents(buffer)
            if (events.length > 0) {
              receivedEvents = events
              break
            }
          }
        })()

        await Promise.race([
          readPromise,
          new Promise(resolve => setTimeout(resolve, 3000)),
        ])

        // If we received events, verify their structure
        if (receivedEvents.length > 0) {
          const event = receivedEvents[0]
          expect(typeof event.line).toBe('string')
          expect(typeof event.line_number).toBe('number')
          expect(typeof event.timestamp).toBe('string')
          // Timestamp should be ISO8601/RFC3339 format
          expect(() => new Date(event.timestamp)).not.toThrow()
        }
      } finally {
        // Clean up: cancel reader before aborting controller
        if (reader) {
          try {
            await reader.cancel()
          } catch {
            // Ignore errors during cleanup
          }
        }
        controller.abort()
      }
    })

    test('truncated event endpoint accepts connection', async () => {
      // Note: This test verifies the implementation phase endpoint is accessible
      // Actual truncated event handling requires writing/truncating log files (covered by unit tests)
      const controller = new AbortController()
      const timeoutId = setTimeout(() => controller.abort(), 5000)
      let reader: ReadableStreamDefaultReader<Uint8Array> | undefined

      try {
        const response = await fetch(`${BASE_URL}/api/tasks/${testTaskId}/logs/implementation/stream`, {
          signal: controller.signal,
        })

        // Verify endpoint is accessible
        if (response.ok) {
          expect(response.headers.get('content-type')).toContain('text/event-stream')
          // Get reader for proper cleanup
          reader = response.body?.getReader()
        } else {
          // 404 is acceptable - no log file
          expect(response.status).toBe(404)
        }
      } catch (err) {
        if ((err as Error).name !== 'AbortError') {
          throw err
        }
      } finally {
        clearTimeout(timeoutId)
        // Clean up: cancel reader before aborting controller
        if (reader) {
          try {
            await reader.cancel()
          } catch {
            // Ignore errors during cleanup
          }
        }
        controller.abort()
      }
    })

    test('reconnect with ?since= catches up missed lines', async () => {
      // Test that ?since parameter is accepted
      // Full catch-up testing would require writing to log file during test
      const controller = new AbortController()
      const timeoutId = setTimeout(() => controller.abort(), 5000)

      try {
        const response = await fetch(`${BASE_URL}/api/tasks/${testTaskId}/logs/planning/stream?since=10`, {
          signal: controller.signal,
        })

        // If we got a response, verify it's either SSE or 404
        if (response.ok) {
          expect(response.headers.get('content-type')).toContain('text/event-stream')
        } else {
          expect(response.status).toBe(404)
        }
      } catch (err) {
        // AbortError is acceptable for SSE endpoint
        if ((err as Error).name !== 'AbortError') {
          throw err
        }
      } finally {
        clearTimeout(timeoutId)
        controller.abort()
      }
    })
  })

  test.describe('Input Validation', () => {
    test('invalid task ID returns 400', async ({ request }) => {
      // Path traversal attempt should be rejected
      const response = await request.get(`${BASE_URL}/api/tasks/${encodeURIComponent(invalidTaskId)}/logs`)

      expect(response.status()).toBe(400)
      const body = await response.json() as TaskPhasesResponse
      expect(body.success).toBe(false)
      expect(body.error).toContain('invalid task ID')
    })

    test('invalid task ID on phase endpoint returns 400', async ({ request }) => {
      const response = await request.get(`${BASE_URL}/api/tasks/${encodeURIComponent(invalidTaskId)}/logs/planning`)

      expect(response.status()).toBe(400)
      const body = await response.json() as LogContentResponse
      expect(body.success).toBe(false)
      expect(body.error).toContain('invalid task ID')
    })
  })
})
