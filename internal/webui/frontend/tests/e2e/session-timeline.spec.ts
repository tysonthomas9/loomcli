import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

/**
 * E2E tests for Session Timeline interactions within the IssueDetailPanel Sessions tab.
 *
 * Tests verify that users can browse sessions, select them, view transcripts and diffs,
 * expand file lists, and see correct formatting of duration/tokens/cost — all using
 * mocked API responses (no real backend).
 */

// -- Workspace mock data --

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

// -- Issue mock data --

const testIssue = {
  id: "sess-test-001",
  title: "Session Timeline Test Issue",
  status: "in_progress",
  priority: 2,
  issue_type: "task",
  description: "Test issue for session timeline",
  created_at: "2026-01-27T10:00:00Z",
  updated_at: "2026-01-27T10:00:00Z",
};

const testIssueDetails = {
  ...testIssue,
  labels: [],
  dependencies: [],
  dependents: [],
  comments: [],
};

// -- Session mock data --

interface MockSession {
  id: string;
  agent_name: string;
  backend: string;
  model?: string;
  phase?: string;
  status: "running" | "completed" | "failed" | "aborted";
  started_at: string;
  ended_at?: string;
  duration_s?: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  estimated_cost_usd: number;
  exit_code: number;
  files_changed: number;
  lines_added: number;
  lines_removed: number;
  files_touched?: string[];
  attempt_num: number;
  has_transcript: boolean;
  has_diff: boolean;
  is_active: boolean;
}

function createMockSession(overrides: Partial<MockSession> = {}): MockSession {
  return {
    id: "sess-1",
    agent_name: "nova",
    backend: "claude",
    model: "claude-opus-4-6",
    phase: "implementation",
    status: "completed",
    started_at: "2026-03-28T01:00:00Z",
    ended_at: "2026-03-28T01:05:00Z",
    duration_s: 300,
    input_tokens: 5000,
    output_tokens: 3000,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    estimated_cost_usd: 1.5,
    exit_code: 0,
    files_changed: 3,
    lines_added: 50,
    lines_removed: 10,
    files_touched: ["src/api.ts", "src/types.ts", "src/utils.ts"],
    attempt_num: 1,
    has_transcript: true,
    has_diff: true,
    is_active: false,
    ...overrides,
  };
}

const mockSessions = [
  createMockSession({
    id: "sess-1",
    agent_name: "nova",
    status: "completed",
    phase: "implementation",
    model: "claude-opus-4-6",
    duration_s: 300,
    input_tokens: 5000,
    output_tokens: 3000,
    estimated_cost_usd: 1.5,
    exit_code: 0,
    files_changed: 3,
    lines_added: 50,
    lines_removed: 10,
    files_touched: ["src/api.ts", "src/types.ts", "src/utils.ts"],
    has_diff: true,
    started_at: "2026-03-28T01:00:00Z",
  }),
  createMockSession({
    id: "sess-2",
    agent_name: "drift",
    status: "failed",
    phase: "planning",
    model: "claude-sonnet-4-6",
    duration_s: 45,
    input_tokens: 300,
    output_tokens: 200,
    estimated_cost_usd: 0.005,
    exit_code: 1,
    files_changed: 0,
    lines_added: 0,
    lines_removed: 0,
    files_touched: undefined,
    has_diff: false,
    started_at: "2026-03-28T00:50:00Z",
  }),
  createMockSession({
    id: "sess-3",
    agent_name: "spark",
    status: "running",
    phase: undefined,
    model: undefined,
    duration_s: undefined,
    input_tokens: 8000,
    output_tokens: 2000,
    estimated_cost_usd: 0,
    exit_code: 0,
    files_changed: 0,
    lines_added: 0,
    lines_removed: 0,
    files_touched: undefined,
    has_diff: false,
    is_active: true,
    started_at: "2026-03-28T02:00:00Z",
    ended_at: undefined,
  }),
  createMockSession({
    id: "sess-4",
    agent_name: "echo",
    status: "aborted",
    phase: "implementation",
    duration_s: 120,
    input_tokens: 1500,
    output_tokens: 500,
    estimated_cost_usd: 0.35,
    exit_code: 2,
    files_changed: 1,
    lines_added: 5,
    lines_removed: 0,
    files_touched: ["src/config.ts"],
    has_diff: true,
    started_at: "2026-03-27T23:00:00Z",
  }),
];

// -- Transcript mock data --

const mockTranscriptSess1 = [
  {
    seq: 1,
    ts: "2026-03-28T01:00:10Z",
    role: "user",
    type: "text",
    content: "Implement the session timeline component",
  },
  {
    seq: 2,
    ts: "2026-03-28T01:00:30Z",
    role: "assistant",
    type: "text",
    content: "I'll start by reading the existing components.",
  },
  {
    seq: 3,
    ts: "2026-03-28T01:01:00Z",
    role: "assistant",
    type: "tool_use",
    tool_name: "Read",
    tool_input: '{"file_path": "src/api.ts"}',
  },
  {
    seq: 4,
    ts: "2026-03-28T01:02:00Z",
    role: "system",
    type: "text",
    content: "Tool execution completed successfully.",
  },
];

// -- Diff mock data --

const mockDiffSess1 = `--- a/src/api.ts
+++ b/src/api.ts
@@ -10,6 +10,12 @@
 export function getTaskSessions(taskId: string) {
+  // Added session caching
+  if (sessionCache.has(taskId)) {
+    return sessionCache.get(taskId);
+  }
   const resp = await get(\`/api/tasks/\${taskId}/sessions\`);
   return resp.data?.sessions ?? [];
 }`;

// -- Helper: JSON response --

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

// -- Mock setup --

async function setupBaseMocks(page: Page) {
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

  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    });
  });

  await page.route("**/api/loom/**", async (route) => {
    if (route.request().url().includes("/health")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ status: "ok" }),
      });
    } else {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ agents: [], tasks: [] }),
      });
    }
  });

  await page.route("**/workspaces/*/stats", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({
        total_issues: 1,
        open_issues: 0,
        in_progress_issues: 1,
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

  await page.route("**/workspaces/*/events**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      headers: {
        "Cache-Control": "no-cache",
        Connection: "keep-alive",
      },
      body: 'event: connected\ndata: {"message":"connected"}\n\n',
    });
  });
}

async function installIssuesMock(page: Page, issues: unknown[]) {
  await page.addInitScript((data: unknown[]) => {
    (window as any).__mockIssues = data;

    const originalFetch = window.fetch.bind(window);
    window.fetch = function (
      input: RequestInfo | URL,
      init?: RequestInit,
    ): Promise<Response> {
      const url = typeof input === "string" ? input : input.toString();
      if (
        /\/api\/workspaces\/[^/]+\/issues(\?|$)/.test(url) &&
        (init?.method ?? "GET") === "GET"
      ) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              success: true,
              data: (window as any).__mockIssues,
            }),
            {
              status: 200,
              headers: { "Content-Type": "application/json" },
            },
          ),
        );
      }
      return originalFetch(input, init);
    };
  }, issues);
}

interface SetupSessionMocksOptions {
  sessions?: typeof mockSessions;
  transcript?: typeof mockTranscriptSess1;
  diff?: string | null;
  sessionsDelay?: number;
  sessionsError?: boolean;
}

async function setupSessionMocks(
  page: Page,
  options: SetupSessionMocksOptions = {},
) {
  const sessions = options.sessions ?? mockSessions;

  // Mock GET /api/workspaces/default/issues/{id} for detail panel
  await page.route("**/workspaces/*/issues/sess-test-*", async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok(testIssueDetails),
      });
    } else {
      await route.fallback();
    }
  });

  // Mock GET /api/tasks/{taskId}/sessions
  // Playwright's * matches a single path segment, so this won't match sub-paths
  await page.route("**/api/tasks/*/sessions", async (route) => {
    if (options.sessionsDelay) {
      await new Promise((r) => setTimeout(r, options.sessionsDelay));
    }
    if (options.sessionsError) {
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ error: "Internal server error" }),
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({ task_id: "sess-test-001", sessions }),
    });
  });

  // Mock GET /api/tasks/{taskId}/sessions/{sessionId}/transcript
  await page.route("**/api/tasks/*/sessions/*/transcript", async (route) => {
    const url = route.request().url();
    const sessionIdMatch = url.match(/\/sessions\/([^/]+)\/transcript/);
    const sessionId = sessionIdMatch ? sessionIdMatch[1] : null;

    let entries = options.transcript ?? mockTranscriptSess1;
    // Only return transcript for sess-1 by default; empty for others
    if (sessionId !== "sess-1" && !options.transcript) {
      entries = [];
    }

    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({ session_id: sessionId, entries }),
    });
  });

  // Mock GET /api/tasks/{taskId}/sessions/{sessionId}/diff
  await page.route("**/api/tasks/*/sessions/*/diff", async (route) => {
    const diffContent =
      options.diff !== undefined ? options.diff : mockDiffSess1;
    if (diffContent === null) {
      await route.fulfill({ status: 404 });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "text/plain",
      body: diffContent,
    });
  });
}

async function navigateAndWaitForBoard(page: Page) {
  await page.goto("/ws/default/", { waitUntil: "domcontentloaded" });
}

async function openIssuePanelAndSwitchToSessions(page: Page) {
  const card = page
    .locator("article")
    .filter({ hasText: "Session Timeline Test Issue" });
  await expect(card).toBeVisible();
  await card.click();

  const panel = page.getByTestId("issue-detail-panel");
  await expect(panel).toHaveAttribute("data-state", "open");
  await expect(panel).toHaveAttribute("data-loading", "false", {
    timeout: 5000,
  });

  // Click Sessions tab
  const sessionsTab = page.locator("#issue-panel-tab-sessions");
  await sessionsTab.click();

  // Wait for sessions data to load (sessions-tab or sessions-empty appears)
  await expect(
    page
      .getByTestId("sessions-tab")
      .or(page.getByTestId("sessions-empty"))
      .or(page.getByText("Failed to load sessions")),
  ).toBeVisible({ timeout: 5000 });
}

// -- Tests --

test.describe("Session Timeline", () => {
  test.describe("Timeline rendering", () => {
    test("sessions tab shows cost summary bar", async ({ page }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      const sessionsTab = page.getByTestId("sessions-tab");
      await expect(sessionsTab).toBeVisible();

      // Verify summary shows session count
      await expect(sessionsTab).toContainText("Sessions");
      await expect(sessionsTab).toContainText("4");

      // Verify summary shows tokens
      await expect(sessionsTab).toContainText("Tokens");

      // Verify summary shows cost
      await expect(sessionsTab).toContainText("Cost");
    });

    test("active sessions count badge appears when running sessions exist", async ({
      page,
    }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      const sessionsTab = page.getByTestId("sessions-tab");
      // sess-3 is active
      await expect(sessionsTab).toContainText("1 active");
    });

    test("timeline renders a row for each session, sorted newest-first", async ({
      page,
    }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      const timeline = page.getByTestId("session-timeline");
      await expect(timeline).toBeVisible();

      // Should have 4 rows
      await expect(page.getByTestId("session-row-sess-1")).toBeVisible();
      await expect(page.getByTestId("session-row-sess-2")).toBeVisible();
      await expect(page.getByTestId("session-row-sess-3")).toBeVisible();
      await expect(page.getByTestId("session-row-sess-4")).toBeVisible();

      // Newest-first: sess-3 (02:00) > sess-1 (01:00) > sess-2 (00:50) > sess-4 (23:00)
      const rows = timeline.locator('[data-testid^="session-row-"]');
      await expect(rows).toHaveCount(4);
      await expect(rows.nth(0)).toHaveAttribute(
        "data-testid",
        "session-row-sess-3",
      );
      await expect(rows.nth(1)).toHaveAttribute(
        "data-testid",
        "session-row-sess-1",
      );
    });

    test("each row displays agent name, status dot, phase badge, duration, tokens, cost", async ({
      page,
    }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      const row = page.getByTestId("session-row-sess-1");
      await expect(row).toContainText("nova");
      await expect(row).toContainText("implementation");
      await expect(row).toContainText("5m 0s");
      await expect(row).toContainText("8.0K tok");
      await expect(row).toContainText("$1.50");

      // Status dot
      const statusDot = row.locator('[data-status="completed"]');
      await expect(statusDot).toBeVisible();
    });

    test("empty state shows when no sessions exist", async ({ page }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page, { sessions: [] });
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      await expect(page.getByTestId("sessions-empty")).toContainText(
        "No sessions recorded yet",
      );
    });
  });

  test.describe("Session selection", () => {
    test("initial state shows placeholder text", async ({ page }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      await expect(
        page.getByText("Select a session to view details"),
      ).toBeVisible();
      await expect(page.getByTestId("session-detail-view")).not.toBeVisible();
    });

    test("clicking a session row shows the detail view", async ({ page }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      await page.getByTestId("session-row-sess-1").click();

      await expect(page.getByTestId("session-detail-view")).toBeVisible();
      await expect(
        page.getByText("Select a session to view details"),
      ).not.toBeVisible();
    });

    test("selected row gets visual highlight", async ({ page }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      const row = page.getByTestId("session-row-sess-1");
      await row.click();

      // The row should have the selected class
      const classAttr = await row.getAttribute("class");
      expect(classAttr).toContain("selected");
    });

    test("clicking a different session switches the detail view", async ({
      page,
    }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      // Select first session
      await page.getByTestId("session-row-sess-1").click();
      await expect(page.getByTestId("session-detail-view")).toContainText(
        "claude-opus-4-6",
      );

      // Select different session
      await page.getByTestId("session-row-sess-2").click();
      await expect(page.getByTestId("session-detail-view")).toContainText(
        "claude-sonnet-4-6",
      );

      // First row should lose highlight
      const row1Class = await page
        .getByTestId("session-row-sess-1")
        .getAttribute("class");
      expect(row1Class).not.toContain("selected");

      // Second row should have highlight
      const row2Class = await page
        .getByTestId("session-row-sess-2")
        .getAttribute("class");
      expect(row2Class).toContain("selected");
    });
  });

  test.describe("Session detail view - metadata", () => {
    test("detail view shows model, exit code, files changed, lines", async ({
      page,
    }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      await page.getByTestId("session-row-sess-1").click();

      const detail = page.getByTestId("session-detail-view");
      await expect(detail).toContainText("claude-opus-4-6");
      await expect(detail).toContainText("0 (success)");
      await expect(detail).toContainText("3"); // files_changed
      await expect(detail).toContainText("+50");
      await expect(detail).toContainText("-10");
    });

    test("files and lines metadata hidden when values are 0", async ({
      page,
    }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      // sess-2 has 0 files and 0 lines
      await page.getByTestId("session-row-sess-2").click();

      const detail = page.getByTestId("session-detail-view");
      // Should show exit code
      await expect(detail).toContainText("1");
      // Should NOT show "Files:" label since files_changed is 0
      await expect(detail.locator("text=Files:")).not.toBeVisible();
    });

    test("model hidden when not present", async ({ page }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      // sess-3 has no model
      await page.getByTestId("session-row-sess-3").click();

      const detail = page.getByTestId("session-detail-view");
      await expect(detail.locator("text=Model:")).not.toBeVisible();
    });
  });

  test.describe("Session detail view - files touched", () => {
    test("files touched section shows expandable list", async ({ page }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      await page.getByTestId("session-row-sess-1").click();

      const detail = page.getByTestId("session-detail-view");
      const filesTouched = detail.locator("details");
      await expect(filesTouched).toBeVisible();
      await expect(filesTouched.locator("summary")).toContainText(
        "Files Touched (3)",
      );
    });

    test("clicking summary expands/collapses the file list", async ({
      page,
    }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      await page.getByTestId("session-row-sess-1").click();

      const detail = page.getByTestId("session-detail-view");
      const detailsEl = detail.locator("details");
      const summary = detailsEl.locator("summary");

      // Initially collapsed — file items not visible
      await expect(detailsEl).not.toHaveAttribute("open", "");

      // Click to expand
      await summary.click();
      await expect(detailsEl).toHaveAttribute("open", "");

      // Verify file paths
      await expect(detail).toContainText("src/api.ts");
      await expect(detail).toContainText("src/types.ts");
      await expect(detail).toContainText("src/utils.ts");

      // Click to collapse
      await summary.click();
      await expect(detailsEl).not.toHaveAttribute("open", "");
    });
  });

  test.describe("Session detail view - transcript tab", () => {
    test("transcript tab is active by default when selecting a session", async ({
      page,
    }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      await page.getByTestId("session-row-sess-1").click();

      const transcriptTab = page.getByTestId("session-inner-tab-transcript");
      const classAttr = await transcriptTab.getAttribute("class");
      expect(classAttr).toContain("activeInnerTab");

      await expect(page.getByTestId("session-transcript")).toBeVisible();
    });

    test("transcript entries render with role labels", async ({ page }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      await page.getByTestId("session-row-sess-1").click();

      const transcript = page.getByTestId("session-transcript");
      await expect(transcript).toBeVisible();

      // Check role labels
      await expect(transcript.locator('[data-role="user"]')).toBeVisible();
      await expect(
        transcript.locator('[data-role="assistant"]').first(),
      ).toBeVisible();
      await expect(transcript.locator('[data-role="system"]')).toBeVisible();
    });

    test("tool entries show Tool: {tool_name} label", async ({ page }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      await page.getByTestId("session-row-sess-1").click();

      const transcript = page.getByTestId("session-transcript");
      await expect(transcript).toContainText("Tool: Read");
    });

    test("empty transcript shows message", async ({ page }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page, { transcript: [] });
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      await page.getByTestId("session-row-sess-1").click();

      await expect(page.getByTestId("session-transcript")).toContainText(
        "No transcript entries",
      );
    });
  });

  test.describe("Session detail view - diff tab", () => {
    test("clicking Diff tab switches to diff content view", async ({
      page,
    }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      await page.getByTestId("session-row-sess-1").click();

      // Click Diff tab
      await page.getByTestId("session-inner-tab-diff").click();

      await expect(page.getByTestId("session-diff")).toBeVisible();
      await expect(page.getByTestId("session-transcript")).not.toBeVisible();
    });

    test("diff tab is disabled when session has_diff is false", async ({
      page,
    }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      // sess-2 has has_diff=false
      await page.getByTestId("session-row-sess-2").click();

      const diffTab = page.getByTestId("session-inner-tab-diff");
      await expect(diffTab).toBeDisabled();
      await expect(diffTab).toHaveAttribute("title", "No diff available");
    });

    test("no diff available message when diff is null", async ({ page }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page, { diff: null });
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      await page.getByTestId("session-row-sess-1").click();
      await page.getByTestId("session-inner-tab-diff").click();

      await expect(page.getByTestId("session-diff")).toContainText(
        "No diff available",
      );
    });
  });

  test.describe("Keyboard navigation", () => {
    test("session rows are focusable with role=button", async ({ page }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      const row = page.getByTestId("session-row-sess-1");
      await expect(row).toHaveAttribute("role", "button");
      await expect(row).toHaveAttribute("tabindex", "0");
    });

    test("Enter/Space key activates a session row", async ({ page }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      const row = page.getByTestId("session-row-sess-1");
      await row.focus();
      await row.press("Enter");

      await expect(page.getByTestId("session-detail-view")).toBeVisible();

      // Space key on different row
      const row2 = page.getByTestId("session-row-sess-2");
      await row2.focus();
      await row2.press("Space");

      await expect(page.getByTestId("session-detail-view")).toContainText(
        "claude-sonnet-4-6",
      );
    });
  });

  test.describe("Status formatting", () => {
    test("duration formats correctly", async ({ page }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      // sess-1: 300s → "5m 0s"
      await expect(page.getByTestId("session-row-sess-1")).toContainText(
        "5m 0s",
      );

      // sess-2: 45s → "45s"
      await expect(page.getByTestId("session-row-sess-2")).toContainText("45s");

      // sess-3: undefined → "--"
      await expect(page.getByTestId("session-row-sess-3")).toContainText("--");
    });

    test("token count formats with K suffix", async ({ page }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      // sess-1: 5000+3000=8000 → "8.0K tok"
      await expect(page.getByTestId("session-row-sess-1")).toContainText(
        "8.0K tok",
      );

      // sess-2: 300+200=500 → "500 tok"
      await expect(page.getByTestId("session-row-sess-2")).toContainText(
        "500 tok",
      );
    });

    test("cost formats as USD", async ({ page }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      // sess-1: 1.5 → "$1.50"
      await expect(page.getByTestId("session-row-sess-1")).toContainText(
        "$1.50",
      );

      // sess-2: 0.005 → "<$0.01"
      await expect(page.getByTestId("session-row-sess-2")).toContainText(
        "<$0.01",
      );

      // sess-3: 0 → "$0.00"
      await expect(page.getByTestId("session-row-sess-3")).toContainText(
        "$0.00",
      );
    });
  });

  test.describe("Edge cases", () => {
    test("session with no phase hides phase badge", async ({ page }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      // sess-3 has no phase
      const row = page.getByTestId("session-row-sess-3");
      await expect(row.locator("[data-phase]")).not.toBeVisible();
    });

    test("API error for sessions shows error state", async ({ page }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page, { sessionsError: true });
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      const panel = page.getByTestId("issue-detail-panel");
      await expect(panel.getByText("Failed to load sessions")).toBeVisible();
    });

    test("running session shows pulsing status dot", async ({ page }) => {
      await installIssuesMock(page, [testIssue]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateAndWaitForBoard(page);
      await openIssuePanelAndSwitchToSessions(page);

      const row = page.getByTestId("session-row-sess-3");
      const statusDot = row.locator('[data-status="running"]');
      await expect(statusDot).toBeVisible();
    });
  });
});
