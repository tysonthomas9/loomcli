import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for the RepoSelector component.
 * Tests the multi-checkbox dropdown in FilterBar for filtering issues by repository.
 */

function createWorkspaceResponse(
  repos: Array<{ name: string; path: string }>
) {
  return {
    success: true,
    data: {
      name: "test-workspace",
      path: "/tmp/test",
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
          name: "test-workspace",
          path: "/tmp/test",
          active: true,
          repo_count: repos.length,
          is_default: true,
        },
      ],
      default_workspace: "test-workspace",
    },
  }
}

const multiRepoWorkspace = createWorkspaceResponse([
  { name: "frontend", path: "/tmp/test/frontend" },
  { name: "backend", path: "/tmp/test/backend" },
  { name: "shared", path: "/tmp/test/shared" },
])

const singleRepoWorkspace = createWorkspaceResponse([
  { name: "monorepo", path: "/tmp/test/monorepo" },
])

const mockIssues = [
  {
    id: "issue-fe-1",
    title: "Login page",
    status: "open",
    priority: 1,
    issue_type: "task",
    source_repo: "frontend",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
  {
    id: "issue-fe-2",
    title: "Dashboard CSS",
    status: "open",
    priority: 2,
    issue_type: "task",
    source_repo: "frontend",
    created_at: "2026-01-24T11:00:00Z",
    updated_at: "2026-01-24T11:00:00Z",
  },
  {
    id: "issue-be-1",
    title: "API endpoint",
    status: "open",
    priority: 1,
    issue_type: "task",
    source_repo: "backend",
    created_at: "2026-01-24T12:00:00Z",
    updated_at: "2026-01-24T12:00:00Z",
  },
  {
    id: "issue-be-2",
    title: "DB migration",
    status: "in_progress",
    priority: 2,
    issue_type: "task",
    source_repo: "backend",
    created_at: "2026-01-24T13:00:00Z",
    updated_at: "2026-01-24T13:00:00Z",
  },
  {
    id: "issue-sh-1",
    title: "Shared utils",
    status: "open",
    priority: 3,
    issue_type: "task",
    source_repo: "shared",
    created_at: "2026-01-24T14:00:00Z",
    updated_at: "2026-01-24T14:00:00Z",
  },
]

async function setupMocks(
  page: Page,
  options?: {
    workspace?: typeof multiRepoWorkspace
  }
) {
  const workspace = options?.workspace ?? multiRepoWorkspace

  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({ status: 404 })
  })

  await page.route("**/api/workspace", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(workspace),
    })
  })

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
        data: { open: 4, closed: 0, total: 5, completion: 0 },
      }),
    })
  })

  await page.route("**/api/blocked", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: [] }),
    })
  })

  await page.route("**/api/events", async (route) => {
    await route.abort()
  })
}

async function navigateAndWait(page: Page, path = "/") {
  await page.goto(path)
  await page.waitForTimeout(1000)
}

test.describe("RepoSelector E2E", () => {
  test.describe("Visibility", () => {
    test("not visible when workspace has only 1 repo", async ({ page }) => {
      await setupMocks(page, { workspace: singleRepoWorkspace })
      await navigateAndWait(page)

      await expect(
        page.getByTestId("repo-filter-trigger")
      ).not.toBeVisible()
    })

    test("visible when workspace has 2+ repos", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await expect(
        page.getByTestId("repo-filter-trigger")
      ).toBeVisible({ timeout: 10000 })
    })
  })

  test.describe("Dropdown interactions", () => {
    test("clicking trigger opens dropdown with repo checkboxes", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const trigger = page.getByTestId("repo-filter-trigger")
      await expect(trigger).toBeVisible({ timeout: 10000 })
      await trigger.click()

      const menu = page.getByTestId("repo-filter-menu")
      await expect(menu).toBeVisible()

      await expect(
        page.getByTestId("repo-option-frontend")
      ).toBeVisible()
      await expect(
        page.getByTestId("repo-option-backend")
      ).toBeVisible()
      await expect(
        page.getByTestId("repo-option-shared")
      ).toBeVisible()
    })

    test("clicking trigger again closes dropdown", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const trigger = page.getByTestId("repo-filter-trigger")
      await expect(trigger).toBeVisible({ timeout: 10000 })
      await trigger.click()

      const menu = page.getByTestId("repo-filter-menu")
      await expect(menu).toBeVisible()

      await trigger.click()
      await expect(menu).not.toBeVisible()
    })

    test("clicking outside closes dropdown", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const trigger = page.getByTestId("repo-filter-trigger")
      await expect(trigger).toBeVisible({ timeout: 10000 })
      await trigger.click()

      const menu = page.getByTestId("repo-filter-menu")
      await expect(menu).toBeVisible()

      await page.locator("body").click({ position: { x: 10, y: 10 } })
      await expect(menu).not.toBeVisible()
    })
  })

  test.describe("Repo filtering", () => {
    test("selecting a repo filters issues to only that repo", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const trigger = page.getByTestId("repo-filter-trigger")
      await expect(trigger).toBeVisible({ timeout: 10000 })
      await trigger.click()

      await page.getByTestId("repo-option-frontend").click()

      await expect(page.getByText("Login page")).toBeVisible()
      await expect(page.getByText("Dashboard CSS")).toBeVisible()
      await expect(page.getByText("API endpoint")).not.toBeVisible()
      await expect(page.getByText("Shared utils")).not.toBeVisible()
    })

    test("selecting multiple repos shows issues from all selected", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const trigger = page.getByTestId("repo-filter-trigger")
      await expect(trigger).toBeVisible({ timeout: 10000 })
      await trigger.click()

      await page.getByTestId("repo-option-frontend").click()
      await page.getByTestId("repo-option-backend").click()

      await expect(page.getByText("Login page")).toBeVisible()
      await expect(page.getByText("API endpoint")).toBeVisible()
      await expect(page.getByText("Shared utils")).not.toBeVisible()
    })

    test("deselecting all repos shows all issues again", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const trigger = page.getByTestId("repo-filter-trigger")
      await expect(trigger).toBeVisible({ timeout: 10000 })
      await trigger.click()

      await page.getByTestId("repo-option-frontend").click()
      await page.getByTestId("repo-option-frontend").click()

      await expect(page.getByText("Login page")).toBeVisible()
      await expect(page.getByText("API endpoint")).toBeVisible()
      await expect(page.getByText("Shared utils")).toBeVisible()
    })
  })

  test.describe("URL synchronization", () => {
    test("selecting repos updates URL with repos param", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const trigger = page.getByTestId("repo-filter-trigger")
      await expect(trigger).toBeVisible({ timeout: 10000 })
      await trigger.click()

      await page.getByTestId("repo-option-frontend").click()
      expect(page.url()).toContain("repos=frontend")
    })

    test("navigating to URL with repos param pre-selects repos", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?repos=backend")

      await expect(page.getByText("API endpoint")).toBeVisible()
      await expect(page.getByText("Login page")).not.toBeVisible()
    })

    test("clearing all repos removes repos param from URL", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?repos=frontend")

      const trigger = page.getByTestId("repo-filter-trigger")
      await expect(trigger).toBeVisible({ timeout: 10000 })
      await trigger.click()

      await page.getByTestId("repo-option-frontend").click()

      expect(page.url()).not.toContain("repos=")
    })
  })
})
