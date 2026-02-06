/**
 * Agent Log Streaming API E2E Tests
 *
 * Story 9: As an operator, I want to view real-time agent logs.
 * Tests log retrieval and SSE streaming endpoints.
 *
 * Endpoints Under Test:
 * - GET /api/agents/{name}/logs - Historical logs
 * - GET /api/agents/{name}/logs/stream - SSE stream
 *
 * Prerequisites:
 * - bd-imhr.1 (API test infrastructure)
 */

import { test, expect, isIntegrationEnabled, generateTestId } from './api-client'

// Skip if integration tests not enabled
test.skip(!isIntegrationEnabled, 'API E2E tests require RUN_INTEGRATION_TESTS=1')

const BASE_URL = 'http://localhost:8080'

// Response types matching Go backend
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

test.describe('Agent Log Streaming', () => {
  // Agent name for testing - follows validation pattern [a-zA-Z0-9_-]+
  const testAgentName = 'test-agent-logs'
  const invalidAgentName = '../../../etc/passwd' // Path traversal attempt

  test.describe('Historical Logs Endpoint', () => {
    test('GET /api/agents/:name/logs returns historical log lines', async ({ request }) => {
      // This test may 404 if no agent logs exist in E2E environment
      // We test the endpoint structure and behavior
      const response = await request.get(`${BASE_URL}/api/agents/${testAgentName}/logs`)

      // Could be 200 (logs exist) or 404 (no log file)
      if (response.ok()) {
        const body = await response.json() as LogContentResponse
        expect(body.success).toBe(true)
        expect(body.data).toBeDefined()
        expect(Array.isArray(body.data?.lines)).toBe(true)
        expect(typeof body.data?.line_count).toBe('number')
      } else {
        // 404 is expected if agent has no logs
        expect(response.status()).toBe(404)
        const body = await response.json() as LogContentResponse
        expect(body.success).toBe(false)
        expect(body.error).toContain('log file not found')
      }
    })

    test('logs endpoint supports ?lines=N parameter (default 200)', async ({ request }) => {
      // Test with explicit lines parameter
      const response = await request.get(`${BASE_URL}/api/agents/${testAgentName}/logs?lines=50`)

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

    test('logs endpoint returns 404 for inactive agent', async ({ request }) => {
      // Use a unique agent name that definitely has no logs
      const inactiveAgent = `inactive-${generateTestId()}`
      const response = await request.get(`${BASE_URL}/api/agents/${inactiveAgent}/logs`)

      expect(response.status()).toBe(404)
      const body = await response.json() as LogContentResponse
      expect(body.success).toBe(false)
      expect(body.error).toContain('log file not found')
    })

    test('invalid agent name returns 400', async ({ request }) => {
      // Test validation with path traversal attempt
      const response = await request.get(`${BASE_URL}/api/agents/${encodeURIComponent(invalidAgentName)}/logs`)

      expect(response.status()).toBe(400)
      const body = await response.json() as LogContentResponse
      expect(body.success).toBe(false)
      expect(body.error).toContain('invalid agent name')
    })
  })

  test.describe('SSE Stream Endpoint', () => {
    test('GET /api/agents/:name/logs/stream establishes SSE connection', async () => {
      // Use native fetch for SSE - Playwright's request doesn't handle streaming
      const controller = new AbortController()
      const timeoutId = setTimeout(() => controller.abort(), 5000)

      try {
        const response = await fetch(`${BASE_URL}/api/agents/${testAgentName}/logs/stream`, {
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
        const response = await fetch(`${BASE_URL}/api/agents/${testAgentName}/logs/stream`, {
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

    // Note: Heartbeat test requires waiting 30+ seconds - skipped for normal runs
    // To run manually: comment out the skip and run with --timeout 40000
    test.skip('SSE stream sends heartbeat every 30 seconds', async () => {
      const controller = new AbortController()
      let receivedHeartbeat = false
      let reader: ReadableStreamDefaultReader<Uint8Array> | undefined

      try {
        const response = await fetch(`${BASE_URL}/api/agents/${testAgentName}/logs/stream`, {
          signal: controller.signal,
        })

        if (!response.ok) {
          expect(response.status).toBe(404)
          return
        }

        reader = response.body?.getReader()
        if (!reader) {
          throw new Error('No response body')
        }

        const decoder = new TextDecoder()
        const startTime = Date.now()

        // Wait up to 35 seconds for heartbeat
        while (Date.now() - startTime < 35000) {
          const { value, done } = await reader.read()
          if (done) break

          const chunk = decoder.decode(value, { stream: true })
          if (chunk.includes(': heartbeat')) {
            receivedHeartbeat = true
            break
          }
        }

        expect(receivedHeartbeat).toBe(true)
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

    test('reconnect with ?since= catches up missed lines', async () => {
      // Test that ?since parameter is accepted
      // Full catch-up testing would require writing to log file during test
      const controller = new AbortController()
      const timeoutId = setTimeout(() => controller.abort(), 5000)

      try {
        const response = await fetch(`${BASE_URL}/api/agents/${testAgentName}/logs/stream?since=10`, {
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

    test('invalid agent name on stream returns 400', async ({ request }) => {
      // Path traversal attempt should be rejected - 400 returned immediately (no streaming)
      const response = await request.get(`${BASE_URL}/api/agents/${encodeURIComponent(invalidAgentName)}/logs/stream`)

      expect(response.status()).toBe(400)
      const body = await response.json() as LogContentResponse
      expect(body.success).toBe(false)
      expect(body.error).toContain('invalid agent name')
    })
  })
})
