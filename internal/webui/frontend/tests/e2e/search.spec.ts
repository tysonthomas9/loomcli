import { test, expect, Page } from "@playwright/test"

/**
 * Mock issues for testing search filtering.
 * Each issue has distinct titles/descriptions for precise filtering tests.
 */
const mockIssues = [
  {
    id: "search-1",
    title: "Authentication Bug",
    description: "Login form validation error",
    status: "open",
    priority: 1,
    issue_type: "bug",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
  {
    id: "search-2",
    title: "Dashboard Feature",
    description: "Add user metrics panel",
    status: "open",
    priority: 2,
    issue_type: "feature",
    created_at: "2026-01-24T11:00:00Z",
    updated_at: "2026-01-24T11:00:00Z",
  },
  {
    id: "search-3",
    title: "API Endpoint",
    description: "Authentication middleware refactor",
    status: "in_progress",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-24T12:00:00Z",
    updated_at: "2026-01-24T12:00:00Z",
  },
  {
    id: "search-4",
    title: "Documentation",
    description: "Update README",
    status: "closed",
    priority: 3,
    issue_type: "task",
    created_at: "2026-01-24T13:00:00Z",
    updated_at: "2026-01-24T13:00:00Z",
  },
]

/**
 * Workspace fixture used by SearchInput filtering tests. Shape matches the
 * WorkspaceData interface. WorkspaceLayout calls fetchWorkspaceApi() before
 * rendering children, so the mock must return an object with a non-empty id
 * under `{ success: true, data: ... }`.
 */
const mockWorkspaceData = {
  id: "default",
  name: "default",
  path: "/test",
  repos: [],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: "default",
      name: "default",
      path: "/test",
      active: true,
      repo_count: 0,
      is_default: true,
    },
  ],
  workspace_order: ["default"],
  default_workspace: "default",
}

/**
 * Set up API mocks for search tests.
 */
async function setupMocks(page: Page) {
  // Neutralize AbortController signals in fetch. React StrictMode (dev mode)
  // double-fires effects; the cleanup aborts in-flight fetches before they
  // reach the network. openapi-fetch bakes the signal into the Request
  // object, so stripping `init.signal` is not enough — we must also
  // reconstruct Request inputs without their signal. Otherwise page.route
  // never sees the kanban /issues request because it's aborted pre-dispatch.
  //
  // Note: the openapi-fetch middleware may have attached a `_timeoutController`
  // to the incoming Request for its onResponse cleanup. We preserve that
  // reference on the reconstructed Request so the middleware's timeout
  // cleanup still runs and doesn't leak 30s timers.
  await page.addInitScript(() => {
    const origFetch = window.fetch
    window.fetch = function (input: RequestInfo | URL, init?: RequestInit) {
      const strippedInit: RequestInit = init ? { ...init } : {}
      if ("signal" in strippedInit) delete strippedInit.signal
      if (input instanceof Request) {
        const req = input
        const newInit: RequestInit = {
          method: req.method,
          headers: req.headers,
          credentials: req.credentials,
          cache: req.cache,
          redirect: req.redirect,
          referrer: req.referrer,
          referrerPolicy: req.referrerPolicy,
          integrity: req.integrity,
          keepalive: req.keepalive,
        }
        const preserveTimeout = (target: Request) => {
          const tc = (req as unknown as { _timeoutController?: unknown })
            ._timeoutController
          if (tc) {
            ;(target as unknown as { _timeoutController: unknown })._timeoutController =
              tc
          }
        }
        if (req.method !== "GET" && req.method !== "HEAD") {
          return req
            .clone()
            .blob()
            .then((blob) => {
              const newReq = new Request(req.url, { ...newInit, body: blob })
              preserveTimeout(newReq)
              return origFetch.call(this, newReq, {})
            })
        }
        const newReq = new Request(req.url, newInit)
        preserveTimeout(newReq)
        return origFetch.call(this, newReq, {})
      }
      return origFetch.call(this, input, strippedInit)
    }
  })

  // App config (boot)
  await page.route("**/api/config", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ mode: "open" }),
    })
  })

  // Auth token
  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token-search" }),
    })
  })

  // Global health (App shell fetches on mount)
  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    })
  })

  // Global backend config
  await page.route("**/api/config/backend", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: {
          backend: "shell",
          source: "default",
          available: ["shell"],
          agents: [],
        },
      }),
    })
  })

  // All /api/workspaces/* endpoints. Dispatches on pathname so a single
  // handler covers workspace metadata, kanban issues, ready issues, stats,
  // blocked, graph, and SSE abort.
  await page.route("**/api/workspaces/**", async (route) => {
    const url = new URL(route.request().url())
    const pathname = url.pathname

    // SSE events — abort so we don't hang waitForLoadState("networkidle")
    if (/\/api\/workspaces\/[^/]+\/events/.test(pathname)) {
      await route.abort()
      return
    }

    // /api/workspaces/{id}/issues — kanban mode hits this via getKanbanIssues
    // /api/workspaces/{id}/ready — table/list mode hits this via getReadyIssues
    if (
      /\/api\/workspaces\/[^/]+\/issues$/.test(pathname) ||
      /\/api\/workspaces\/[^/]+\/ready$/.test(pathname)
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: mockIssues }),
      })
      return
    }

    // /api/workspaces/{id}/stats
    if (/\/api\/workspaces\/[^/]+\/stats$/.test(pathname)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: { open: 3, closed: 1, total: 4, completion: 25 },
        }),
      })
      return
    }

    // /api/workspaces/{id}/blocked
    if (/\/api\/workspaces\/[^/]+\/blocked$/.test(pathname)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: [] }),
      })
      return
    }

    // /api/workspaces/{id}/issues/graph
    if (/\/api\/workspaces\/[^/]+\/issues\/graph$/.test(pathname)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: mockIssues }),
      })
      return
    }

    // /api/workspaces/{id} — workspace metadata. Must return an object with
    // a non-empty id, otherwise WorkspaceLayout redirects to "/" and loops.
    if (/^\/api\/workspaces\/[^/]+\/?$/.test(pathname)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: mockWorkspaceData }),
      })
      return
    }

    // Anything else under /api/workspaces/* — return empty success.
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: [] }),
    })
  })

  // Abort loom monitor requests (not workspace-scoped)
  await page.route("**/monitor/**", async (route) => {
    await route.abort()
  })
}

/**
 * Navigate to a page and wait for API response.
 */
async function navigateAndWait(page: Page, path: string) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) =>
        res.url().includes("/api/workspaces/") &&
        /\/issues(\?|$)/.test(res.url()) &&
        res.status() === 200
    ),
    page.goto(path),
  ])
  expect(response.ok()).toBe(true)
}

/**
 * Count visible issue cards across all columns.
 */
async function countVisibleCards(page: Page): Promise<number> {
  return page.locator("section[data-status] article").count()
}

test.describe("SearchInput filtering", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page)
  })

  test("filters issues by title match", async ({ page }) => {
    await navigateAndWait(page, "/ws/default/kanban")

    // Verify all 4 cards are visible initially
    await expect(async () => {
      expect(await countVisibleCards(page)).toBe(4)
    }).toPass({ timeout: 5000 })

    // Type "Authentication" in search (matches "Authentication Bug" by title)
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("Authentication")

    // Wait for debounce and verify only matching cards are visible
    await expect(async () => {
      // "Authentication Bug" in open, "API Endpoint" has "Authentication" in description
      expect(await countVisibleCards(page)).toBe(2)
    }).toPass({ timeout: 2000 })

    // Verify the specific card is visible
    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn.getByText("Authentication Bug")).toBeVisible()
  })

  test("filters issues by description match", async ({ page }) => {
    await navigateAndWait(page, "/ws/default/kanban")

    // Type "metrics" in search (matches "Dashboard Feature" by description)
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("metrics")

    // Wait for debounce and verify only matching card is visible
    await expect(async () => {
      expect(await countVisibleCards(page)).toBe(1)
    }).toPass({ timeout: 2000 })

    // Verify the specific card is visible
    const openColumn = page.locator('section[data-status="ready"]')
    await expect(openColumn.getByText("Dashboard Feature")).toBeVisible()
  })

  test("search is case-insensitive", async ({ page }) => {
    await navigateAndWait(page, "/ws/default/kanban")

    // Type in uppercase - should still match
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("AUTHENTICATION")

    // Wait for debounce and verify matches
    await expect(async () => {
      // "Authentication Bug" (title) and "API Endpoint" (description has "Authentication")
      expect(await countVisibleCards(page)).toBe(2)
    }).toPass({ timeout: 2000 })

    // Verify both matching cards are visible
    await expect(page.getByText("Authentication Bug")).toBeVisible()
    await expect(page.getByText("API Endpoint")).toBeVisible()
  })

  test("partial match filters correctly", async ({ page }) => {
    await navigateAndWait(page, "/ws/default/kanban")

    // Type partial term "Auth" - should match both "Authentication Bug" and "API Endpoint"
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("Auth")

    // Wait for debounce and verify partial matches
    await expect(async () => {
      expect(await countVisibleCards(page)).toBe(2)
    }).toPass({ timeout: 2000 })

    // Verify both cards are visible
    await expect(page.getByText("Authentication Bug")).toBeVisible()
    await expect(page.getByText("API Endpoint")).toBeVisible()
  })

  test("no matches shows empty columns", async ({ page }) => {
    await navigateAndWait(page, "/ws/default/kanban")

    // Type a term that doesn't match anything
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("xyznonexistent")

    // Wait for debounce and verify no cards visible
    await expect(async () => {
      expect(await countVisibleCards(page)).toBe(0)
    }).toPass({ timeout: 2000 })

    // When every issue is filtered out, the board renders EmptyWorkspaceBoard
    // instead of per-column "0 issues" badges. KanbanPage does not forward
    // the `filters` prop, so hasFiltersActive is false and the heading stays
    // "No issues yet" — if filters are ever threaded through, the heading
    // becomes "No issues match your filters" and this locator must update.
    await expect(
      page.getByRole("heading", { name: "No issues yet" }),
    ).toBeVisible()
  })

  test("clearing search shows all issues", async ({ page }) => {
    await navigateAndWait(page, "/ws/default/kanban")

    // First, filter to show fewer cards
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("metrics")

    // Wait for filter to apply
    await expect(async () => {
      expect(await countVisibleCards(page)).toBe(1)
    }).toPass({ timeout: 2000 })

    // Click the clear button
    const clearButton = page.getByTestId("search-input-clear")
    await clearButton.click()

    // Verify all cards are visible again
    await expect(async () => {
      expect(await countVisibleCards(page)).toBe(4)
    }).toPass({ timeout: 2000 })

    // Verify search input is cleared
    await expect(searchInput).toHaveValue("")
  })

  test("search highlights matching text in card titles", async ({ page }) => {
    await navigateAndWait(page, "/ws/default/kanban")

    // Wait for cards to render before searching
    await expect(async () => {
      expect(await countVisibleCards(page)).toBe(4)
    }).toPass({ timeout: 5000 })

    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("Auth")

    // Wait for debounce + filter
    await expect(async () => {
      expect(await countVisibleCards(page)).toBe(2)
    }).toPass({ timeout: 2000 })

    // Title "Authentication Bug" should contain a <mark> element with "Auth"
    const authCard = page
      .locator("article")
      .filter({ hasText: "Authentication Bug" })
    const titleMark = authCard.locator('[data-testid="issue-card-title"] mark')
    await expect(titleMark).toHaveCount(1)
    await expect(titleMark).toHaveText(/^Auth$/i)
  })

  test("clearing search removes highlight marks", async ({ page }) => {
    await navigateAndWait(page, "/ws/default/kanban")

    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("Auth")

    // Wait for at least one title highlight to appear
    await expect(async () => {
      const count = await page
        .locator('[data-testid="issue-card-title"] mark')
        .count()
      expect(count).toBeGreaterThan(0)
    }).toPass({ timeout: 2000 })

    // Clear search and verify marks are gone
    const clearButton = page.getByTestId("search-input-clear")
    await clearButton.click()

    await expect(page.locator('[data-testid="issue-card-title"] mark')).toHaveCount(0, {
      timeout: 2000,
    })
  })

  test("Escape key clears search", async ({ page }) => {
    await navigateAndWait(page, "/ws/default/kanban")

    // First, filter to show fewer cards
    const searchInput = page.getByTestId("search-input-field")
    await searchInput.fill("metrics")

    // Wait for filter to apply
    await expect(async () => {
      expect(await countVisibleCards(page)).toBe(1)
    }).toPass({ timeout: 2000 })

    // Press Escape while input is focused
    await searchInput.press("Escape")

    // Verify all cards are visible again
    await expect(async () => {
      expect(await countVisibleCards(page)).toBe(4)
    }).toPass({ timeout: 2000 })

    // Verify search input is cleared
    await expect(searchInput).toHaveValue("")
  })
})
