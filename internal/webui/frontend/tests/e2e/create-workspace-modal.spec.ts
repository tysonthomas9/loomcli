import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

/**
 * E2E tests for CreateWorkspaceModal component.
 *
 * Tests the full workspace creation flow: opening the modal via the
 * WorkspaceTree "+ New Workspace" button, interacting with form fields
 * (onboarding defaults and multi-value URL chips), submitting to the mocked
 * API, and verifying success/error behavior.
 */

// -- Mock data --

const mockWorkspaceData = {
  id: "default",
  name: "default",
  path: "/tmp/test-ws",
  repos: [],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: "default",
      name: "default",
      path: "/tmp/test-ws",
      active: true,
      repo_count: 0,
      is_default: true,
    },
  ],
  workspace_order: ["default"],
  default_workspace: "default",
};

const createdWorkspaceData = {
  ...mockWorkspaceData,
  workspaces: [
    ...mockWorkspaceData.workspaces,
    {
      id: "new-ws",
      name: "test-workspace",
      path: "/tmp/test-workspace",
      active: false,
      repo_count: 0,
      is_default: false,
    },
  ],
};

// At least one issue is needed so App.tsx takes the success render path
// (the empty-state branch doesn't render CreateWorkspaceModal).
const mockIssue = {
  id: "ws-modal-test-1",
  title: "Placeholder Issue",
  status: "open",
  priority: 2,
  issue_type: "task",
  created_at: "2026-01-15T10:00:00Z",
  updated_at: "2026-01-15T10:00:00Z",
};

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

// -- Setup --

async function setupMocks(
  page: Page,
  options?: {
    postDelay?: number;
    postError?: boolean;
    postErrorMessage?: string;
    onPost?: (body: Record<string, unknown>) => void;
  },
) {
  const postCalls: { body: Record<string, unknown> }[] = [];

  await page.route("**/api/config", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname !== "/api/config") {
      await route.fallback();
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ mode: "open" }),
    });
  });

  // Workspace resolution
  await page.route("**/api/workspaces/active", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok(mockWorkspaceData),
    });
  });

  await page.route("**/api/workspaces/default", async (route) => {
    const url = new URL(route.request().url());
    if (
      url.pathname === "/api/workspaces/default" ||
      url.pathname === "/api/workspaces/default/"
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok(mockWorkspaceData),
      });
    } else {
      await route.fallback();
    }
  });

  // POST /api/workspaces (workspace creation)
  await page.route("**/api/workspaces", async (route) => {
    if (route.request().method() === "POST") {
      if (options?.postDelay) {
        await new Promise((r) => setTimeout(r, options.postDelay));
      }

      const body = route.request().postDataJSON();
      postCalls.push({ body });
      options?.onPost?.(body);

      if (options?.postError) {
        await route.fulfill({
          status: 400,
          contentType: "application/json",
          body: JSON.stringify({
            success: false,
            error: options.postErrorMessage ?? "Failed to create workspace",
          }),
        });
        return;
      }

      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok(createdWorkspaceData),
      });
    } else {
      await route.fallback();
    }
  });

  // Health & auth
  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    });
  });

  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token-e2e" }),
    });
  });

  // Health endpoint
  await page.route("**/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok" }),
    });
  });

  // Monitor agent endpoints
  await page.route("**/api/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ agents: [], tasks: [] }),
    });
  });

  // Workspace-scoped endpoints
  await page.route("**/workspaces/*/stats", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        total_issues: 0,
        open_issues: 0,
        in_progress_issues: 0,
        closed_issues: 0,
        blocked_issues: 0,
        deferred_issues: 0,
        ready_issues: 0,
        tombstone_issues: 0,
        pinned_issues: 0,
        epics_eligible_for_closure: 0,
        average_lead_time_hours: 0,
      }),
    });
  });

  await page.route("**/workspaces/*/blocked*", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok([]),
    });
  });

  await page.route(
    "**/workspaces/*/terminal/sessions/by-issue",
    async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok({}),
      });
    },
  );

  await page.route("**/workspaces/*/terminal/tabs", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok([]),
    });
  });

  await page.route("**/workspaces/*/terminal/state", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ active_tab: "" }),
    });
  });

  await page.route("**/workspaces/*/terminal/sessions", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({}),
    });
  });

  await page.route("**/workspaces/*/issues/graph", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok([]),
    });
  });

  // SSE events
  await page.route("**/workspaces/*/events**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      headers: { "Cache-Control": "no-cache", Connection: "keep-alive" },
      body: 'event: connected\ndata: {"message":"connected"}\n\n',
    });
  });

  // Issues list — use addInitScript to survive React StrictMode abort.
  // The empty-state branch in App.tsx doesn't render CreateWorkspaceModal,
  // so we need at least one issue for the success render path.
  await page.addInitScript((issue: typeof mockIssue) => {
    const originalFetch = window.fetch.bind(window);
    window.fetch = function (
      input: RequestInfo | URL,
      init?: RequestInit,
    ): Promise<Response> {
      const url = typeof input === "string" ? input : input.toString();
      const method = init?.method ?? "GET";
      if (
        /\/api\/workspaces\/[^/]+\/issues(\?|$)/.test(url) &&
        method === "GET"
      ) {
        return Promise.resolve(
          new Response(JSON.stringify({ success: true, data: [issue] }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        );
      }
      return originalFetch(input, init);
    };
  }, mockIssue);

  return { postCalls };
}

async function navigateToApp(page: Page) {
  await page.goto("/ws/default/", { waitUntil: "domcontentloaded" });
}

async function openCreateWorkspaceModal(page: Page) {
  await page
    .getByRole("button", { name: /Active workspace: default/i })
    .click();

  const newWSButton = page.getByRole("button", { name: "+ New Workspace" });
  await expect(newWSButton).toBeVisible({ timeout: 10_000 });
  await newWSButton.click();

  const overlay = page.getByTestId("create-workspace-overlay");
  await expect(overlay).toBeVisible({ timeout: 5_000 });
}

/** Wait for POST /api/workspaces response. */
function waitForCreatePost(page: Page) {
  return page.waitForResponse(
    (res) =>
      res.url().endsWith("/api/workspaces") &&
      res.request().method() === "POST",
  );
}

// -- Tests --

test.describe("CreateWorkspaceModal", () => {
  test.describe("Modal Open/Close", () => {
    test("clicking '+ New Workspace' opens the modal", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      const dialog = page.getByRole("dialog", { name: "New Workspace" });
      await expect(dialog).toBeVisible();
    });

    test("modal has dialog role, aria-modal, and aria-label", async ({
      page,
    }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      const dialog = page.getByRole("dialog", { name: "New Workspace" });
      await expect(dialog).toHaveAttribute("aria-modal", "true");
      await expect(dialog).toHaveAttribute("aria-label", "New Workspace");
    });

    test("modal title shows 'New Workspace'", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      await expect(
        page.locator("h2").filter({ hasText: "New Workspace" }),
      ).toBeVisible();
    });

    test("Cancel button closes modal", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      await page.getByTestId("create-workspace-cancel").click();
      await expect(
        page.getByTestId("create-workspace-overlay"),
      ).not.toBeVisible();
    });

    test("clicking overlay closes modal", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      // Click top-left corner of overlay (outside the centered dialog)
      const overlay = page.getByTestId("create-workspace-overlay");
      await overlay.click({ position: { x: 10, y: 10 } });

      await expect(overlay).not.toBeVisible();
    });

    test("clicking inside dialog does NOT close modal", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      const dialog = page.getByRole("dialog", { name: "New Workspace" });
      await dialog.click();

      await expect(page.getByTestId("create-workspace-overlay")).toBeVisible();
    });

    test("Escape key closes modal", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      await page.keyboard.press("Escape");

      await expect(
        page.getByTestId("create-workspace-overlay"),
      ).not.toBeVisible();
    });
  });

  test.describe("Form Fields Display", () => {
    test("Name input visible with correct placeholder", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      const nameInput = page.getByTestId("create-workspace-name");
      await expect(nameInput).toBeVisible();
      await expect(nameInput).toHaveAttribute("placeholder", "my-workspace");
    });

    test("opens with the onboarding repository defaults", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      await expect(page.getByTestId("create-workspace-name")).toHaveValue(
        "Hello-World",
      );
      await expect(page.getByTestId("create-workspace-clone-url")).toHaveValue(
        "https://github.com/octocat/Hello-World",
      );
      await expect(page.getByTestId("create-workspace-submit")).toBeEnabled();
    });

    test("shows the clone-only Repository URL field", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      await expect(
        page.getByTestId("create-workspace-clone-url"),
      ).toBeVisible();
      await expect(page.getByTestId("create-workspace-repo-path")).toHaveCount(
        0,
      );
      await expect(page.getByTestId("create-workspace-path")).toHaveCount(0);
    });
  });

  test.describe("Multi-Value Clone URL Input", () => {
    test("typing URL and clicking Add creates a chip", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      const urlInput = page.getByTestId("create-workspace-clone-url");
      await urlInput.fill("https://github.com/test/repo.git");

      // Click the Add button next to the clone URL input
      const addRow = urlInput.locator("..");
      await addRow.locator("button", { hasText: "Add" }).click();

      // Chip appears with URL text
      await expect(
        page.locator('[class*="chipText"]', {
          hasText: "https://github.com/test/repo.git",
        }),
      ).toBeVisible();

      // Input cleared
      await expect(urlInput).toHaveValue("");
    });

    test("pressing Enter in URL field adds chip", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      const urlInput = page.getByTestId("create-workspace-clone-url");
      await urlInput.fill("https://github.com/test/repo2.git");
      await urlInput.press("Enter");

      await expect(
        page.locator('[class*="chipText"]', {
          hasText: "https://github.com/test/repo2.git",
        }),
      ).toBeVisible();
    });

    test("chip has remove button that removes it", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      const urlInput = page.getByTestId("create-workspace-clone-url");
      await urlInput.fill("https://github.com/test/repo.git");
      await urlInput.press("Enter");

      // Remove button exists with aria-label
      const removeBtn = page.locator(
        'button[aria-label="Remove https://github.com/test/repo.git"]',
      );
      await expect(removeBtn).toBeVisible();
      await removeBtn.click();

      await expect(
        page.locator('[class*="chipText"]', {
          hasText: "https://github.com/test/repo.git",
        }),
      ).not.toBeVisible();
    });

    test("duplicate URLs are not added", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      const urlInput = page.getByTestId("create-workspace-clone-url");
      await urlInput.fill("https://github.com/test/repo.git");
      await urlInput.press("Enter");
      await urlInput.fill("https://github.com/test/repo.git");
      await urlInput.press("Enter");

      await expect(page.locator('[class*="chipText"]')).toHaveCount(1);
    });

    test("Add button disabled when URL input is empty", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      const urlInput = page.getByTestId("create-workspace-clone-url");
      await urlInput.fill("");
      const addButton = urlInput
        .locator("..")
        .locator("button", { hasText: "Add" });
      await expect(addButton).toBeDisabled();
    });
  });

  test.describe("Submit Button Validation", () => {
    test("submit disabled when name is empty", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      await page.getByTestId("create-workspace-name").fill("");

      await expect(page.getByTestId("create-workspace-submit")).toBeDisabled();
    });

    test("submit disabled when clone type and no URLs added", async ({
      page,
    }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      await page.getByTestId("create-workspace-name").fill("test-workspace");
      await page.getByTestId("create-workspace-clone-url").fill("");

      await expect(page.getByTestId("create-workspace-submit")).toBeDisabled();
    });

    test("submit enabled with name + pending URL text", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      await page.getByTestId("create-workspace-name").fill("test-workspace");
      await page
        .getByTestId("create-workspace-clone-url")
        .fill("https://github.com/test/repo.git");

      await expect(page.getByTestId("create-workspace-submit")).toBeEnabled();
    });

    test("submit enabled when name filled and URLs added", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      await page.getByTestId("create-workspace-name").fill("test-workspace");
      const urlInput = page.getByTestId("create-workspace-clone-url");
      await urlInput.fill("https://github.com/test/repo.git");
      await urlInput.press("Enter");

      await expect(page.getByTestId("create-workspace-submit")).toBeEnabled();
    });
  });

  test.describe("Form Submission - Clone Type", () => {
    test("submits with correct body", async ({ page }) => {
      const { postCalls } = await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      await page.getByTestId("create-workspace-name").fill("test-workspace");
      const urlInput = page.getByTestId("create-workspace-clone-url");
      await urlInput.fill("https://github.com/test/repo.git");
      await urlInput.press("Enter");

      const postPromise = waitForCreatePost(page);
      await page.getByTestId("create-workspace-submit").click();
      await postPromise;

      expect(postCalls).toHaveLength(1);
      expect(postCalls[0].body).toEqual({
        name: "test-workspace",
        type: "clone",
        clone_urls: ["https://github.com/test/repo.git"],
      });
    });

    test("modal closes on success", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      await page.getByTestId("create-workspace-name").fill("test-workspace");
      const urlInput = page.getByTestId("create-workspace-clone-url");
      await urlInput.fill("https://github.com/test/repo.git");
      await urlInput.press("Enter");

      const postPromise = waitForCreatePost(page);
      await page.getByTestId("create-workspace-submit").click();
      await postPromise;

      await expect(
        page.getByTestId("create-workspace-overlay"),
      ).not.toBeVisible();
    });

    test("pending URL auto-added on submit", async ({ page }) => {
      const { postCalls } = await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      await page.getByTestId("create-workspace-name").fill("test-workspace");
      // Type URL but do NOT click Add or press Enter
      await page
        .getByTestId("create-workspace-clone-url")
        .fill("https://github.com/test/pending.git");

      const postPromise = waitForCreatePost(page);
      await page.getByTestId("create-workspace-submit").click();
      await postPromise;

      expect(postCalls[0].body).toEqual({
        name: "test-workspace",
        type: "clone",
        clone_urls: ["https://github.com/test/pending.git"],
      });
    });
  });

  test.describe("Loading State", () => {
    test("submit shows 'Creating...' and disables form during submission", async ({
      page,
    }) => {
      await setupMocks(page, { postDelay: 500 });
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      const nameInput = page.getByTestId("create-workspace-name");
      await nameInput.fill("test-workspace");

      const urlInput = page.getByTestId("create-workspace-clone-url");
      await urlInput.fill("https://github.com/test/repo.git");
      await urlInput.press("Enter");

      const postPromise = waitForCreatePost(page);
      const submitButton = page.getByTestId("create-workspace-submit");
      await submitButton.click();

      // During submission
      await expect(submitButton).toContainText("Creating...");
      await expect(submitButton).toBeDisabled();
      await expect(nameInput).toBeDisabled();

      await postPromise;
    });
  });

  test.describe("Error Handling", () => {
    test("API error shows error message", async ({ page }) => {
      await setupMocks(page, {
        postError: true,
        postErrorMessage: "Workspace name already exists",
      });
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      await page.getByTestId("create-workspace-name").fill("test-workspace");
      const urlInput = page.getByTestId("create-workspace-clone-url");
      await urlInput.fill("https://github.com/test/repo.git");
      await urlInput.press("Enter");

      const postPromise = waitForCreatePost(page);
      await page.getByTestId("create-workspace-submit").click();
      await postPromise;

      await expect(page.getByTestId("create-workspace-error")).toBeVisible();
    });

    test("form remains editable after error", async ({ page }) => {
      await setupMocks(page, { postError: true });
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      const nameInput = page.getByTestId("create-workspace-name");
      await nameInput.fill("test-workspace");
      const urlInput = page.getByTestId("create-workspace-clone-url");
      await urlInput.fill("https://github.com/test/repo.git");
      await urlInput.press("Enter");

      const postPromise = waitForCreatePost(page);
      await page.getByTestId("create-workspace-submit").click();
      await postPromise;

      // Error shown, form still editable, modal still open
      await expect(page.getByTestId("create-workspace-error")).toBeVisible();
      await expect(nameInput).toBeEnabled();
      await expect(page.getByTestId("create-workspace-submit")).toBeEnabled();
      await expect(page.getByTestId("create-workspace-overlay")).toBeVisible();
    });
  });

  test.describe("Form Reset", () => {
    test("close and reopen restores the onboarding defaults", async ({
      page,
    }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      // Fill fields and add a chip
      await page.getByTestId("create-workspace-name").fill("test-workspace");
      const urlInput = page.getByTestId("create-workspace-clone-url");
      await urlInput.fill("https://github.com/test/repo.git");
      await urlInput.press("Enter");

      // Close
      await page.getByTestId("create-workspace-cancel").click();
      await expect(
        page.getByTestId("create-workspace-overlay"),
      ).not.toBeVisible();

      // Reopen
      await openCreateWorkspaceModal(page);

      // Transient edits reset back to the values supplied by App.
      await expect(page.getByTestId("create-workspace-name")).toHaveValue(
        "Hello-World",
      );
      await expect(page.getByTestId("create-workspace-clone-url")).toHaveValue(
        "https://github.com/octocat/Hello-World",
      );
      await expect(page.locator('[class*="chipText"]')).toHaveCount(0);
    });
  });

  test.describe("Accessibility", () => {
    test("name input auto-focuses when modal opens", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      await expect(page.getByTestId("create-workspace-name")).toBeFocused();
    });

    test("form labels correctly associated with inputs", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openCreateWorkspaceModal(page);

      // Name label → ws-name input
      await expect(page.locator('label[for="ws-name"]')).toHaveText("Name");
      await expect(page.getByTestId("create-workspace-name")).toHaveAttribute(
        "id",
        "ws-name",
      );

      // Repository URL label → ws-clone-url input
      await expect(page.locator('label[for="ws-clone-url"]')).toHaveText(
        "Repository URL",
      );
      await expect(
        page.getByTestId("create-workspace-clone-url"),
      ).toHaveAttribute("id", "ws-clone-url");
    });
  });
});
