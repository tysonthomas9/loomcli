/**
 * Daemon Config API E2E Tests
 *
 * Tests the /api/daemon/status endpoint which exposes daemon runtime configuration.
 * Verifies that the daemon correctly reports its auto-sync settings.
 */

import { test, expect, isIntegrationEnabled } from './api-client'

// Skip if integration tests not enabled
test.skip(!isIntegrationEnabled, 'API E2E tests require RUN_INTEGRATION_TESTS=1')

// Serial mode for consistency
test.describe.configure({ mode: 'serial' })

test.describe('Daemon Config API', () => {
  test('GET /api/daemon/status - returns daemon configuration', async ({ api }) => {
    const status = await api.daemonStatus()

    // Verify all configuration fields are present
    expect(status).toHaveProperty('auto_commit')
    expect(status).toHaveProperty('auto_push')
    expect(status).toHaveProperty('auto_pull')
    expect(status).toHaveProperty('local_mode')
    expect(status).toHaveProperty('sync_interval')
    expect(status).toHaveProperty('daemon_mode')

    // Verify types are correct
    expect(typeof status.auto_commit).toBe('boolean')
    expect(typeof status.auto_push).toBe('boolean')
    expect(typeof status.auto_pull).toBe('boolean')
    expect(typeof status.local_mode).toBe('boolean')
    expect(typeof status.sync_interval).toBe('string')
    expect(typeof status.daemon_mode).toBe('string')
  })

  test('daemon status includes version and paths', async ({ api }) => {
    const status = await api.daemonStatus()

    // Verify daemon metadata is present
    expect(typeof status.version).toBe('string')
    expect(status.version.length).toBeGreaterThan(0)
    expect(typeof status.workspace_path).toBe('string')
    expect(typeof status.database_path).toBe('string')
    expect(typeof status.socket_path).toBe('string')
  })

  test('daemon status includes process info', async ({ api }) => {
    const status = await api.daemonStatus()

    // Verify process info
    expect(typeof status.pid).toBe('number')
    expect(status.pid).toBeGreaterThan(0)
    expect(typeof status.uptime_seconds).toBe('number')
    expect(status.uptime_seconds).toBeGreaterThanOrEqual(0)
  })

  test('daemon reports sync_interval in expected format', async ({ api }) => {
    const status = await api.daemonStatus()

    // Sync interval should be a non-empty Go duration string (e.g., "5s", "30s", "1m30s")
    expect(typeof status.sync_interval).toBe('string')
    expect(status.sync_interval.length).toBeGreaterThan(0)
    // Verify it ends with a time unit (s, m, h, ns, us, ms)
    expect(status.sync_interval).toMatch(/[0-9](ns|us|µs|ms|s|m|h)$/)
  })

  test('daemon reports daemon_mode as events or poll', async ({ api }) => {
    const status = await api.daemonStatus()

    // Daemon mode should be one of the expected values
    expect(['events', 'poll']).toContain(status.daemon_mode)
  })

  test('daemon runs in local_mode in E2E container environment', async ({ api }) => {
    const status = await api.daemonStatus()

    // In the E2E container (no git repo configured), daemon runs in local mode
    // This is expected behavior - when sync-branch is not configured, local_mode is true
    expect(status.local_mode).toBe(true)
  })

  test('exclusive_lock_active is boolean', async ({ api }) => {
    const status = await api.daemonStatus()

    // Lock status should be reported
    expect(typeof status.exclusive_lock_active).toBe('boolean')

    // If lock is active, holder should be present
    if (status.exclusive_lock_active) {
      expect(typeof status.exclusive_lock_holder).toBe('string')
      expect(status.exclusive_lock_holder!.length).toBeGreaterThan(0)
    }
  })
})
