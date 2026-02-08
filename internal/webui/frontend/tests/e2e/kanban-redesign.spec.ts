import { test, expect } from "@playwright/test"

/**
 * E2E tests for the Kanban Redesign CSS properties.
 *
 * These tests programmatically assert computed CSS values to validate
 * that design tokens are correctly applied. Unlike visual regression tests
 * (pixel comparison), these tests extract computed styles using getComputedStyle()
 * to catch CSS regressions that might pass pixel thresholds.
 *
 * Column backgrounds and header border colors are covered in kanban-column-redesign.spec.ts.
 * Card hover states, selection, and basic shadow existence are in kanban-ui-redesign.spec.ts.
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

test.describe("IssueCard CSS Styling", () => {
  test("card has 1px border", async ({ page }) => {
    const issues = [
      makeIssue({ id: "b-1", title: "Border Test", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const card = page.locator('section[data-status="ready"] article')
    await expect(card).toBeVisible()

    // Use borderTopWidth since borderWidth shorthand returns empty in computed styles
    const borderTopWidth = await card.evaluate(
      (el) => window.getComputedStyle(el).borderTopWidth
    )
    expect(borderTopWidth).toBe("1px")
  })

  test("card has correct border color (#cccccc)", async ({ page }) => {
    const issues = [
      makeIssue({ id: "bc-1", title: "Border Color Test", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const card = page.locator('section[data-status="ready"] article')
    await expect(card).toBeVisible()

    // Use borderTopColor since borderColor shorthand may be inconsistent
    const borderTopColor = await card.evaluate(
      (el) => window.getComputedStyle(el).borderTopColor
    )
    // --color-border: #cccccc → rgb(204, 204, 204)
    expect(borderTopColor).toBe("rgb(204, 204, 204)")
  })

  test("card has dual-layer box-shadow", async ({ page }) => {
    const issues = [
      makeIssue({ id: "s-1", title: "Shadow Test", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const card = page.locator('section[data-status="ready"] article')
    const shadow = await card.evaluate(
      (el) => window.getComputedStyle(el).boxShadow
    )
    // CSS: box-shadow: 0 1px 0 rgba(0, 0, 0, 0.08), 0 2px 5px rgba(0, 0, 0, 0.12)
    expect(shadow).toContain("0px 1px 0px")
    expect(shadow).toContain("0px 2px 5px")
  })

  test("card title has font-weight 600 (semibold)", async ({ page }) => {
    const issues = [
      makeIssue({ id: "fw-1", title: "Font Weight Test", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const title = page.locator('section[data-status="ready"] article h3')
    await expect(title).toBeVisible()

    const fontWeight = await title.evaluate(
      (el) => window.getComputedStyle(el).fontWeight
    )
    expect(fontWeight).toBe("600")
  })

  test("P0 card title has font-weight 700 (bold)", async ({ page }) => {
    const issues = [
      makeIssue({
        id: "p0-fw",
        title: "P0 Font Weight",
        status: "open",
        priority: 0,
      }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const title = page.locator('article[data-priority="0"] h3')
    await expect(title).toBeVisible()

    const fontWeight = await title.evaluate(
      (el) => window.getComputedStyle(el).fontWeight
    )
    expect(fontWeight).toBe("700")
  })

  test("card hover elevates shadow to 0 3px 8px", async ({ page }) => {
    const issues = [
      makeIssue({ id: "h-1", title: "Hover Shadow Test", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const card = page.locator(
      'section[data-status="ready"] article[role="button"]'
    )
    await expect(card).toBeVisible()

    await card.hover()
    // Wait for CSS transition (0.15s)
    await page.waitForTimeout(200)

    const shadow = await card.evaluate(
      (el) => window.getComputedStyle(el).boxShadow
    )
    // CSS: box-shadow: 0 3px 8px rgba(0, 0, 0, 0.14)
    expect(shadow).toContain("0px 3px 8px")
  })

  test("in-progress card has same border as other cards (no special left border)", async ({
    page,
  }) => {
    const issues = [
      makeIssue({
        id: "ip-1",
        title: "In Progress Card",
        status: "in_progress",
        priority: 2,
      }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const card = page.locator(
      'section[data-status="in_progress"] article[data-column="in_progress"]'
    )
    await expect(card).toBeVisible()

    // CSS: border-left: 1px solid var(--color-border) - same as other cards
    const borderLeftWidth = await card.evaluate(
      (el) => window.getComputedStyle(el).borderLeftWidth
    )
    expect(borderLeftWidth).toBe("1px")
  })
})

test.describe("Priority Badge CSS Colors", () => {
  test("P0 badge has warm red background (#e24b3b)", async ({ page }) => {
    const issues = [
      makeIssue({
        id: "p0-bg",
        title: "P0 Badge Color",
        status: "open",
        priority: 0,
      }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const badge = page.locator('span[data-priority="0"]')
    await expect(badge).toBeVisible()

    const bgColor = await badge.evaluate(
      (el) => window.getComputedStyle(el).backgroundColor
    )
    // --color-priority-0: #e24b3b → rgb(226, 75, 59)
    expect(bgColor).toBe("rgb(226, 75, 59)")
  })

  test("P0 badge has white text", async ({ page }) => {
    const issues = [
      makeIssue({
        id: "p0-text",
        title: "P0 Text Color",
        status: "open",
        priority: 0,
      }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const badge = page.locator('span[data-priority="0"]')
    const textColor = await badge.evaluate(
      (el) => window.getComputedStyle(el).color
    )
    // color: var(--color-text-inverse) → #ffffff → rgb(255, 255, 255)
    expect(textColor).toBe("rgb(255, 255, 255)")
  })

  test("P1 badge has orange background (#ef7f4a)", async ({ page }) => {
    const issues = [
      makeIssue({
        id: "p1-bg",
        title: "P1 Badge Color",
        status: "open",
        priority: 1,
      }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const badge = page.locator('span[data-priority="1"]')
    await expect(badge).toBeVisible()

    const bgColor = await badge.evaluate(
      (el) => window.getComputedStyle(el).backgroundColor
    )
    // --color-priority-1: #ef7f4a → rgb(239, 127, 74)
    expect(bgColor).toBe("rgb(239, 127, 74)")
  })

  test("P2 badge has warm yellow background (#f0b24a)", async ({ page }) => {
    const issues = [
      makeIssue({
        id: "p2-bg",
        title: "P2 Badge Color",
        status: "open",
        priority: 2,
      }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const badge = page.locator('span[data-priority="2"]')
    await expect(badge).toBeVisible()

    const bgColor = await badge.evaluate(
      (el) => window.getComputedStyle(el).backgroundColor
    )
    // --color-priority-2: #f0b24a → rgb(240, 178, 74)
    expect(bgColor).toBe("rgb(240, 178, 74)")
  })

  test("P2 badge has dark text (WCAG contrast)", async ({ page }) => {
    const issues = [
      makeIssue({
        id: "p2-text",
        title: "P2 Text Color",
        status: "open",
        priority: 2,
      }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const badge = page.locator('span[data-priority="2"]')
    const textColor = await badge.evaluate(
      (el) => window.getComputedStyle(el).color
    )
    // P2 uses dark text for contrast: var(--color-text) → #000000 → rgb(0, 0, 0)
    expect(textColor).toBe("rgb(0, 0, 0)")
  })

  test("P3 badge has soft blue background (#5b85f7)", async ({ page }) => {
    const issues = [
      makeIssue({
        id: "p3-bg",
        title: "P3 Badge Color",
        status: "in_progress",
        priority: 3,
      }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const badge = page.locator('span[data-priority="3"]')
    await expect(badge).toBeVisible()

    const bgColor = await badge.evaluate(
      (el) => window.getComputedStyle(el).backgroundColor
    )
    // --color-priority-3: #5b85f7 → rgb(91, 133, 247)
    expect(bgColor).toBe("rgb(91, 133, 247)")
  })

  test("P4 badge has gray background (#6b7280)", async ({ page }) => {
    const issues = [
      makeIssue({
        id: "p4-bg",
        title: "P4 Badge Color",
        status: "open",
        priority: 4,
      }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const badge = page.locator('span[data-priority="4"]')
    await expect(badge).toBeVisible()

    const bgColor = await badge.evaluate(
      (el) => window.getComputedStyle(el).backgroundColor
    )
    // --color-priority-4: #6b7280 → rgb(107, 114, 128)
    expect(bgColor).toBe("rgb(107, 114, 128)")
  })
})

test.describe("Header & Nav CSS Styling", () => {
  test("active nav button has soft blue background (#5b85f7)", async ({
    page,
  }) => {
    const issues = [
      makeIssue({ id: "t-1", title: "Nav Test", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    // NavRail is used for view switching (not ViewSwitcher tabs)
    // The active button has data-active="true" and aria-label="Kanban"
    const activeNavButton = page.locator(
      'nav[aria-label="Primary"] button[data-active="true"]'
    )
    await expect(activeNavButton).toBeVisible()
    await expect(activeNavButton).toHaveAttribute("aria-label", "Kanban")

    const bgColor = await activeNavButton.evaluate(
      (el) => window.getComputedStyle(el).backgroundColor
    )
    // .navButton[data-active='true']: background-color: var(--color-priority-3) → #5b85f7 → rgb(91, 133, 247)
    expect(bgColor).toBe("rgb(91, 133, 247)")
  })

  test("active nav button has white text/icon color", async ({ page }) => {
    const issues = [
      makeIssue({ id: "t-2", title: "Nav Text Test", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const activeNavButton = page.locator(
      'nav[aria-label="Primary"] button[data-active="true"]'
    )
    await expect(activeNavButton).toBeVisible()

    const textColor = await activeNavButton.evaluate(
      (el) => window.getComputedStyle(el).color
    )
    // .navButton[data-active='true']: color: var(--color-text-inverse) → white → rgb(255, 255, 255)
    expect(textColor).toBe("rgb(255, 255, 255)")
  })

  test("nav rail has border separator", async ({ page }) => {
    const issues = [
      makeIssue({ id: "t-3", title: "Nav Border Test", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    // NavRail has a border-right separator
    const navRail = page.locator('nav[aria-label="Primary"]')
    await expect(navRail).toBeVisible()

    const borderRightColor = await navRail.evaluate(
      (el) => window.getComputedStyle(el).borderRightColor
    )
    // .navRail: border-right: 1px solid var(--color-border) → #cccccc → rgb(204, 204, 204)
    expect(borderRightColor).toBe("rgb(204, 204, 204)")
  })

  test("unified header height is 56px", async ({ page }) => {
    const issues = [
      makeIssue({ id: "h-1", title: "Header Height Test", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    // The unified header containing the tabs
    const header = page.locator('header, [role="banner"]').first()
    await expect(header).toBeVisible()

    const height = await header.evaluate(
      (el) => window.getComputedStyle(el).height
    )
    // --unified-header-height: 56px
    expect(height).toBe("56px")
  })
})

test.describe("Layout Structure CSS", () => {
  test("sidebar has width of 240px when expanded", async ({ page }) => {
    const issues = [
      makeIssue({ id: "sb-1", title: "Sidebar Width Test", status: "open" }),
    ]
    await setupMocks(page, { issues })

    // Mock loom endpoints to prevent connection errors
    await page.route("**/api/loom/**", async (route) => {
      await route.abort()
    })
    await page.route("**/loom/**", async (route) => {
      await route.abort()
    })

    await navigateToKanban(page)

    const sidebar = page.locator('aside[data-collapsed="false"]')
    await expect(sidebar).toBeVisible()

    const width = await sidebar.evaluate(
      (el) => window.getComputedStyle(el).width
    )
    // .sidebar: width: 240px
    expect(width).toBe("240px")
  })

  test("sidebar has min-width matching width for non-collapsible state", async ({
    page,
  }) => {
    const issues = [
      makeIssue({
        id: "sb-2",
        title: "Sidebar MinWidth Test",
        status: "open",
      }),
    ]
    await setupMocks(page, { issues })

    await page.route("**/api/loom/**", async (route) => {
      await route.abort()
    })
    await page.route("**/loom/**", async (route) => {
      await route.abort()
    })

    await navigateToKanban(page)

    // Current app has collapsible={false}, so sidebar always stays expanded
    // Verify min-width matches width to prevent resizing
    const sidebar = page.locator('aside[data-collapsed="false"]')
    await expect(sidebar).toBeVisible()

    const [width, minWidth] = await sidebar.evaluate((el) => {
      const style = window.getComputedStyle(el)
      return [style.width, style.minWidth]
    })
    // .sidebar: width: 240px, min-width: 240px
    expect(width).toBe("240px")
    expect(minWidth).toBe("240px")
  })

  test("Talk to Lead FAB has fixed positioning", async ({ page }) => {
    const issues = [
      makeIssue({ id: "fab-1", title: "FAB Position Test", status: "open" }),
    ]
    await setupMocks(page, { issues })
    await navigateToKanban(page)

    const fab = page.getByTestId("talk-to-lead-button")
    await expect(fab).toBeVisible()

    const position = await fab.evaluate(
      (el) => window.getComputedStyle(el).position
    )
    expect(position).toBe("fixed")
  })

  test("sidebar renders on the left side of the layout", async ({ page }) => {
    const issues = [
      makeIssue({ id: "layout-1", title: "Layout Order Test", status: "open" }),
    ]
    await setupMocks(page, { issues })

    await page.route("**/api/loom/**", async (route) => {
      await route.abort()
    })
    await page.route("**/loom/**", async (route) => {
      await route.abort()
    })

    await navigateToKanban(page)

    // The layout should have sidebar (aside) before main content
    // Verify by checking the sidebar's bounding box is to the left of the board
    const sidebar = page.locator("aside[data-collapsed]")
    const board = page.locator('[data-testid="kanban-board"]').or(
      page.locator('main, [role="main"]')
    ).first()

    await expect(sidebar).toBeVisible()
    await expect(board).toBeVisible()

    const sidebarBox = await sidebar.boundingBox()
    const boardBox = await board.boundingBox()

    expect(sidebarBox).not.toBeNull()
    expect(boardBox).not.toBeNull()

    // Sidebar should be to the left of the board
    expect(sidebarBox!.x).toBeLessThan(boardBox!.x)
  })
})
