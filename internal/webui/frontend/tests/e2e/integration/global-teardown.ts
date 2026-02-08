import { FullConfig } from '@playwright/test'
import { exec as execCallback } from 'child_process'
import { promisify } from 'util'
import * as fs from 'fs/promises'
import * as path from 'path'

const exec = promisify(execCallback)

// Must match the path used in global-setup.ts (which is in parent directory)
const STATE_FILE = path.join(__dirname, '../.e2e-state.json')

interface E2EState {
  startedAt: string
  webUrl: string
  loomUrl: string
  composeDir: string
}

async function globalTeardown(config: FullConfig): Promise<void> {
  // Check if state file exists (indicates setup was run)
  let state: E2EState | null = null
  try {
    const content = await fs.readFile(STATE_FILE, 'utf-8')
    state = JSON.parse(content) as E2EState
  } catch (err) {
    // State file doesn't exist or is invalid - setup was likely skipped
    console.log('No E2E state file found - skipping teardown')
    return
  }

  console.log('Tearing down E2E integration environment...')

  // Stop Podman Compose stack and remove volumes
  try {
    console.log('Stopping Podman Compose stack...')
    await exec('podman-compose -f compose.e2e.yml down -v --remove-orphans', {
      cwd: state.composeDir,
    })
    console.log('✓ Containers stopped and volumes removed')
  } catch (err) {
    // Log but don't throw - we still want to clean up state file
    console.error('Warning: Failed to stop Podman Compose:', err)
  }

  // Always attempt to remove state file
  try {
    await fs.unlink(STATE_FILE)
    console.log('✓ State file cleaned up')
  } catch (err) {
    console.warn('Warning: Failed to delete state file:', err)
  }

  console.log('E2E teardown complete')
}

export default globalTeardown
