import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for the WorkspaceBreadcrumb component.
 * Tests breadcrumb display showing workspace name + view label in multi-repo mode,
 * and the "Cortex" fallback in single-repo mode.
 */

const mockIssues = [
  {
    id: "bc-test-1",
    title: "Breadcrumb Test Issue",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
]

function createWorkspaceResponse(
  name: string,
  repos: Array<{ name: string; path: string }>
) {
  return {
    success: true,
    data: {
      name,
      path: "/mock/path/" + name,
      repos: repos.map((r) => ({
        name: r.name,
        path: r.path,
        default_branch: "main",
        remote: "origin",
        groups: [],
      })),
      groups: [],
      agents: [],
      workspaces: [
        {
          name,
          path: "/mock/path/" + name,
          active: true,
          repo_count: repos.length,
          is_default: true,
        },
      ],
      workspace_order: [name],
      default_workspace: name,
    },
  }
}

const multiRepoWorkspace = createWorkspaceResponse("my-project", [
  { name: "frontend", path: "/mock/path/frontend" },
  { name: "backend", path: "/mock/path/backend" },
])

const emptyRepoWorkspace = createWorkspaceResponse("empty-ws", [])

async function setupMocks(
  page: Page,
  options?: {
    workspace?: ReturnType<typeof createWorkspaceResponse>
    workspaceError?: boolean
  }
) {
  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({ status: 404 })
  })

  await page.route("**/api/events", async (route) => {
    await route.abort()
  })

  if (options?.workspaceError) {
    await page.route("**/api/workspace", async (route) => {
      await route.fulfill({ status: 500, body: "Internal Server Error" })
    })
  } else {
    const workspace = options?.workspace ?? multiRepoWorkspace
    await page.route("**/api/workspace", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(workspace),
      })
    })
  }

  await page.route("**/api/ready", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: mockIssues }),
    })
  })

  await page.route("**/api/stats", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: { open: 1, closed: 0, total: 1, completion: 0 },
      }),
    })
  })

  await page.route("**/api/issues/graph", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: mockIssues }),
    })
  })
}

async function navigateAndWait(page: Page, path = "/") {
  await page.goto(path)
  await page.locator("h1").waitFor({ state: "visible", timeout: 10000 })
}

test.describe("WorkspaceBreadcrumb", () => {
  test.describe("single-repo fallback", () => {
    test("shows Cortex when workspace has no repos", async ({ page }) => {
      await setupMocks(page, { workspace: emptyRepoWorkspace })
      await navigateAndWait(page)

      const heading = page.locator("h1")
      await expect(heading.getByText("Cortex")).toBeVisible()
    })

    test("shows Cortex when workspace API returns error", async ({ page }) => {
      await setupMocks(page, { workspaceError: true })
      await navigateAndWait(page)

      const heading = page.locator("h1")
      await expect(heading.getByText("Cortex")).toBeVisible()
    })
  })

  test.describe("multi-repo breadcrumb", () => {
    test("shows workspace name with separator", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const heading = page.locator("h1")
      await expect(heading.getByText("my-project")).toBeVisible({
        timeout: 10000,
      })
      await expect(
        heading.getByText("/", { exact: true })
      ).toBeVisible()
    })

    test("shows Kanban label on default view", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const heading = page.locator("h1")
      await expect(heading.getByText("Kanban")).toBeVisible({ timeout: 10000 })
    })

    test("shows List label on table view", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=table")

      const heading = page.locator("h1")
      await expect(heading.getByText("List")).toBeVisible({ timeout: 10000 })
    })

    test("shows Terminal label on terminal view", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=terminal")

      const heading = page.locator("h1")
      await expect(heading.getByText("Terminal")).toBeVisible({
        timeout: 10000,
      })
    })

    test("shows Settings label on settings view", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=settings")

      const heading = page.locator("h1")
      await expect(heading.getByText("Settings")).toBeVisible({
        timeout: 10000,
      })
    })
  })

  test.describe("view label updates via NavRail", () => {
    test("updates to List when clicking List NavRail button", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await page.getByLabel("List").click()

      const heading = page.locator("h1")
      await expect(heading.getByText("List")).toBeVisible()
    })

    test("updates to Terminal when clicking Terminal NavRail button", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await page.getByLabel("Terminal").click()

      const heading = page.locator("h1")
      await expect(heading.getByText("Terminal")).toBeVisible()
    })

    test("updates to Settings when clicking Settings NavRail button", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await page.getByLabel("Settings").click()

      const heading = page.locator("h1")
      await expect(heading.getByText("Settings")).toBeVisible()
    })

    test("updates back to Kanban when clicking Kanban NavRail button", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?view=table")

      await page.getByLabel("Kanban").click()

      const heading = page.locator("h1")
      await expect(heading.getByText("Kanban")).toBeVisible()
    })
  })

  test.describe("workspace name display", () => {
    test("displays workspace name from API response", async ({ page }) => {
      const customWorkspace = createWorkspaceResponse("alpha-team", [
        { name: "repo1", path: "/mock/repo1" },
        { name: "repo2", path: "/mock/repo2" },
      ])
      await setupMocks(page, { workspace: customWorkspace })
      await navigateAndWait(page)

      const heading = page.locator("h1")
      await expect(heading.getByText("alpha-team")).toBeVisible({
        timeout: 10000,
      })
    })
  })

  test.describe("visual structure", () => {
    test("dot has a background color", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await expect(
        page.locator("h1").getByText("my-project")
      ).toBeVisible({ timeout: 10000 })

      const dot = page.locator('h1 span[style*="background"]')
      await expect(dot).toBeVisible()
    })
  })
})
