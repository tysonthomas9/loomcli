import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for the CreateWorkspaceModal component.
 * Tests modal open/close, form fields, multi-value inputs, validation, submission, and errors.
 */

const mockWorkspaceData = {
  success: true,
  data: {
    name: "existing-workspace",
    path: "/tmp/existing",
    repos: [
      {
        name: "repo-one",
        path: "/tmp/existing/repo-one",
        default_branch: "main",
        remote: "origin",
        groups: [],
      },
    ],
    groups: [],
    agents: [],
    workspaces: [
      {
        name: "existing-workspace",
        path: "/tmp/existing",
        active: true,
        repo_count: 1,
        is_default: true,
      },
    ],
    default_workspace: "existing-workspace",
  },
}

const mockIssues = [
  {
    id: "cw-test-1",
    title: "Create Workspace Test Issue",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
]

let createCalls: Array<{ body: string }> = []

async function setupMocks(
  page: Page,
  options?: {
    createError?: boolean
    createDelay?: number
  }
) {
  createCalls = []

  await page.route("**/api/events", async (route) => {
    await route.abort()
  })

  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token-e2e" }),
    })
  })

  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    })
  })

  await page.route("**/api/workspace", async (route) => {
    if (route.request().url().includes("/create")) {
      await route.fallback()
      return
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(mockWorkspaceData),
    })
  })

  await page.route("**/api/workspace/create", async (route) => {
    const body = route.request().postData() || ""
    createCalls.push({ body })

    if (options?.createDelay) {
      await new Promise((r) => setTimeout(r, options.createDelay))
    }

    if (options?.createError) {
      await route.fulfill({
        status: 400,
        contentType: "application/json",
        body: JSON.stringify({
          success: false,
          error: "Workspace name already exists",
        }),
      })
      return
    }

    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: { name: "new-workspace", path: "/tmp/new" },
      }),
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
        data: { open: 1, closed: 0, total: 1, completion: 0 },
      }),
    })
  })
}

async function navigateToApp(page: Page) {
  await page.goto("/")
  await page.waitForTimeout(1000)
}

async function openCreateWorkspaceModal(page: Page) {
  const newWsBtn = page.getByText("+ New Workspace")
  await expect(newWsBtn).toBeVisible({ timeout: 10000 })
  await newWsBtn.click()

  const dialog = page.getByRole("dialog")
  await expect(dialog).toBeVisible()
  return dialog
}

test.describe("CreateWorkspaceModal", () => {
  test.describe("Modal Open/Close", () => {
    test('clicking New Workspace button opens the modal', async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)

      const dialog = await openCreateWorkspaceModal(page)
      await expect(dialog).toBeVisible()
    })

    test("modal renders with dialog role and aria-modal", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)

      await openCreateWorkspaceModal(page)

      const dialog = page.getByRole("dialog")
      await expect(dialog).toHaveAttribute("aria-modal", "true")
    })

    test('modal title shows Create Workspace', async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)

      await openCreateWorkspaceModal(page)
      await expect(page.getByText("Create Workspace")).toBeVisible()
    })

    test("Cancel button closes modal", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)

      await openCreateWorkspaceModal(page)
      await page.getByRole("button", { name: /cancel/i }).click()
      await expect(page.getByRole("dialog")).not.toBeVisible()
    })

    test("Escape key closes modal", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)

      await openCreateWorkspaceModal(page)
      await page.keyboard.press("Escape")
      await expect(page.getByRole("dialog")).not.toBeVisible()
    })
  })

  test.describe("Form Fields Display", () => {
    test("Name input is visible with placeholder", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)

      await openCreateWorkspaceModal(page)

      const nameInput = page.locator('input[placeholder="my-workspace"]')
      await expect(nameInput).toBeVisible()
    })

    test("Clone type is selected by default", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)

      await openCreateWorkspaceModal(page)

      const cloneRadio = page.getByLabel("Clone")
      await expect(cloneRadio).toBeChecked()
    })

    test("switching to Local Repos shows repo path field", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)

      await openCreateWorkspaceModal(page)

      const localRadio = page.getByLabel("Local Repos")
      await localRadio.click()

      await expect(
        page.getByText("Repository Paths")
      ).toBeVisible()
    })
  })

  test.describe("Multi-Value Clone URL Input", () => {
    test("typing URL and clicking Add creates a chip", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)

      await openCreateWorkspaceModal(page)

      const urlInput = page.getByTestId("clone-url-input")
      await urlInput.fill("https://github.com/test/repo.git")

      const addBtn = page.getByTestId("clone-url-add")
      await addBtn.click()

      await expect(
        page.getByText("https://github.com/test/repo.git")
      ).toBeVisible()
    })

    test("pressing Enter in URL field adds chip", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)

      await openCreateWorkspaceModal(page)

      const urlInput = page.getByTestId("clone-url-input")
      await urlInput.fill("https://github.com/test/repo2.git")
      await urlInput.press("Enter")

      await expect(
        page.getByText("https://github.com/test/repo2.git")
      ).toBeVisible()
    })

    test("clicking remove button on chip removes it", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)

      await openCreateWorkspaceModal(page)

      const urlInput = page.getByTestId("clone-url-input")
      await urlInput.fill("https://github.com/test/repo.git")
      await urlInput.press("Enter")

      await expect(
        page.getByText("https://github.com/test/repo.git")
      ).toBeVisible()

      const removeBtn = page.getByTestId("clone-url-remove-0")
      await removeBtn.click()

      await expect(
        page.getByText("https://github.com/test/repo.git")
      ).not.toBeVisible()
    })
  })

  test.describe("Submit Button Validation", () => {
    test("Submit disabled when name is empty", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)

      await openCreateWorkspaceModal(page)

      const submitBtn = page.getByRole("button", {
        name: /create workspace/i,
      })
      await expect(submitBtn).toBeDisabled()
    })

    test("Submit enabled when name and URL filled", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)

      await openCreateWorkspaceModal(page)

      const nameInput = page.locator('input[placeholder="my-workspace"]')
      await nameInput.fill("test-workspace")

      const urlInput = page.getByTestId("clone-url-input")
      await urlInput.fill("https://github.com/test/repo.git")
      await urlInput.press("Enter")

      const submitBtn = page.getByRole("button", {
        name: /create workspace/i,
      })
      await expect(submitBtn).toBeEnabled()
    })
  })

  test.describe("Form Submission - Clone Type", () => {
    test("submitting clone form calls create API with correct body", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)

      await openCreateWorkspaceModal(page)

      const nameInput = page.locator('input[placeholder="my-workspace"]')
      await nameInput.fill("my-new-ws")

      const urlInput = page.getByTestId("clone-url-input")
      await urlInput.fill("https://github.com/test/repo.git")
      await urlInput.press("Enter")

      const submitBtn = page.getByRole("button", {
        name: /create workspace/i,
      })
      await submitBtn.click()

      await expect(page.getByRole("dialog")).not.toBeVisible({
        timeout: 10000,
      })

      expect(createCalls.length).toBe(1)
      const body = JSON.parse(createCalls[0].body)
      expect(body.name).toBe("my-new-ws")
      expect(body.type).toBe("clone")
      expect(body.clone_urls).toContain("https://github.com/test/repo.git")
    })
  })

  test.describe("Loading State", () => {
    test('Submit button shows Creating... during submission', async ({
      page,
    }) => {
      await setupMocks(page, { createDelay: 2000 })
      await navigateToApp(page)

      await openCreateWorkspaceModal(page)

      const nameInput = page.locator('input[placeholder="my-workspace"]')
      await nameInput.fill("slow-create")

      const urlInput = page.getByTestId("clone-url-input")
      await urlInput.fill("https://github.com/test/repo.git")
      await urlInput.press("Enter")

      const submitBtn = page.getByRole("button", {
        name: /create workspace/i,
      })
      await submitBtn.click()

      await expect(page.getByText("Creating...")).toBeVisible()
    })
  })

  test.describe("Error Handling", () => {
    test("API error displays error message", async ({ page }) => {
      await setupMocks(page, { createError: true })
      await navigateToApp(page)

      await openCreateWorkspaceModal(page)

      const nameInput = page.locator('input[placeholder="my-workspace"]')
      await nameInput.fill("existing-name")

      const urlInput = page.getByTestId("clone-url-input")
      await urlInput.fill("https://github.com/test/repo.git")
      await urlInput.press("Enter")

      const submitBtn = page.getByRole("button", {
        name: /create workspace/i,
      })
      await submitBtn.click()

      await expect(
        page.getByText("Workspace name already exists")
      ).toBeVisible({ timeout: 10000 })

      await expect(page.getByRole("dialog")).toBeVisible()
    })
  })

  test.describe("Form Reset", () => {
    test("closing and reopening modal resets form fields", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateToApp(page)

      await openCreateWorkspaceModal(page)

      const nameInput = page.locator('input[placeholder="my-workspace"]')
      await nameInput.fill("test-value")

      await page.getByRole("button", { name: /cancel/i }).click()
      await expect(page.getByRole("dialog")).not.toBeVisible()

      await openCreateWorkspaceModal(page)

      const nameInputReopened = page.locator(
        'input[placeholder="my-workspace"]'
      )
      await expect(nameInputReopened).toHaveValue("")
    })
  })

  test.describe("Accessibility", () => {
    test("Name input auto-focuses when modal opens", async ({ page }) => {
      await setupMocks(page)
      await navigateToApp(page)

      await openCreateWorkspaceModal(page)

      const nameInput = page.locator('input[placeholder="my-workspace"]')
      await expect(nameInput).toBeFocused()
    })
  })
})
