import { FullConfig } from '@playwright/test'
import { exec as execCallback } from 'child_process'
import { promisify } from 'util'
import * as fs from 'fs/promises'
import * as path from 'path'

const exec = promisify(execCallback)

// Path to loomcli repository root (where compose.e2e.yml lives)
const COMPOSE_DIR = path.resolve(__dirname, '../../../../..')
const STATE_FILE = path.join(__dirname, '.e2e-state.json')
const HEALTH_TIMEOUT = 120_000  // 2 minutes for container builds
const POLL_INTERVAL = 2_000     // 2 seconds between checks

async function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms))
}

async function waitForHealth(url: string): Promise<void> {
  const startTime = Date.now()
  let lastError: Error | null = null

  while (Date.now() - startTime < HEALTH_TIMEOUT) {
    try {
      const response = await fetch(url)
      if (response.ok) {
        console.log(`Health check passed: ${url}`)
        return
      }
      lastError = new Error(`HTTP ${response.status}`)
    } catch (err) {
      lastError = err as Error
    }
    await sleep(POLL_INTERVAL)
  }

  throw new Error(`${url} did not become healthy: ${lastError?.message}`)
}

async function globalSetup(config: FullConfig): Promise<void> {
  // Skip if integration tests are not enabled
  if (!process.env.RUN_INTEGRATION_TESTS) {
    console.log('Skipping integration setup (RUN_INTEGRATION_TESTS not set)')
    return
  }

  const localServer = !!process.env.LOOM_LOCAL_SERVER
  const baseURL = process.env.LOOM_BASE_URL || 'http://localhost:8080'

  if (localServer) {
    // Local mode: just health-check the running loom serve instance
    console.log(`Checking local server at ${baseURL}...`)
    await waitForHealth(`${baseURL}/health`)

    await fs.writeFile(STATE_FILE, JSON.stringify({
      startedAt: new Date().toISOString(),
      webUrl: baseURL,
      loomUrl: baseURL,
      composeDir: COMPOSE_DIR,
      localMode: true,
    }, null, 2))

    console.log('Local server is healthy - E2E environment ready!')
    return
  }

  // webServer mode (Playwright manages server lifecycle) — no Podman needed.
  // Just write state file so teardown doesn't error.
  if (!process.env.PODMAN_COMPOSE) {
    console.log('webServer mode — Playwright manages server lifecycle')
    await fs.writeFile(STATE_FILE, JSON.stringify({
      startedAt: new Date().toISOString(),
      webUrl: baseURL,
      loomUrl: baseURL,
      composeDir: COMPOSE_DIR,
      localMode: true,
    }, null, 2))
    return
  }

  // Podman Compose mode (container-based integration tests)
  console.log('Starting E2E integration environment...')

  console.log('Starting Podman Compose stack...')
  try {
    await exec('podman-compose -f compose.e2e.yml up -d --build', {
      cwd: COMPOSE_DIR,
    })
  } catch (err) {
    console.error('Failed to start Podman Compose:', err)
    throw err
  }

  // Wait for services to become healthy
  console.log('Waiting for services to become healthy...')

  await Promise.all([
    waitForHealth('http://localhost:8081/health'),
    waitForHealth('http://localhost:9000/health'),
  ])

  // Write state file for teardown and tests
  await fs.writeFile(STATE_FILE, JSON.stringify({
    startedAt: new Date().toISOString(),
    webUrl: 'http://localhost:8081',
    loomUrl: 'http://localhost:9000',
    composeDir: COMPOSE_DIR,
    localMode: false,
  }, null, 2))

  console.log('E2E environment ready!')
}

export default globalSetup
