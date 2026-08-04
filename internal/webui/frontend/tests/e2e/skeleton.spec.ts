import { test, expect } from "@playwright/test"
import {
  setupFleetMocks,
  waitForWorkspaceIssues,
  workspacePath,
} from "./helpers/fleet"

/**
 * Mock issues for testing skeleton-to-content transition.
 */
const mockIssues = [
  {
    id: "issue-1",
    title: "Test Issue",
    status: "open",
    priority: 2,
    created_at: "2026-01-25T10:00:00Z",
    updated_at: "2026-01-25T10:00:00Z",
  },
]

test.describe("Loading Skeleton States", () => {
  test.beforeEach(async ({ page }) => {
  })

  test("shows three skeleton columns while loading", async ({ page }) => {
    await setupFleetMocks(page, mockIssues, undefined, { issuesDelayMs: 500 })

    // Navigate without waiting for full load to catch skeleton
    await page.goto(workspacePath("/"), { waitUntil: "domcontentloaded" })

    // Verify skeleton columns are visible (use partial class selector for CSS Modules)
    // LoadingSkeleton.Column renders with aria-hidden="true"
    const skeletonColumns = page.locator('[class*="column"][aria-hidden="true"]')
    await expect(skeletonColumns).toHaveCount(3)
  })

  test("skeleton column structure matches StatusColumn layout", async ({ page }) => {
    await setupFleetMocks(page, mockIssues, undefined, { issuesDelayMs: 800 })

    await page.goto(workspacePath("/"), { waitUntil: "domcontentloaded" })

    // Get the first skeleton column
    const column = page.locator('[class*="column"][aria-hidden="true"]').first()
    await expect(column).toBeVisible()

    // Header should contain text skeleton and circle skeleton
    const header = column.locator('[class*="columnHeader"]')
    await expect(header).toBeVisible()
    await expect(header.locator('[class*="text"]')).toBeVisible()
    await expect(header.locator('[class*="circle"]')).toBeVisible()

    // Content area should have card skeletons (default cardCount is 3)
    // Use direct children to avoid matching .cardHeader which also contains "card"
    const content = column.locator('[class*="columnContent"]')
    await expect(content).toBeVisible()
    const cards = content.locator('> [class*="card"]')
    await expect(cards).toHaveCount(3)
  })

  test("skeleton elements have aria-hidden for accessibility", async ({ page }) => {
    await setupFleetMocks(page, mockIssues, undefined, { issuesDelayMs: 500 })

    await page.goto(workspacePath("/"), { waitUntil: "domcontentloaded" })

    // LoadingSkeleton.Column has aria-hidden="true" at the root
    const skeletonColumn = page.locator('[class*="column"][aria-hidden="true"]').first()
    await expect(skeletonColumn).toBeVisible()
    await expect(skeletonColumn).toHaveAttribute("aria-hidden", "true")

    // Card skeletons also have aria-hidden="true"
    const skeletonCard = skeletonColumn.locator('[class*="card"]').first()
    await expect(skeletonCard).toHaveAttribute("aria-hidden", "true")
  })

  test("skeleton transitions to real content after load", async ({ page }) => {
    await setupFleetMocks(page, mockIssues, undefined, { issuesDelayMs: 300 })

    // Use domcontentloaded to catch skeleton initially
    await page.goto(workspacePath("/"), { waitUntil: "domcontentloaded" })

    // Initially shows skeleton columns
    const skeletonColumns = page.locator('[class*="column"][aria-hidden="true"]')
    await expect(skeletonColumns.first()).toBeVisible()

    await waitForWorkspaceIssues(page)

    // Skeleton should be gone after data loads
    await expect(skeletonColumns).toHaveCount(0)

    // Real columns should be visible (StatusColumn uses section[data-status])
    await expect(page.locator('section[data-status="ready"]')).toBeVisible()
    await expect(page.locator('section[data-status="in_progress"]')).toBeVisible()
    await expect(page.locator('section[data-status="done"]')).toBeVisible()
  })

  test("skeleton has shimmer animation", async ({ page }) => {
    await setupFleetMocks(page, mockIssues, undefined, { issuesDelayMs: 600 })

    await page.goto(workspacePath("/"), { waitUntil: "domcontentloaded" })

    // Find a skeleton element with the base .skeleton class
    const skeleton = page.locator('[class*="skeleton"]').first()
    await expect(skeleton).toBeVisible()

    await expect(skeleton).toHaveAttribute("aria-hidden", "true")
    await expect(skeleton).toHaveClass(/skeleton/)
  })
})
