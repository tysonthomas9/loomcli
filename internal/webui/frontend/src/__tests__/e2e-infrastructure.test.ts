/**
 * Unit tests for E2E test infrastructure utilities.
 *
 * Tests the pure utility functions exported from the E2E API client
 * (generateTestId, waitFor, isIntegrationEnabled) without requiring
 * a running Playwright context or Podman Compose stack.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

// Mock @playwright/test so importing api-client.ts does not fail in vitest.
// The LoomApiClient class itself depends on Playwright's APIRequestContext,
// but the utility functions we test here are pure and independent.
vi.mock('@playwright/test', () => ({
  test: {
    extend: vi.fn(() => ({})),
  },
  expect: {},
  APIRequestContext: class {},
}))

// Import after mock is set up
import {
  generateTestId,
  waitFor,
  isIntegrationEnabled,
  LoomApiClient,
} from '../../tests/e2e/api/api-client'

// Also verify barrel export works
import * as apiBarrel from '../../tests/e2e/api/index'

describe('E2E Infrastructure: generateTestId', () => {
  it('returns a string starting with "test-"', () => {
    const id = generateTestId()
    expect(id).toMatch(/^test-/)
  })

  it('contains a numeric timestamp segment', () => {
    const id = generateTestId()
    const parts = id.split('-')
    // parts[0] = "test", parts[1] = timestamp, parts[2] = random
    expect(parts.length).toBe(3)
    expect(Number(parts[1])).not.toBeNaN()
    expect(Number(parts[1])).toBeGreaterThan(0)
  })

  it('contains a random alphanumeric suffix', () => {
    const id = generateTestId()
    const parts = id.split('-')
    const randomPart = parts[2]!
    // Should be base-36 characters (a-z, 0-9), between 1-9 chars from substring(2, 11)
    expect(randomPart).toMatch(/^[a-z0-9]+$/)
    expect(randomPart.length).toBeGreaterThan(0)
    expect(randomPart.length).toBeLessThanOrEqual(9)
  })

  it('generates unique IDs on successive calls', () => {
    const ids = new Set<string>()
    for (let i = 0; i < 100; i++) {
      ids.add(generateTestId())
    }
    // All 100 should be unique (extremely high probability)
    expect(ids.size).toBe(100)
  })

  it('uses a recent timestamp', () => {
    const before = Date.now()
    const id = generateTestId()
    const after = Date.now()
    const timestamp = Number(id.split('-')[1])
    expect(timestamp).toBeGreaterThanOrEqual(before)
    expect(timestamp).toBeLessThanOrEqual(after)
  })
})

describe('E2E Infrastructure: waitFor', () => {
  // These tests use real timers with very short intervals to avoid
  // unhandled rejection issues that arise with fake timers and async loops.

  it('resolves immediately when predicate is true on first call', async () => {
    const fn = vi.fn().mockResolvedValue(42)
    const predicate = (v: number) => v === 42

    const result = await waitFor(fn, predicate, { timeout: 1000, interval: 10 })
    expect(result).toBe(42)
    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('polls until predicate becomes true', async () => {
    let callCount = 0
    const fn = vi.fn().mockImplementation(async () => {
      callCount++
      return callCount
    })
    const predicate = (v: number) => v >= 3

    const result = await waitFor(fn, predicate, { timeout: 2000, interval: 10 })
    expect(result).toBe(3)
    expect(fn).toHaveBeenCalledTimes(3)
  })

  it('throws timeout error when predicate never becomes true', async () => {
    const fn = vi.fn().mockResolvedValue('nope')
    const predicate = () => false

    await expect(
      waitFor(fn, predicate, { timeout: 50, interval: 10 })
    ).rejects.toThrow('Timeout waiting for condition after 50ms')
  })

  it('includes timeout duration in error message', async () => {
    const fn = vi.fn().mockResolvedValue(null)
    const predicate = () => false

    await expect(
      waitFor(fn, predicate, { timeout: 75, interval: 10 })
    ).rejects.toThrow('75ms')
  })

  it('calls fn multiple times while polling', async () => {
    const fn = vi.fn().mockResolvedValue('not-ready')
    const predicate = () => false

    await expect(
      waitFor(fn, predicate, { timeout: 100, interval: 15 })
    ).rejects.toThrow('Timeout')

    // With 15ms interval over ~100ms, we expect several calls
    expect(fn.mock.calls.length).toBeGreaterThanOrEqual(3)
  })

  it('returns the value from fn when predicate matches', async () => {
    const data = { status: 'ready', count: 5 }
    const fn = vi.fn().mockResolvedValue(data)
    const predicate = (result: typeof data) => result.status === 'ready'

    const result = await waitFor(fn, predicate, { timeout: 1000, interval: 10 })
    expect(result).toEqual(data)
    expect(result.count).toBe(5)
  })

  it('resolves with real async functions', async () => {
    const fn = async () => 'done'
    const predicate = (v: string) => v === 'done'

    const result = await waitFor(fn, predicate, { timeout: 1000, interval: 10 })
    expect(result).toBe('done')
  })

  it('propagates errors thrown by fn', async () => {
    const fn = vi.fn().mockRejectedValue(new Error('network error'))
    const predicate = () => true

    // waitFor does not catch fn errors - they propagate
    await expect(
      waitFor(fn, predicate, { timeout: 500, interval: 10 })
    ).rejects.toThrow('network error')
  })

  it('handles predicate that transitions from false to true', async () => {
    let ready = false
    // After a short delay, flip the ready flag
    setTimeout(() => { ready = true }, 30)

    const fn = vi.fn().mockImplementation(async () => ({ ready }))
    const predicate = (result: { ready: boolean }) => result.ready

    const result = await waitFor(fn, predicate, { timeout: 1000, interval: 10 })
    expect(result.ready).toBe(true)
    // fn should have been called more than once (some false, then true)
    expect(fn.mock.calls.length).toBeGreaterThanOrEqual(2)
  })
})

describe('E2E Infrastructure: isIntegrationEnabled', () => {
  it('is a boolean value', () => {
    expect(typeof isIntegrationEnabled).toBe('boolean')
  })

  it('reflects the RUN_INTEGRATION_TESTS env var at import time', () => {
    // When running vitest normally, RUN_INTEGRATION_TESTS is not set,
    // so isIntegrationEnabled should be false.
    // This test verifies the behavior matches the current env.
    const expected = !!process.env.RUN_INTEGRATION_TESTS
    expect(isIntegrationEnabled).toBe(expected)
  })
})

describe('E2E Infrastructure: LoomApiClient class', () => {
  it('is exported and is a constructor function', () => {
    expect(LoomApiClient).toBeDefined()
    expect(typeof LoomApiClient).toBe('function')
  })

  it('can be instantiated with a mock request context', () => {
    // Provide a minimal mock that satisfies the constructor signature
    const mockRequest = {} as InstanceType<typeof Object>
    const client = new LoomApiClient(mockRequest as never)
    expect(client).toBeInstanceOf(LoomApiClient)
  })

  it('accepts an optional baseURL parameter', () => {
    const mockRequest = {} as never
    const client = new LoomApiClient(mockRequest, 'http://custom:9999')
    expect(client).toBeInstanceOf(LoomApiClient)
  })
})

describe('E2E Infrastructure: barrel export (api/index.ts)', () => {
  it('re-exports generateTestId', () => {
    expect(apiBarrel.generateTestId).toBe(generateTestId)
  })

  it('re-exports waitFor', () => {
    expect(apiBarrel.waitFor).toBe(waitFor)
  })

  it('re-exports isIntegrationEnabled', () => {
    expect(apiBarrel.isIntegrationEnabled).toBe(isIntegrationEnabled)
  })

  it('re-exports LoomApiClient', () => {
    expect(apiBarrel.LoomApiClient).toBe(LoomApiClient)
  })

  it('exports type-related interfaces (smoke test via runtime check)', () => {
    // Types are compile-time only, but we can verify the module
    // exports the expected set of runtime values
    const exportedKeys = Object.keys(apiBarrel)
    expect(exportedKeys).toContain('generateTestId')
    expect(exportedKeys).toContain('waitFor')
    expect(exportedKeys).toContain('isIntegrationEnabled')
    expect(exportedKeys).toContain('LoomApiClient')
  })
})
