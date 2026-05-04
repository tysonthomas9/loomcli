/**
 * E2E Journey: Create issue, drag to column, assign agent.
 *
 * Tests the full issue creation and triage workflow:
 * 1. Load kanban, open CreateIssueModal, fill and submit
 * 2. Verify new card appears in Ready column after refetch
 * 3. Open IssueDetailPanel, edit description, add label, set assignee
 * 4. Drag card from Ready to In Progress, handle AssigneePrompt
 *
 * Uses workspace-scoped API mocking (same pattern as keyboard-setup bootApp).
 */

import { test, expect } from "../fixtures";
import type { Page } from "@playwright/test";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const WORKSPACE_ID = "journey-ws";

const WORKSPACE_DATA = {
  id: WORKSPACE_ID,
  name: "Journey Test Workspace",
  path: "/tmp/journey-ws",
  repos: [],
  groups: [],
  agents: [
    { name: "ember", status: "ready" },
    { name: "drift", status: "working" },
  ],
  workspaces: [
    {
      id: WORKSPACE_ID,
      name: "Journey Test Workspace",
      path: "/tmp/journey-ws",
      active: true,
      repo_count: 1,
      is_default: true,
    },
  ],
  default_workspace: WORKSPACE_ID,
};

const WS_PREFIX = `/api/workspaces/${WORKSPACE_ID}`;

// Existing issues on the board
const EXISTING_ISSUES = [
  {
    id: "existing-001",
    title: "Existing Open Task",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
  {
    id: "existing-002",
    title: "In Progress Task",
    status: "in_progress",
    priority: 1,
    issue_type: "feature",
    assignee: "drift",
    created_at: "2026-01-24T11:00:00Z",
    updated_at: "2026-01-24T11:00:00Z",
  },
  {
    id: "existing-003",
    title: "Review Task",
    status: "review",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-24T12:00:00Z",
    updated_at: "2026-01-24T12:00:00Z",
  },
];

// The newly created issue
const NEW_ISSUE = {
  id: "journey-new-001",
  title: "Journey Test Bug",
  status: "open",
  priority: 1,
  issue_type: "bug",
  created_at: "2026-03-28T01:00:00Z",
  updated_at: "2026-03-28T01:00:00Z",
};

// Issue details (for detail panel GET)
const NEW_ISSUE_DETAILS = {
  ...NEW_ISSUE,
  description: "",
  labels: [],
  dependencies: [],
  dependents: [],
  comments: [],
  events: [],
};

const MOCK_STATS = {
  total_issues: 4,
  open_issues: 2,
  in_progress_issues: 1,
  closed_issues: 0,
  blocked_issues: 0,
  deferred_issues: 0,
  ready_issues: 2,
  tombstone_issues: 0,
  pinned_issues: 0,
  epics_eligible_for_closure: 0,
  average_lead_time_hours: 24,
};

// ---------------------------------------------------------------------------
// Mock setup
// ---------------------------------------------------------------------------

interface MockIssue {
  id: string;
  title: string;
  status: string;
  priority: number;
  issue_type: string;
  created_at: string;
  updated_at: string;
  assignee?: string;
  labels?: string[];
  [key: string]: unknown;
}

interface MockState {
  issues: MockIssue[];
  issueDetails: Record<string, Record<string, unknown>>;
  postCalls: Array<{ url: string; body: unknown }>;
  patchCalls: Array<{ url: string; body: unknown }>;
}

async function setupMocks(page: Page, state: MockState): Promise<void> {
  // Neutralize AbortController signals in fetch to prevent React StrictMode
  // from aborting in-flight API requests (effects fire twice in dev mode).
  // This is a standard Playwright workaround — the signal is stripped so
  // route.fulfill() responses are always received by the browser.
  await page.addInitScript(() => {
    const origFetch = window.fetch;
    window.fetch = function (input: RequestInfo | URL, init?: RequestInit) {
      if (init?.signal) {
        const { signal: _signal, ...rest } = init;
        return origFetch.call(this, input, rest);
      }
      return origFetch.call(this, input, init);
    };
  });

  // App config (auth mode discovery)
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

  await page.route("**/api/backends", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: [{ name: "claude", available: true, display_name: "Claude" }],
      }),
    });
  });

  await page.route("**/api/workspaces/*/config/backend", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: {
          backend: "claude",
          source: "workspace",
          available: ["claude"],
          agents: [],
        },
      }),
    });
  });

  // Auth token
  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token-journey" }),
    });
  });

  // Daemon health
  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        status: "ok",
        daemon: { connected: true, status: "running", uptime: 1000, version: "test" },
      }),
    });
  });

  // Workspace-scoped API endpoints
  await page.route(
    (url) => {
      const s = url.toString();
      return s.includes("/api/workspaces/") && !s.includes("/src/");
    },
    async (route) => {
      const url = route.request().url();
      const method = route.request().method();

      // Workspace resolution: /api/workspaces/active
      if (url.includes("/api/workspaces/active")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: WORKSPACE_DATA }),
        });
        return;
      }

      // SSE events: abort to prevent networkidle timeout
      if (url.includes(WS_PREFIX + "/events")) {
        await route.abort();
        return;
      }

      if (url.includes(WS_PREFIX + "/config/backend")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            success: true,
            data: {
              backend: "claude",
              source: "workspace",
              available: ["claude"],
              agents: [],
            },
          }),
        });
        return;
      }

      // POST /api/workspaces/{ws}/issues — create issue
      if (url.includes(WS_PREFIX + "/issues") && method === "POST" && !url.includes("/comments")) {
        const body = route.request().postDataJSON();
        state.postCalls.push({ url, body });
        await route.fulfill({
          status: 201,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: NEW_ISSUE }),
        });
        return;
      }

      // PATCH /api/workspaces/{ws}/issues/{id}
      if (url.includes(WS_PREFIX + "/issues/") && method === "PATCH") {
        const body = route.request().postDataJSON();
        state.patchCalls.push({ url, body });

        // Find the issue being updated and return merged data
        const issueId = url.split("/issues/")[1]?.split("?")[0]?.split("/")[0];
        const existing = state.issues.find((i) => i.id === issueId) ?? NEW_ISSUE;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            success: true,
            data: { ...existing, ...body, updated_at: new Date().toISOString() },
          }),
        });
        return;
      }

      // GET /api/workspaces/{ws}/issues/{id}/... — issue sub-resources (events, tabs, comments, etc.)
      // Must be checked BEFORE the generic issue detail handler to avoid
      // returning issue details when a sub-resource is requested.
      if (url.includes(WS_PREFIX + "/issues/") && method === "GET" && !url.includes("/graph")) {
        const afterIssues = url.split(WS_PREFIX + "/issues/")[1] ?? "";
        const pathParts = afterIssues.split("?")[0].split("/");
        if (pathParts.length > 1 && pathParts[1]) {
          // /tabs returns null (no saved tab state), everything else returns []
          const subResource = pathParts[1];
          const data = subResource === "tabs" ? null : [];
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ success: true, data }),
          });
          return;
        }
      }

      // GET /api/workspaces/{ws}/issues/{id} — issue details (no sub-path)
      if (url.includes(WS_PREFIX + "/issues/") && method === "GET" && !url.includes("/graph")) {
        const issueId = url.split("/issues/")[1]?.split("?")[0]?.split("/")[0];
        if (issueId && state.issueDetails[issueId]) {
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ success: true, data: state.issueDetails[issueId] }),
          });
          return;
        }
        // Fallback: construct from issues list
        const issue = state.issues.find((i) => i.id === issueId);
        if (issue) {
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({
              success: true,
              data: { ...issue, labels: [], dependencies: [], dependents: [], comments: [], events: [] },
            }),
          });
          return;
        }
      }

      // Issues graph
      if (url.includes(WS_PREFIX + "/issues/graph")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: state.issues }),
        });
        return;
      }

      // Issues list / ready endpoint
      if (url.includes(WS_PREFIX + "/ready") || (url.includes(WS_PREFIX + "/issues") && method === "GET")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: state.issues }),
        });
        return;
      }

      // Blocked issues
      if (url.includes(WS_PREFIX + "/blocked")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: [] }),
        });
        return;
      }

      // Stats
      if (url.includes(WS_PREFIX + "/stats")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: MOCK_STATS }),
        });
        return;
      }

      if (url.includes(WS_PREFIX + "/terminal/tabs")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: [] }),
        });
        return;
      }

      if (url.includes(WS_PREFIX + "/terminal/state")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ active_tab: "" }),
        });
        return;
      }

      if (url.includes(WS_PREFIX + "/terminal/sessions/by-issue")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: {} }),
        });
        return;
      }

      // Workspace validation (exact workspace path)
      if (url.includes(WS_PREFIX)) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: WORKSPACE_DATA }),
        });
        return;
      }

      // Fallback
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: [] }),
      });
    },
  );

  // Monitor server endpoints (global)
  await page.route("**/api/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ agents: [], tasks: [], stats: {} }),
    });
  });
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

test.describe("Journey: Create issue, drag to column, assign agent", () => {
  test.describe.configure({ mode: "serial" });

  test("1. Load kanban and open create modal", async ({ page }) => {
    const state: MockState = {
      issues: [...EXISTING_ISSUES],
      issueDetails: {},
      postCalls: [],
      patchCalls: [],
    };

    await setupMocks(page, state);
    await page.goto(`/ws/${WORKSPACE_ID}/kanban`);
    await page.waitForSelector('section[data-status="ready"]', { timeout: 15000 });

    // Verify kanban columns are visible
    const readyColumn = page.locator('section[data-status="ready"]');
    const inProgressColumn = page.locator('section[data-status="in_progress"]');
    await expect(readyColumn).toBeVisible();
    await expect(inProgressColumn).toBeVisible();

    // Verify existing cards render
    await expect(readyColumn.getByText("Existing Open Task")).toBeVisible();
    await expect(inProgressColumn.getByText("In Progress Task")).toBeVisible();

    // Open CreateIssueModal
    await page.getByTestId("new-issue-button").click();
    const modal = page.getByRole("dialog", { name: "Create Issue" });
    await expect(modal).toBeVisible();

    // Verify focus is inside modal (title input is auto-focused)
    await expect(page.getByTestId("create-issue-title")).toBeFocused();

    // Verify Escape closes modal
    await page.keyboard.press("Escape");
    await expect(modal).not.toBeVisible();
  });

  test("2. Fill form and submit — card appears in Ready column", async ({ page }) => {
    const state: MockState = {
      issues: [...EXISTING_ISSUES],
      issueDetails: {},
      postCalls: [],
      patchCalls: [],
    };

    await setupMocks(page, state);

    // After create, refetch will be called — update state to include new issue
    // We intercept POST, then on subsequent ready calls include the new issue
    let created = false;

    // Override the workspace route to handle dynamic issues list
    await page.route(
      (url) => {
        const s = url.toString();
        return s.includes(WS_PREFIX + "/issues") && !s.includes("/graph") && !s.includes("/src/");
      },
      async (route) => {
        const method = route.request().method();
        const url = route.request().url();

        if (method === "POST" && !url.includes("/comments")) {
          const body = route.request().postDataJSON();
          state.postCalls.push({ url, body });
          created = true;
          await route.fulfill({
            status: 201,
            contentType: "application/json",
            body: JSON.stringify({ success: true, data: NEW_ISSUE }),
          });
          return;
        }

        if (method === "GET") {
          const issues = created ? [...EXISTING_ISSUES, NEW_ISSUE] : EXISTING_ISSUES;
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ success: true, data: issues }),
          });
          return;
        }

        await route.fallback();
      },
    );

    // Also override the ready endpoint to return new issue after create
    await page.route(
      (url) => url.toString().includes(WS_PREFIX + "/ready"),
      async (route) => {
        const issues = created ? [...EXISTING_ISSUES, NEW_ISSUE] : EXISTING_ISSUES;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: issues }),
        });
      },
    );

    await page.goto(`/ws/${WORKSPACE_ID}/kanban`);
    await page.waitForSelector('section[data-status="ready"]', { timeout: 15000 });

    const readyColumn = page.locator('section[data-status="ready"]');

    // Verify initial state: 1 card in Ready
    await expect(readyColumn.locator("article")).toHaveCount(1);

    // Open modal and fill form
    await page.getByTestId("new-issue-button").click();
    const modal = page.getByRole("dialog", { name: "Create Issue" });
    await expect(modal).toBeVisible();

    await page.getByTestId("create-issue-title").fill("Journey Test Bug");
    await page.getByTestId("create-issue-type").selectOption("bug");
    await page.getByTestId("create-issue-priority").selectOption("1");

    // Submit and wait for POST response
    const postPromise = page.waitForResponse(
      (res) =>
        res.url().includes(`${WS_PREFIX}/issues`) &&
        !res.url().includes("/comments") &&
        res.request().method() === "POST",
    );
    await page.getByTestId("create-issue-submit").click();
    await postPromise;

    // Modal should close
    await expect(modal).not.toBeVisible();

    // After refetch, new card should appear in Ready column
    await expect(readyColumn.getByText("Journey Test Bug")).toBeVisible({ timeout: 5000 });
    await expect(readyColumn.locator("article")).toHaveCount(2);

    // Verify POST payload
    expect(state.postCalls).toHaveLength(1);
    const postBody = state.postCalls[0].body as Record<string, unknown>;
    expect(postBody.title).toBe("Journey Test Bug");
    expect(postBody.issue_type).toBe("bug");
    expect(postBody.priority).toBe(1);
  });

  test("3. Open detail panel and edit description", async ({ page }) => {
    const state: MockState = {
      issues: [...EXISTING_ISSUES, NEW_ISSUE],
      issueDetails: {
        [NEW_ISSUE.id]: { ...NEW_ISSUE_DETAILS },
      },
      postCalls: [],
      patchCalls: [],
    };

    await setupMocks(page, state);
    await page.goto(`/ws/${WORKSPACE_ID}/kanban`);
    await page.waitForSelector('section[data-status="ready"]', { timeout: 15000 });

    const readyColumn = page.locator('section[data-status="ready"]');

    // Click the new issue card to open detail panel
    await readyColumn.getByText("Journey Test Bug").click();

    // Wait for detail panel content to load (editable-description appears when detail fetch completes)
    await expect(page.getByTestId("editable-description")).toBeVisible({ timeout: 5000 });

    // Click edit on EditableDescription
    await page.getByTestId("description-edit-button").click();
    const textarea = page.getByTestId("description-textarea");
    await expect(textarea).toBeVisible();

    // Type description and save with Ctrl+Enter
    await textarea.fill("This is a critical bug that needs immediate attention");

    // Set up response waiter BEFORE triggering save to avoid race condition
    const patchPromise = page.waitForResponse(
      (res) => res.url().includes(`/issues/${NEW_ISSUE.id}`) && res.request().method() === "PATCH",
    );
    await textarea.press("Control+Enter");
    await patchPromise;

    // Verify PATCH was called with description
    expect(state.patchCalls.length).toBeGreaterThanOrEqual(1);
    const descPatch = state.patchCalls.find(
      (c) => (c.body as Record<string, unknown>).description !== undefined,
    );
    expect(descPatch).toBeTruthy();
    expect((descPatch!.body as Record<string, unknown>).description).toBe(
      "This is a critical bug that needs immediate attention",
    );
  });

  test("4. Add label and select assignee", async ({ page }) => {
    const state: MockState = {
      issues: [...EXISTING_ISSUES, NEW_ISSUE],
      issueDetails: {
        [NEW_ISSUE.id]: {
          ...NEW_ISSUE_DETAILS,
          description: "This is a critical bug that needs immediate attention",
        },
      },
      postCalls: [],
      patchCalls: [],
    };

    await setupMocks(page, state);
    await page.goto(`/ws/${WORKSPACE_ID}/kanban`);
    await page.waitForSelector('section[data-status="ready"]', { timeout: 15000 });

    // Open detail panel for the issue
    const readyColumn = page.locator('section[data-status="ready"]');
    await readyColumn.getByText("Journey Test Bug").click();

    // Wait for panel content to load
    await expect(page.getByTestId("editable-description")).toBeVisible({ timeout: 5000 });

    // Add label via LabelEditor
    await page.getByTestId("add-label-button").click();
    const labelInput = page.getByTestId("label-input");
    await expect(labelInput).toBeVisible();
    await labelInput.fill("frontend");

    // Set up response waiter BEFORE triggering the action
    const labelPatchPromise = page.waitForResponse(
      (res) => res.url().includes(`/issues/${NEW_ISSUE.id}`) && res.request().method() === "PATCH",
    );
    await labelInput.press("Enter");
    await labelPatchPromise;

    const labelPatch = state.patchCalls.find(
      (c) => (c.body as Record<string, unknown>).add_labels !== undefined,
    );
    expect(labelPatch).toBeTruthy();
    expect((labelPatch!.body as Record<string, unknown>).add_labels).toEqual(["frontend"]);

    // Select assignee via AssigneeDropdown (scoped to the detail panel tabpanel)
    const detailTab = page.getByRole("tabpanel", { name: "Details" });
    await detailTab.getByTestId("assignee-dropdown-trigger").click();
    const assigneeInput = page.getByTestId("assignee-input");
    await expect(assigneeInput).toBeVisible();
    await assigneeInput.fill("ember");

    const assigneePatchPromise = page.waitForResponse(
      (res) => res.url().includes(`/issues/${NEW_ISSUE.id}`) && res.request().method() === "PATCH",
    );
    await page.getByTestId("assignee-submit").click();
    await assigneePatchPromise;

    const assigneePatch = state.patchCalls.find(
      (c) => (c.body as Record<string, unknown>).assignee !== undefined,
    );
    expect(assigneePatch).toBeTruthy();
    // Human assignees get [H] prefix
    expect((assigneePatch!.body as Record<string, unknown>).assignee).toBe("[H] ember");

    // Close panel
    await page.keyboard.press("Escape");
  });

  test("5. Drag card from Ready to In Progress with AssigneePrompt", async ({ page }) => {
    const state: MockState = {
      issues: [
        ...EXISTING_ISSUES,
        { ...NEW_ISSUE, assignee: "[H] ember", labels: ["frontend"] },
      ],
      issueDetails: {},
      postCalls: [],
      patchCalls: [],
    };

    await setupMocks(page, state);
    await page.goto(`/ws/${WORKSPACE_ID}/kanban`);
    await page.waitForSelector('section[data-status="ready"]', { timeout: 15000 });

    const readyColumn = page.locator('section[data-status="ready"]');
    const inProgressColumn = page.locator('section[data-status="in_progress"]');

    // Verify initial state
    await expect(readyColumn.getByText("Journey Test Bug")).toBeVisible();

    // Find the draggable wrapper for our card
    const draggableWrapper = page
      .getByRole("button", { name: "Issue: Journey Test Bug" })
      .first();
    await expect(draggableWrapper).toBeVisible();

    // Get drop target
    const dropTarget = inProgressColumn.locator('[data-droppable-id="in_progress"]');
    await expect(dropTarget).toBeVisible();

    // Get bounding boxes for drag coordinates
    const dragBox = await draggableWrapper.boundingBox();
    const dropBox = await dropTarget.boundingBox();
    if (!dragBox || !dropBox) throw new Error("Could not get element bounds");

    const startX = dragBox.x + dragBox.width / 2;
    const startY = dragBox.y + dragBox.height / 2;
    const endX = dropBox.x + dropBox.width / 2;
    const endY = dropBox.y + dropBox.height / 2;

    // Perform drag using @dnd-kit pointer events pattern
    await draggableWrapper.dispatchEvent("pointerdown", {
      clientX: startX,
      clientY: startY,
      button: 0,
      buttons: 1,
      pointerId: 1,
      pointerType: "mouse",
      isPrimary: true,
    });

    await page.waitForTimeout(50);

    // Move past activation threshold
    await page.dispatchEvent("body", "pointermove", {
      clientX: startX + 10,
      clientY: startY,
      button: 0,
      buttons: 1,
      pointerId: 1,
      pointerType: "mouse",
      isPrimary: true,
    });

    await page.waitForTimeout(50);

    // Move to target
    await page.dispatchEvent("body", "pointermove", {
      clientX: endX,
      clientY: endY,
      button: 0,
      buttons: 1,
      pointerId: 1,
      pointerType: "mouse",
      isPrimary: true,
    });

    await page.waitForTimeout(50);

    // Drop
    await page.dispatchEvent("body", "pointerup", {
      clientX: endX,
      clientY: endY,
      button: 0,
      buttons: 0,
      pointerId: 1,
      pointerType: "mouse",
      isPrimary: true,
    });

    // AssigneePrompt should appear (drag from open to in_progress)
    const promptModal = page.getByTestId("assignee-prompt-modal");
    await expect(promptModal).toBeVisible({ timeout: 5000 });

    // Wait for input to be ready (AssigneePrompt has 100ms focus activation delay)
    const nameInput = page.getByTestId("assignee-name-input");
    await expect(nameInput).toBeVisible();
    await expect(nameInput).toBeEditable();
    await nameInput.fill("ember");
    // Verify the input took effect (button becomes enabled when input is non-empty)
    await expect(page.getByTestId("assignee-confirm-button")).toBeEnabled();

    // Set up response waiter BEFORE triggering the action
    const dragPatchPromise = page.waitForResponse(
      (res) => res.url().includes(`/issues/${NEW_ISSUE.id}`) && res.request().method() === "PATCH",
    );
    await page.getByTestId("assignee-confirm-button").click();
    await dragPatchPromise;

    // Verify PATCH was called with status and assignee
    // Note: the assignee-confirm flow uses raw updateIssue (not optimistic update),
    // so the card stays in Ready until SSE mutation arrives. We verify the API contract.
    expect(state.patchCalls.length).toBeGreaterThanOrEqual(1);
    const dragPatch = state.patchCalls.find(
      (c) => (c.body as Record<string, unknown>).status === "in_progress",
    );
    expect(dragPatch).toBeTruthy();
    expect((dragPatch!.body as Record<string, unknown>).assignee).toBe("[H] ember");

    // Verify card appears in In Progress after refetch
    // Trigger refetch by updating mock state and reloading
    state.issues = state.issues.map((i) =>
      i.id === NEW_ISSUE.id ? { ...i, status: "in_progress" as const, assignee: "[H] ember" } : i,
    );
    await page.reload();
    await page.waitForSelector('section[data-status="in_progress"]', { timeout: 15000 });
    await expect(inProgressColumn.getByText("Journey Test Bug")).toBeVisible({ timeout: 5000 });
  });
});
