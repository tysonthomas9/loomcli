import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

/**
 * E2E tests for SessionDetailView component's transcript and diff inner tabs.
 *
 * Validates deep content rendering paths: transcript entries with varying roles
 * and tool metadata, diff display, inner tab switching, loading/error/empty
 * states per inner tab, and the files-touched collapsible section.
 *
 * Navigation to the detail view is a setup step — sibling spec loomcli-hmq8g
 * covers timeline row interactions and metadata rendering.
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

const MOCK_ISSUE = {
  id: "sdv-e2e-task-1",
  title: "Session Detail View Test Issue",
  status: "in_progress",
  priority: 2,
  issue_type: "task",
  description: "Test issue for session detail view",
  created_at: "2026-01-20T10:00:00Z",
  updated_at: "2026-01-20T10:00:00Z",
};

const MOCK_ISSUE_DETAILS = {
  ...MOCK_ISSUE,
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

function createMockSession(
  overrides: Partial<MockSession> = {},
): MockSession {
  return {
    id: "sess-001",
    agent_name: "agent-alpha",
    backend: "claude",
    model: "opus-4",
    phase: "implementation",
    status: "completed",
    started_at: "2026-01-20T10:00:00Z",
    ended_at: "2026-01-20T10:05:00Z",
    duration_s: 300,
    input_tokens: 5000,
    output_tokens: 3000,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    estimated_cost_usd: 0.15,
    exit_code: 0,
    files_changed: 3,
    lines_added: 50,
    lines_removed: 10,
    files_touched: ["src/foo.ts", "src/bar.ts", "README.md"],
    attempt_num: 1,
    has_transcript: true,
    has_diff: true,
    is_active: false,
    ...overrides,
  };
}

const MOCK_SESSIONS = [
  createMockSession(),
  createMockSession({
    id: "sess-002",
    agent_name: "falcon",
    status: "failed",
    model: undefined,
    started_at: "2026-01-20T09:00:00Z",
    ended_at: "2026-01-20T09:02:00Z",
    duration_s: 120,
    input_tokens: 1000,
    output_tokens: 500,
    estimated_cost_usd: 0.03,
    exit_code: 1,
    files_changed: 0,
    lines_added: 0,
    lines_removed: 0,
    files_touched: undefined,
    has_transcript: true,
    has_diff: false,
    is_active: false,
  }),
];

// TranscriptEntry[] — covers user, assistant, tool_use, and tool_result
// in the canonical transcript.Event wire format (text/tool_input/output).
const MOCK_TRANSCRIPT = [
  {
    seq: 1,
    timestamp: "2026-01-20T10:00:01Z",
    role: "user",
    type: "text",
    text: "Please fix the login bug",
  },
  {
    seq: 2,
    timestamp: "2026-01-20T10:00:05Z",
    role: "assistant",
    type: "text",
    text: "I will investigate the login handler",
  },
  {
    seq: 3,
    timestamp: "2026-01-20T10:00:10Z",
    role: "assistant",
    type: "tool_use",
    tool_name: "Read",
    tool_input: { file_path: "src/auth.ts" },
  },
  {
    seq: 4,
    timestamp: "2026-01-20T10:00:15Z",
    role: "tool",
    type: "tool_result",
    output: "function login() { ... }",
  },
  {
    seq: 5,
    timestamp: "2026-01-20T10:00:20Z",
    role: "assistant",
    type: "tool_use",
    tool_name: "Bash",
    tool_input: { command: "npm test" },
  },
];

const MOCK_DIFF = `--- a/src/auth.ts
+++ b/src/auth.ts
@@ -10,7 +10,7 @@
 function login(user: string) {
-  return null;
+  return authenticate(user);
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

  await page.route("**/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok" }),
    });
  });

  await page.route("**/api/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ agents: [], tasks: [] }),
    });
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

  // SSE mock — fulfill immediately with connected event to prevent reconnection loops
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
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
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
              // eslint-disable-next-line @typescript-eslint/no-explicit-any
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

interface SessionMockOptions {
  transcriptDelay?: number;
  diffDelay?: number;
  emptyTranscript?: boolean;
  diff404?: boolean;
  transcriptError?: boolean;
  diffError?: boolean;
}

async function setupSessionMocks(
  page: Page,
  options: SessionMockOptions = {},
) {
  // Mock GET /api/workspaces/default/issues/{id} for detail panel
  await page.route("**/workspaces/*/issues/sdv-e2e-*", async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok(MOCK_ISSUE_DETAILS),
      });
    } else {
      await route.fallback();
    }
  });

  // Mock GET /api/tasks/{taskId}/sessions/{sessionId}/transcript
  // Registered BEFORE broader sessions route for LIFO priority
  await page.route("**/api/tasks/*/sessions/*/transcript", async (route) => {
    if (options.transcriptError) {
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ error: "Internal error" }),
      });
      return;
    }
    if (options.transcriptDelay) {
      await new Promise((r) => setTimeout(r, options.transcriptDelay));
    }
    const entries = options.emptyTranscript ? [] : MOCK_TRANSCRIPT;
    const url = route.request().url();
    const sessionId =
      url.match(/sessions\/([^/]+)\/transcript/)?.[1] ?? "unknown";
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({ session_id: sessionId, entries }),
    });
  });

  // Mock GET /api/tasks/{taskId}/sessions/{sessionId}/diff — raw text/plain
  await page.route("**/api/tasks/*/sessions/*/diff", async (route) => {
    if (options.diff404) {
      await route.fulfill({
        status: 404,
        contentType: "application/json",
        body: JSON.stringify({ error: "Not found" }),
      });
      return;
    }
    if (options.diffError) {
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ error: "Internal error" }),
      });
      return;
    }
    if (options.diffDelay) {
      await new Promise((r) => setTimeout(r, options.diffDelay));
    }
    await route.fulfill({
      status: 200,
      contentType: "text/plain",
      body: MOCK_DIFF,
    });
  });

  // Mock GET /api/tasks/{taskId}/sessions — session list
  await page.route("**/api/tasks/*/sessions", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({ task_id: MOCK_ISSUE.id, sessions: MOCK_SESSIONS }),
    });
  });
}

// -- Navigation helpers --

async function navigateToApp(page: Page) {
  await page.goto("/ws/default/", { waitUntil: "domcontentloaded" });
}

async function openIssuePanel(page: Page) {
  const card = page
    .locator("article")
    .filter({ hasText: MOCK_ISSUE.title });
  await expect(card).toBeVisible();
  await card.click();

  const panel = page.getByTestId("issue-detail-panel");
  await expect(panel).toHaveAttribute("data-state", "open");
  await expect(panel).toHaveAttribute("data-loading", "false", {
    timeout: 5000,
  });
}

async function switchToSessionsTab(page: Page) {
  await page.locator("#issue-panel-tab-sessions").click();
  await expect(
    page
      .getByTestId("sessions-tab")
      .or(page.getByTestId("sessions-empty"))
      .or(page.getByText("Failed to load sessions")),
  ).toBeVisible({ timeout: 5000 });
}

async function selectSession(
  page: Page,
  sessionRowTestId = "session-row-sess-001",
) {
  await page.getByTestId(sessionRowTestId).click();
  await expect(page.getByTestId("session-detail-view")).toBeVisible();
}

async function navigateToSessionDetail(
  page: Page,
  sessionRowTestId?: string,
) {
  await navigateToApp(page);
  await openIssuePanel(page);
  await switchToSessionsTab(page);
  await selectSession(page, sessionRowTestId);
}

// -- Tests --

test.describe("Session Detail View - Transcript and Diff Tabs", () => {
  test.describe("transcript tab", () => {
    test("transcript tab is active by default and shows entries", async ({
      page,
    }) => {
      await installIssuesMock(page, [MOCK_ISSUE]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateToSessionDetail(page);

      // Transcript inner tab has activeInnerTab class
      await expect(
        page.getByTestId("session-inner-tab-transcript"),
      ).toHaveClass(/activeInnerTab/);
      // Transcript container is visible
      await expect(page.getByTestId("session-transcript")).toBeVisible();
      // 5 transcript entries rendered (matching MOCK_TRANSCRIPT length)
      const roleElements = page
        .getByTestId("session-transcript")
        .locator("[data-role]");
      await expect(roleElements).toHaveCount(5);
    });

    test("renders role labels for each entry type", async ({ page }) => {
      await installIssuesMock(page, [MOCK_ISSUE]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateToSessionDetail(page);

      const transcript = page.getByTestId("session-transcript");
      // data-role attributes: 1 user, 3 assistant (seq 2, 3, 5), 1 tool (seq 4)
      await expect(transcript.locator('[data-role="user"]')).toHaveCount(1);
      await expect(transcript.locator('[data-role="assistant"]')).toHaveCount(
        3,
      );
      await expect(transcript.locator('[data-role="tool"]')).toHaveCount(1);
      // Role text content is rendered
      await expect(transcript.locator('[data-role="user"]')).toContainText(
        "user",
      );
      await expect(transcript.locator('[data-role="tool"]')).toContainText(
        "tool",
      );
    });

    test("renders tool name for tool_use entries", async ({ page }) => {
      await installIssuesMock(page, [MOCK_ISSUE]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateToSessionDetail(page);

      const transcript = page.getByTestId("session-transcript");
      // "Tool: Read" from seq 3
      await expect(transcript).toContainText("Tool: Read");
      // "Tool: Bash" from seq 5
      await expect(transcript).toContainText("Tool: Bash");
    });

    test("shows tool_input as fallback when content is absent on tool_use entry", async ({
      page,
    }) => {
      await installIssuesMock(page, [MOCK_ISSUE]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateToSessionDetail(page);

      // seq 5: tool_use with tool_name="Bash", tool_input="npm test", NO content
      await expect(page.getByTestId("session-transcript")).toContainText(
        "npm test",
      );
    });

    test("shows user message content", async ({ page }) => {
      await installIssuesMock(page, [MOCK_ISSUE]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateToSessionDetail(page);

      await expect(page.getByTestId("session-transcript")).toContainText(
        "Please fix the login bug",
      );
    });

    test("shows assistant message content", async ({ page }) => {
      await installIssuesMock(page, [MOCK_ISSUE]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateToSessionDetail(page);

      await expect(page.getByTestId("session-transcript")).toContainText(
        "I will investigate the login handler",
      );
    });

    test("shows tool_result content", async ({ page }) => {
      await installIssuesMock(page, [MOCK_ISSUE]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateToSessionDetail(page);

      await expect(page.getByTestId("session-transcript")).toContainText(
        "function login() { ... }",
      );
    });

    test("shows empty state when transcript has no entries", async ({
      page,
    }) => {
      await installIssuesMock(page, [MOCK_ISSUE]);
      await setupBaseMocks(page);
      await setupSessionMocks(page, { emptyTranscript: true });
      await navigateToSessionDetail(page);

      await expect(page.getByTestId("session-transcript")).toContainText(
        "No transcript entries",
      );
    });

    test("shows loading state while transcript is fetching", async ({
      page,
    }) => {
      await installIssuesMock(page, [MOCK_ISSUE]);
      await setupBaseMocks(page);
      await setupSessionMocks(page, { transcriptDelay: 5000 });
      await navigateToSessionDetail(page);

      // transcriptLoading && entries.length === 0 → loading text
      await expect(page.getByTestId("session-transcript")).toContainText(
        "Loading transcript...",
      );
    });

    test("shows error state when transcript fetch fails", async ({
      page,
    }) => {
      await installIssuesMock(page, [MOCK_ISSUE]);
      await setupBaseMocks(page);
      await setupSessionMocks(page, { transcriptError: true });
      await navigateToSessionDetail(page);

      await expect(page.getByTestId("session-transcript")).toContainText(
        "Failed to load transcript",
      );
    });
  });

  test.describe("diff tab", () => {
    test("clicking Diff tab shows diff content", async ({ page }) => {
      await installIssuesMock(page, [MOCK_ISSUE]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateToSessionDetail(page); // sess-001 has_diff: true

      await page.getByTestId("session-inner-tab-diff").click();
      await expect(page.getByTestId("session-diff")).toBeVisible();
      // Diff content contains key text from MOCK_DIFF
      await expect(page.getByTestId("session-diff")).toContainText(
        "authenticate(user)",
      );
    });

    test("diff tab is disabled when session has_diff is false", async ({
      page,
    }) => {
      await installIssuesMock(page, [MOCK_ISSUE]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateToSessionDetail(page, "session-row-sess-002"); // has_diff: false

      await expect(
        page.getByTestId("session-inner-tab-diff"),
      ).toBeDisabled();
      await expect(
        page.getByTestId("session-inner-tab-diff"),
      ).toHaveAttribute("title", "No diff available");
    });

    test("shows empty state when diff endpoint returns 404", async ({
      page,
    }) => {
      await installIssuesMock(page, [MOCK_ISSUE]);
      await setupBaseMocks(page);
      await setupSessionMocks(page, { diff404: true });
      await navigateToSessionDetail(page); // sess-001 has_diff: true

      await page.getByTestId("session-inner-tab-diff").click();
      // getSessionDiff catches 404 → returns null → "No diff available"
      await expect(page.getByTestId("session-diff")).toContainText(
        "No diff available",
      );
    });

    test("shows loading state while diff is fetching", async ({ page }) => {
      await installIssuesMock(page, [MOCK_ISSUE]);
      await setupBaseMocks(page);
      await setupSessionMocks(page, { diffDelay: 5000 });
      await navigateToSessionDetail(page);

      await page.getByTestId("session-inner-tab-diff").click();
      await expect(page.getByTestId("session-diff")).toContainText(
        "Loading diff...",
      );
    });

    test("shows error state when diff fetch fails", async ({ page }) => {
      await installIssuesMock(page, [MOCK_ISSUE]);
      await setupBaseMocks(page);
      await setupSessionMocks(page, { diffError: true });
      await navigateToSessionDetail(page);

      await page.getByTestId("session-inner-tab-diff").click();
      // 500 error propagates to diffError state
      await expect(page.getByTestId("session-diff")).toContainText(
        "Failed to load diff",
      );
    });
  });

  test.describe("inner tab switching", () => {
    test("switching from transcript to diff hides transcript and shows diff", async ({
      page,
    }) => {
      await installIssuesMock(page, [MOCK_ISSUE]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateToSessionDetail(page);

      // Initially: transcript visible, diff not visible
      await expect(page.getByTestId("session-transcript")).toBeVisible();
      await expect(page.getByTestId("session-diff")).not.toBeVisible();

      // Click diff tab
      await page.getByTestId("session-inner-tab-diff").click();

      // Now: diff visible, transcript not visible
      await expect(page.getByTestId("session-diff")).toBeVisible();
      await expect(
        page.getByTestId("session-transcript"),
      ).not.toBeVisible();
    });

    test("switching back to transcript from diff restores transcript content", async ({
      page,
    }) => {
      await installIssuesMock(page, [MOCK_ISSUE]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateToSessionDetail(page);

      // Go to diff, then back to transcript
      await page.getByTestId("session-inner-tab-diff").click();
      await expect(page.getByTestId("session-diff")).toBeVisible();

      await page.getByTestId("session-inner-tab-transcript").click();
      await expect(page.getByTestId("session-transcript")).toBeVisible();
      await expect(page.getByTestId("session-transcript")).toContainText(
        "Please fix the login bug",
      );
    });

    test("active tab button has activeInnerTab class", async ({ page }) => {
      await installIssuesMock(page, [MOCK_ISSUE]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateToSessionDetail(page);

      // Initially transcript is active
      await expect(
        page.getByTestId("session-inner-tab-transcript"),
      ).toHaveClass(/activeInnerTab/);
      await expect(
        page.getByTestId("session-inner-tab-diff"),
      ).not.toHaveClass(/activeInnerTab/);

      // Switch to diff
      await page.getByTestId("session-inner-tab-diff").click();
      await expect(
        page.getByTestId("session-inner-tab-diff"),
      ).toHaveClass(/activeInnerTab/);
      await expect(
        page.getByTestId("session-inner-tab-transcript"),
      ).not.toHaveClass(/activeInnerTab/);
    });
  });

  test.describe("files touched section", () => {
    test("shows collapsible files touched section with file count and paths", async ({
      page,
    }) => {
      await installIssuesMock(page, [MOCK_ISSUE]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateToSessionDetail(page); // sess-001 has files_touched

      const detailView = page.getByTestId("session-detail-view");
      await expect(detailView.locator("summary")).toContainText(
        "Files Touched (3)",
      );

      // Click summary to expand the <details> element
      await detailView.locator("summary").click();

      // File paths are visible
      await expect(detailView).toContainText("src/foo.ts");
      await expect(detailView).toContainText("src/bar.ts");
      await expect(detailView).toContainText("README.md");
    });

    test("hides files touched section when session has no files_touched", async ({
      page,
    }) => {
      await installIssuesMock(page, [MOCK_ISSUE]);
      await setupBaseMocks(page);
      await setupSessionMocks(page);
      await navigateToSessionDetail(page, "session-row-sess-002"); // no files_touched

      const detailView = page.getByTestId("session-detail-view");
      // "Files Touched" text is NOT present — no <summary> element
      await expect(detailView.locator("summary")).not.toBeVisible();
    });
  });
});
