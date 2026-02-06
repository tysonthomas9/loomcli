import { FullConfig } from '@playwright/test'
import { exec as execCallback } from 'child_process'
import { promisify } from 'util'
import * as fs from 'fs/promises'
import * as path from 'path'

const exec = promisify(execCallback)

// Path to beads-web-ui directory (where compose.e2e.yml lives)
const COMPOSE_DIR = path.resolve(__dirname, '../../..')
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
        console.log(`✓ ${url} is healthy`)
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

  console.log('Starting E2E integration environment...')

  // Start Podman Compose stack
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
    waitForHealth('http://localhost:8080/health'),
    waitForHealth('http://localhost:9000/health'),
  ])

  // Write state file for teardown and tests
  await fs.writeFile(STATE_FILE, JSON.stringify({
    startedAt: new Date().toISOString(),
    webUrl: 'http://localhost:8080',
    loomUrl: 'http://localhost:9000',
    composeDir: COMPOSE_DIR,
  }, null, 2))

  console.log('E2E environment ready!')
}

export default globalSetup
