import { test, expect } from "@playwright/test"

test.describe("App", () => {
  test("homepage loads successfully", async ({ page }) => {
    const response = await page.goto("/")
    expect(response?.status()).toBe(200)
  })

  test("has correct page title", async ({ page }) => {
    await page.goto("/")
    await expect(page).toHaveTitle("Beads Web UI")
  })

  test("displays main heading", async ({ page }) => {
    await page.goto("/")
    await expect(page.locator("h1")).toHaveText("Beads")
  })

  test("displays connection status", async ({ page }) => {
    await page.goto("/")
    // The app now shows a connection status indicator in the header
    // Use data-state attribute to find ConnectionStatus specifically (dnd-kit adds other status elements)
    await expect(page.locator('[data-state]')).toBeVisible()
  })

  test("has no console errors on load", async ({ page }) => {
    const consoleErrors: string[] = []
    page.on("console", (msg) => {
      if (msg.type() === "error") {
        consoleErrors.push(msg.text())
      }
    })

    await page.goto("/")
    await page.waitForLoadState("networkidle")

    expect(consoleErrors).toHaveLength(0)
  })

  test("page renders within acceptable time", async ({ page }) => {
    const startTime = Date.now()
    await page.goto("/")
    await page.waitForSelector("h1")
    const loadTime = Date.now() - startTime

    expect(loadTime).toBeLessThan(3000)
  })
})
