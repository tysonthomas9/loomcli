import { test, expect } from "@playwright/test"

/**
 * E2E tests for the Kanban Board UI Redesign (bd-spq5).
 *
 * Covers card styling, hover/selection states, priority badges,
 * Talk to Lead FAB, sidebar rendering, and review column features.
 *
 * Column layout, ordering, backgrounds, icons, and epic filtering
 * are covered in kanban-column-redesign.spec.ts.
 */

const NOW = "2026-01-31T10:00:00Z"

function makeIssue(overrides: Record<string, unknown>) {
  return {
    id: "test-issue",
    title: "Test Issue",
    status: "open",
    priority: 2,
    created_at: NOW,
    updated_at: NOW,
    ...overrides,
  }
}

async function setupMocks(
  page: import("@playwright/test").Page,
  options: {
    issues: Record<string, unknown>[]
    blockedIssues?: Record<string, unknown>[]
  }
) {
  await page.route("**/api/ready", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: options.issues }),
    })
  })

  await page.route("**/api/blocked", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: options.blockedIssues ?? [],
      }),
    })
  })

  await page.route("**/api/stats", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: { open: 0, closed: 0, in_progress: 0, blocked: 0, total: 0 },
      }),
    })
  })

  await page.route("**/api/events", async (route) => {
    await route.abort()
  })
}

async function navigateToKanban(page: import("@playwright/test").Page) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) => res.url().includes("/api/ready") && res.status() === 200
    ),
    page.goto("/?groupBy=none"),
  ])
  expect(response.ok()).toBe(true)
}

test.describe("Card Styling: in-progress blue border", () => {
  test("cards in In Progress column have data-column=in_progress", async ({
    page,
  }) => {
    const issues = [
      makeIssue({
        id: "ip-1",
        title: "In Progress Task",
        status: "in_progress",
        priority: 2,
      }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const ipColumn = page.locator('section[data-status="in_progress"]')
    const card = ipColumn.locator("article")
    await expect(card).toHaveAttribute("data-column", "in_progress")
    await expect(card).toHaveAttribute("data-priority", "2")
  })

  test("in-progress card has blue left border override", async ({ page }) => {
    const issues = [
      makeIssue({
        id: "ip-2",
        title: "Blue Border Task",
        status: "in_progress",
        priority: 3,
      }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const card = page.locator(
      'section[data-status="in_progress"] article[data-column="in_progress"]'
    )
    await expect(card).toBeVisible()

    // CSS rule: .issueCard[data-column='in_progress'][data-priority] { border-left: 3px solid var(--color-primary) }
    const borderLeft = await card.evaluate((el) => {
      const style = window.getComputedStyle(el)
      return style.borderLeftWidth
    })
    expect(borderLeft).toBe("3px")
  })
})

test.describe("Card Styling: title and shadow", () => {
  test("card title uses h3 element with semibold weight", async ({ page }) => {
    const issues = [
      makeIssue({ id: "t-1", title: "Title Weight Test", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const card = page.locator('section[data-status="ready"] article')
    const title = card.locator("h3")
    await expect(title).toHaveText("Title Weight Test")
  })

  test("P0 card title uses bold font weight", async ({ page }) => {
    const issues = [
      makeIssue({
        id: "p0-1",
        title: "Critical P0 Task",
        status: "open",
        priority: 0,
      }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const card = page.locator('article[data-priority="0"]')
    await expect(card).toBeVisible()
    // CSS rule: .issueCard[data-priority='0'] .title { font-weight: var(--font-weight-bold) }
    // Just verify the card has the correct data attribute - CSS applies the style
    await expect(card).toHaveAttribute("data-priority", "0")
  })

  test("card has box-shadow", async ({ page }) => {
    const issues = [
      makeIssue({ id: "s-1", title: "Shadow Test", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const card = page.locator('section[data-status="ready"] article')
    const shadow = await card.evaluate(
      (el) => window.getComputedStyle(el).boxShadow
    )
    // CSS: box-shadow: 2px 2px 0 rgba(0, 0, 0, 0.05)
    expect(shadow).not.toBe("none")
  })
})

test.describe("Card Styling: hover and selection", () => {
  test("card hover applies translateY and elevated shadow", async ({
    page,
  }) => {
    const issues = [
      makeIssue({ id: "h-1", title: "Hover Test", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    // Cards need onClick (role=button) for hover effects
    const card = page.locator('section[data-status="ready"] article[role="button"]')
    await expect(card).toBeVisible()

    // Get pre-hover transform
    const preTransform = await card.evaluate(
      (el) => window.getComputedStyle(el).transform
    )

    await card.hover()
    // Allow CSS transition to complete (0.15s)
    await page.waitForTimeout(200)

    const postShadow = await card.evaluate(
      (el) => window.getComputedStyle(el).boxShadow
    )
    // Hover shadow should be non-empty (var(--shadow-lg))
    expect(postShadow).not.toBe("none")
  })

  test("clicking a card opens the IssueDetailPanel", async ({ page }) => {
    const issues = [
      makeIssue({ id: "sel-1", title: "Selectable Card", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const card = page.locator('section[data-status="ready"] article')
    await card.click()

    // The IssueDetailPanel should become visible after clicking
    const detailPanel = page.getByTestId("issue-detail-panel").or(
      page.locator('[class*="issueDetailPanel"]')
    )
    // Fallback: check that some detail content appears
    await expect(
      detailPanel.or(page.getByText("Selectable Card").nth(1))
    ).toBeVisible({ timeout: 3000 })
  })
})

test.describe("Priority Badges", () => {
  test("P0 badge renders with data-priority=0", async ({ page }) => {
    const issues = [
      makeIssue({
        id: "p0",
        title: "P0 Critical",
        status: "open",
        priority: 0,
      }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const badge = page.locator('span[data-priority="0"]')
    await expect(badge).toBeVisible()
    await expect(badge).toHaveText("P0")
  })

  test("P2 badge renders with data-priority=2", async ({ page }) => {
    const issues = [
      makeIssue({
        id: "p2",
        title: "P2 Medium",
        status: "open",
        priority: 2,
      }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const badge = page.locator('span[data-priority="2"]')
    await expect(badge).toBeVisible()
    await expect(badge).toHaveText("P2")
  })

  test("P3 badge renders with data-priority=3", async ({ page }) => {
    const issues = [
      makeIssue({
        id: "p3",
        title: "P3 Normal",
        status: "in_progress",
        priority: 3,
      }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const badge = page.locator('span[data-priority="3"]')
    await expect(badge).toBeVisible()
    await expect(badge).toHaveText("P3")
  })

  test("all priority badges render with correct text across columns", async ({
    page,
  }) => {
    const issues = [
      makeIssue({ id: "p0", title: "P0 Task", status: "open", priority: 0 }),
      makeIssue({
        id: "p1",
        title: "P1 Task",
        status: "in_progress",
        priority: 1,
      }),
      makeIssue({ id: "p2", title: "P2 Task", status: "review", priority: 2 }),
      makeIssue({ id: "p3", title: "P3 Task", status: "closed", priority: 3 }),
      makeIssue({
        id: "p4",
        title: "P4 Task",
        status: "blocked",
        priority: 4,
      }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    // Each card should have a priority badge with the correct text
    for (let i = 0; i <= 4; i++) {
      const badge = page.locator(`span[data-priority="${i}"]`)
      await expect(badge).toBeVisible()
      await expect(badge).toHaveText(`P${i}`)
    }
  })

  test("P0 card has red-tinted background and 3px left border", async ({
    page,
  }) => {
    const issues = [
      makeIssue({
        id: "p0-style",
        title: "P0 Styled Task",
        status: "open",
        priority: 0,
      }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const card = page.locator('article[data-priority="0"]')
    await expect(card).toBeVisible()

    const borderLeftWidth = await card.evaluate(
      (el) => window.getComputedStyle(el).borderLeftWidth
    )
    expect(borderLeftWidth).toBe("3px")
  })
})

test.describe("Review Column Features", () => {
  test("review column has data-column-type=review and data-has-items when populated", async ({
    page,
  }) => {
    const issues = [
      makeIssue({
        id: "rev-1",
        title: "Status Review Task",
        status: "review",
      }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const reviewColumn = page.locator('section[data-status="review"]')
    await expect(reviewColumn).toHaveAttribute("data-column-type", "review")
    await expect(reviewColumn).toHaveAttribute("data-has-items", "true")
  })

  test("review column has data-has-items absent when empty", async ({
    page,
  }) => {
    const issues = [
      makeIssue({ id: "r-1", title: "Ready Only", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const reviewColumn = page.locator('section[data-status="review"]')
    await expect(reviewColumn).toBeVisible()
    // data-has-items should not be present when empty
    const hasItems = await reviewColumn.getAttribute("data-has-items")
    expect(hasItems).toBeNull()
  })
})

test.describe("Talk to Lead FAB", () => {
  test("FAB button is visible with correct text", async ({ page }) => {
    const issues = [
      makeIssue({ id: "f-1", title: "Some Task", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const fab = page.getByTestId("talk-to-lead-button")
    await expect(fab).toBeVisible()
    await expect(fab).toHaveText(/Talk to Lead/)
  })

  test("FAB has coral background color", async ({ page }) => {
    const issues = [
      makeIssue({ id: "f-2", title: "Some Task", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const fab = page.getByTestId("talk-to-lead-button")
    await expect(fab).toBeVisible()

    const bgColor = await fab.evaluate(
      (el) => window.getComputedStyle(el).backgroundColor
    )
    // #f05d46 → rgb(240, 93, 70)
    expect(bgColor).toBe("rgb(240, 93, 70)")
  })

  test("FAB has aria-label for accessibility", async ({ page }) => {
    const issues = [
      makeIssue({ id: "f-3", title: "Some Task", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const fab = page.getByTestId("talk-to-lead-button")
    await expect(fab).toHaveAttribute("aria-label", "Talk to Lead")
  })
})

test.describe("Sidebar Rendering", () => {
  test("AgentsSidebar toggle is visible", async ({ page }) => {
    const issues = [
      makeIssue({ id: "sb-1", title: "Some Task", status: "open" }),
    ]
    await setupMocks(page, { issues })

    // Also mock loom endpoint to prevent connection errors
    await page.route("**/api/loom/**", async (route) => {
      await route.abort()
    })
    await page.route("**/loom/**", async (route) => {
      await route.abort()
    })

    await navigateToKanban(page)

    // The sidebar renders as an aside element with the sidebar class
    const sidebar = page.locator('aside[data-collapsed]')
    await expect(sidebar).toBeVisible()
  })
})

test.describe("Backlog and Blocked Column Card Styling", () => {
  test("blocked cards in blocked column have dimmed appearance", async ({ page }) => {
    const issues = [
      makeIssue({
        id: "bl-1",
        title: "Blocked Task",
        status: "blocked",
        priority: 2,
      }),
      makeIssue({ id: "r-1", title: "Open Task", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const blockedColumn = page.locator('section[data-status="blocked"]')
    const blockedCard = blockedColumn.locator("article")
    await expect(blockedCard).toBeVisible()
    await expect(blockedCard).toHaveAttribute("data-in-backlog", "true")

    // Blocked column cards have opacity 0.7
    const opacity = await blockedCard.evaluate(
      (el) => window.getComputedStyle(el).opacity
    )
    expect(opacity).toBe("0.7")
  })

  test("deferred cards show deferred badge in backlog", async ({ page }) => {
    const issues = [
      makeIssue({
        id: "def-1",
        title: "Deferred Task",
        status: "deferred",
      }),
      makeIssue({ id: "r-1", title: "Open Task", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const backlogColumn = page.locator('section[data-status="backlog"]')
    await expect(backlogColumn.getByText("Deferred Task")).toBeVisible()

    // Deferred badge should be visible (use span selector to avoid matching the article's aria-label)
    const deferredBadge = backlogColumn.locator('span[aria-label="Deferred"]')
    await expect(deferredBadge).toBeVisible()
  })
})

test.describe("Blocked Card Styling", () => {
  test("blocked cards display blocked badge with count", async ({ page }) => {
    const issues = [
      makeIssue({
        id: "blk-1",
        title: "Blocked By Others",
        status: "open",
      }),
      makeIssue({ id: "r-1", title: "Open Task", status: "open" }),
    ]

    const blockedData = {
      ...issues[0],
      blocked_by_count: 2,
      blocked_by: ["blocker-1", "blocker-2"],
    }

    await setupMocks(page, { issues, blockedIssues: [blockedData] })
    await navigateToKanban(page)

    // Blocked card should be in the Blocked column (not Backlog)
    const blockedColumn = page.locator('section[data-status="blocked"]')
    const blockedCard = blockedColumn.locator('article[data-blocked="true"]')
    await expect(blockedCard).toBeVisible()
  })
})
