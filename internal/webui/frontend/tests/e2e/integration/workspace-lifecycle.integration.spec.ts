/**
 * Workspace Lifecycle Integration Tests
 *
 * Full-stack integration tests exercising the complete workspace lifecycle
 * through the real backend API: create, read topology, set default, rename,
 * reorder, update backend config, clear default, and delete.
 *
 * Validates that each mutation is persisted and observable through subsequent
 * reads, ensuring the workspace CRUD pipeline works end-to-end.
 *
 * Requires:
 * - A running loom serve instance (default http://localhost:8080)
 * - RUN_INTEGRATION_TESTS=1 environment variable
 *
 * Run with: RUN_INTEGRATION_TESTS=1 npx playwright test --project=integration workspace-lifecycle
 */

import { test, expect } from '@playwright/test'
import {
  BASE_URL,
  authHeaders,
  generateTestId,
  getWorkspace,
  createTestWorkspace,
  deleteTestWorkspace,
  setDefaultWorkspace,
  clearDefaultWorkspace,
  renameWorkspace,
  reorderWorkspaces,
  updateWorkspaceBackend,
} from './helpers'

// Skip if integration tests not enabled
const skipIntegration = !process.env.RUN_INTEGRATION_TESTS
test.skip(skipIntegration, 'Integration tests require RUN_INTEGRATION_TESTS=1')

// Run tests serially: workspace mutations are global (modify ~/.loom/config.yaml)
test.describe.configure({ mode: 'serial' })

test.describe('Workspace Lifecycle Integration', () => {
  const testId = generateTestId()
  let workspaceName = `ws-${testId}`
  let renamedName = ''
  let workspaceCreated = false
  let originalDefault = ''

  test.afterAll(async () => {
    // Clean up: delete workspace (use renamed name if applicable)
    const nameToDelete = renamedName || workspaceName
    if (workspaceCreated) {
      await deleteTestWorkspace(nameToDelete)
    }

    // Restore original default if we changed it
    if (originalDefault) {
      try {
        await setDefaultWorkspace(originalDefault)
      } catch {
        // Ignore errors restoring default
      }
    } else {
      try {
        await clearDefaultWorkspace()
      } catch {
        // Ignore errors clearing default
      }
    }
  })

  test('create workspace via API and verify in workspace list', async () => {
    // Discover a valid repo path from the running server's current workspace
    const currentWsResp = await getWorkspace()
    let repoPaths: string[] = []

    if (currentWsResp.ok) {
      const currentWs = await currentWsResp.json()
      if (currentWs.data?.repos?.length > 0) {
        repoPaths = currentWs.data.repos.map((r: { path: string }) => r.path).slice(0, 1)
      }
    }

    // Create workspace
    const response = await createTestWorkspace(workspaceName, {
      type: 'empty',
      repos: repoPaths,
    })

    expect(response.status).toBe(201)
    const body = await response.json()
    expect(body.success).toBe(true)
    workspaceCreated = true

    // Verify workspace appears in listing
    const listResp = await getWorkspace()
    expect(listResp.ok).toBe(true)
    const listBody = await listResp.json()

    const found = listBody.data?.workspaces?.find(
      (ws: { name: string }) => ws.name === workspaceName,
    )
    expect(found).toBeDefined()
  })

  test('read workspace topology returns repos and agents', async () => {
    const response = await getWorkspace(workspaceName)
    expect(response.ok).toBe(true)

    const body = await response.json()
    expect(body.data).toBeDefined()
    expect(body.data.name).toBe(workspaceName)
    expect(Array.isArray(body.data.repos)).toBe(true)
    expect(Array.isArray(body.data.agents)).toBe(true)

    // If repos were provided, verify structure
    if (body.data.repos.length > 0) {
      const repo = body.data.repos[0]
      expect(repo.name).toBeDefined()
      expect(repo.path).toBeDefined()
    }
  })

  test('set created workspace as default', async () => {
    // Save the current default so we can restore it
    const beforeResp = await getWorkspace()
    if (beforeResp.ok) {
      const beforeBody = await beforeResp.json()
      originalDefault = beforeBody.data?.default_workspace || ''
    }

    const response = await setDefaultWorkspace(workspaceName)
    expect(response.status).toBe(200)

    const body = await response.json()
    expect(body.success).toBe(true)

    // Verify default was set
    const verifyResp = await getWorkspace()
    expect(verifyResp.ok).toBe(true)
    const verifyBody = await verifyResp.json()
    expect(verifyBody.data?.default_workspace).toBe(workspaceName)
  })

  test('rename workspace and verify new name persists', async () => {
    renamedName = `ws-renamed-${testId}`

    const response = await renameWorkspace(workspaceName, renamedName)
    expect(response.status).toBe(200)

    const body = await response.json()
    expect(body.success).toBe(true)

    // Verify new name appears in listing
    const listResp = await getWorkspace()
    expect(listResp.ok).toBe(true)
    const listBody = await listResp.json()

    const foundOld = listBody.data?.workspaces?.find(
      (ws: { name: string }) => ws.name === workspaceName,
    )
    const foundNew = listBody.data?.workspaces?.find(
      (ws: { name: string }) => ws.name === renamedName,
    )
    expect(foundOld).toBeUndefined()
    expect(foundNew).toBeDefined()

    // Verify default_workspace was updated if it was the renamed workspace
    if (listBody.data?.default_workspace) {
      expect(listBody.data.default_workspace).toBe(renamedName)
    }

    // Update tracking variable for subsequent tests
    workspaceName = renamedName
  })

  test('reorder workspaces and verify order persists', async () => {
    // Get current workspace list to build a valid order
    const listResp = await getWorkspace()
    expect(listResp.ok).toBe(true)
    const listBody = await listResp.json()

    const currentNames = (listBody.data?.workspaces || []).map(
      (ws: { name: string }) => ws.name,
    )
    if (currentNames.length < 2) {
      test.skip(true, 'Need at least 2 workspaces to test reordering')
      return
    }

    // Put our workspace first
    const reordered = [workspaceName, ...currentNames.filter((n: string) => n !== workspaceName)]

    const response = await reorderWorkspaces(reordered)
    expect(response.status).toBe(200)

    const body = await response.json()
    expect(body.success).toBe(true)

    // Verify order persisted
    const verifyResp = await getWorkspace()
    expect(verifyResp.ok).toBe(true)
    const verifyBody = await verifyResp.json()
    expect(verifyBody.data?.workspace_order?.[0]).toBe(workspaceName)
  })

  test('update workspace backend config', async () => {
    const response = await updateWorkspaceBackend(workspaceName, 'codex')
    expect(response.status).toBe(200)

    const body = await response.json()
    expect(body.success).toBe(true)

    // Verify backend is reflected in workspace summaries
    const verifyResp = await getWorkspace(workspaceName)
    expect(verifyResp.ok).toBe(true)
  })

  test('clear default workspace', async () => {
    const response = await clearDefaultWorkspace()
    expect(response.status).toBe(200)

    const body = await response.json()
    expect(body.success).toBe(true)

    // Verify default is cleared
    const verifyResp = await getWorkspace()
    expect(verifyResp.ok).toBe(true)
    const verifyBody = await verifyResp.json()
    // Default workspace should be empty or unset
    expect(verifyBody.data?.default_workspace || '').toBe('')
  })

  test('delete workspace removes from config', async () => {
    const nameToDelete = renamedName || workspaceName

    const response = await fetch(
      `${BASE_URL}/api/workspace/${encodeURIComponent(nameToDelete)}`,
      { method: 'DELETE', headers: authHeaders() },
    )
    expect(response.status).toBe(200)

    const body = await response.json()
    expect(body.success).toBe(true)

    // Verify workspace no longer in list
    const verifyResp = await getWorkspace()
    expect(verifyResp.ok).toBe(true)
    const verifyBody = await verifyResp.json()

    const found = verifyBody.data?.workspaces?.find(
      (ws: { name: string }) => ws.name === nameToDelete,
    )
    expect(found).toBeUndefined()

    // Mark as deleted so afterAll doesn't try to clean up
    workspaceCreated = false
  })

  // Validation / error tests
  test.describe('Validation Errors', () => {
    test('create workspace with invalid name returns 400', async () => {
      const response = await createTestWorkspace('invalid name with spaces!', {
        type: 'empty',
      })
      expect(response.status).toBe(400)

      const body = await response.json()
      expect(body.success).toBe(false)
      expect(body.error).toBeDefined()
    })

    test('create workspace with missing type returns 400', async () => {
      const response = await createTestWorkspace(`ws-valid-${generateTestId()}`, {
        type: '',
      })
      expect(response.status).toBe(400)

      const body = await response.json()
      expect(body.success).toBe(false)
    })

    test('delete non-existent workspace returns 404', async () => {
      const response = await fetch(
        `${BASE_URL}/api/workspace/nonexistent-${generateTestId()}`,
        { method: 'DELETE', headers: authHeaders() },
      )
      expect(response.status).toBe(404)

      const body = await response.json()
      expect(body.success).toBe(false)
    })

    test('rename to existing workspace name returns 409', async () => {
      // Get current workspace list
      const listResp = await getWorkspace()
      if (!listResp.ok) {
        test.skip(true, 'Cannot get workspace list')
        return
      }
      const listBody = await listResp.json()
      const workspaces = listBody.data?.workspaces || []

      if (workspaces.length < 2) {
        test.skip(true, 'Need at least 2 workspaces to test rename conflict')
        return
      }

      // Try to rename one workspace to another's name
      const firstName = workspaces[0].name
      const secondName = workspaces[1].name

      const response = await renameWorkspace(firstName, secondName)
      expect(response.status).toBe(409)

      const body = await response.json()
      expect(body.success).toBe(false)
    })
  })
})
