/**
 * E2E: Session history section in IssueDetailPanel.
 *
 * Tests the SessionHistorySection component which displays terminal session
 * history records, action buttons (Jump to tab / View scrollback), and
 * the scrollback overlay. Uses workspace-scoped API mocking.
 */

import { test, expect } from "../fixtures";
import type { Page } from "@playwright/test";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const WS_ID = "test-ws";
const WS_PREFIX = `/api/workspaces/${WS_ID}`;

// ---------------------------------------------------------------------------
// Mock data
// ---------------------------------------------------------------------------

const WORKSPACE_DATA = {
  id: WS_ID,
  name: "Test Workspace",
  path: "/tmp/test",
  repos: [],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: WS_ID,
      name: "Test Workspace",
      path: "/tmp/test",
      active: true,
      repo_count: 0,
      is_default: true,
    },
  ],
  workspace_order: [WS_ID],
  default_workspace: "",
};

const MOCK_STATS = {
  total_issues: 2,
  open_issues: 2,
  in_progress_issues: 0,
  closed_issues: 0,
  blocked_issues: 0,
  deferred_issues: 0,
  ready_issues: 2,
  tombstone_issues: 0,
  pinned_issues: 0,
  epics_eligible_for_closure: 0,
  average_lead_time_hours: 0,
};

interface SessionRecord {
  id: string;
  session_name: string;
  issue_id: string;
  backend: string;
  status: "active" | "completed";
  launcher: string;
  started_at: string;
  ended_at?: string;
  scrollback_path?: string;
}

function createMockSession(overrides?: Partial<SessionRecord>): SessionRecord {
  return {
    id: "sess-rec-1",
    session_name: "session-alpha",
    issue_id: "sess-test-1",
    backend: "claude",
    status: "completed",
    launcher: "loom",
    started_at: new Date(Date.now() - 2 * 60 * 60 * 1000).toISOString(), // 2h ago
    ended_at: new Date(Date.now() - 1.5 * 60 * 60 * 1000).toISOString(), // 1.5h ago
    scrollback_path: "/tmp/scrollback/sess-rec-1.log",
    ...overrides,
  };
}

const activeSession = createMockSession({
  id: "sess-active",
  session_name: "session-live",
  status: "active",
  started_at: new Date(Date.now() - 30 * 60 * 1000).toISOString(), // 30m ago
  ended_at: undefined,
  scrollback_path: undefined,
});

const completedSessionWithScrollback = createMockSession({
  id: "sess-completed-sb",
  session_name: "session-done",
  status: "completed",
  started_at: new Date(Date.now() - 3 * 60 * 60 * 1000).toISOString(),
  ended_at: new Date(Date.now() - 2.5 * 60 * 60 * 1000).toISOString(),
  scrollback_path: "/tmp/scrollback/done.log",
});

const completedSessionNoScrollback = createMockSession({
  id: "sess-completed-nosb",
  session_name: "session-nosb",
  status: "completed",
  started_at: new Date(Date.now() - 5 * 60 * 60 * 1000).toISOString(),
  ended_at: new Date(Date.now() - 4 * 60 * 60 * 1000).toISOString(),
  scrollback_path: undefined,
});

const DEFAULT_SESSIONS = [
  activeSession,
  completedSessionWithScrollback,
  completedSessionNoScrollback,
];

const MOCK_ISSUES = [
  {
    id: "sess-test-1",
    title: "Session Test Issue",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-20T10:00:00Z",
    updated_at: "2026-01-20T10:00:00Z",
  },
  {
    id: "sess-test-2",
    title: "Issue Without Sessions",
    status: "open",
    priority: 3,
    issue_type: "task",
    created_at: "2026-01-20T11:00:00Z",
    updated_at: "2026-01-20T11:00:00Z",
  },
];

function getIssueDetails(id: string) {
  const issue = MOCK_ISSUES.find((i) => i.id === id) ?? MOCK_ISSUES[0];
  return {
    ...issue,
    description: "",
    labels: [],
    dependencies: [],
    dependents: [],
    comments: [],
    events: [],
  };
}

// ---------------------------------------------------------------------------
// Setup helpers
// ---------------------------------------------------------------------------

interface SetupOptions {
  sessions?: SessionRecord[];
  sessionsError?: boolean;
  sessionsDelay?: number;
  scrollbackContent?: string;
  scrollbackLines?: number;
  scrollbackError?: boolean;
  scrollbackDelay?: number;
}

async function setupMocks(page: Page, options: SetupOptions = {}): Promise<void> {
  const {
    sessions = DEFAULT_SESSIONS,
    sessionsError = false,
    sessionsDelay = 0,
    scrollbackContent = "$ echo hello\nhello\n$ exit",
    scrollbackError = false,
    scrollbackDelay = 0,
  } = options;

  // Neutralize AbortController signals (React StrictMode workaround)
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

  // Auth token
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

  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token" }),
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

  // Workspace-scoped routes — single handler (LIFO-safe; sub-paths checked first)
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

      // SSE events
      if (url.includes(WS_PREFIX + "/events")) {
        await route.abort();
        return;
      }

      // Terminal metadata/session-map endpoints used during app boot.
      if (url.includes(WS_PREFIX + "/terminal/tabs")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: [] }),
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

      // Scrollback: /issues/*/sessions/*/scrollback — check BEFORE sessions
      if (url.includes("/sessions/") && url.includes("/scrollback")) {
        if (scrollbackDelay > 0) {
          await new Promise((r) => setTimeout(r, scrollbackDelay));
        }
        if (scrollbackError) {
          await route.fulfill({
            status: 500,
            contentType: "application/json",
            body: JSON.stringify({ success: false, error: "scrollback fetch failed" }),
          });
          return;
        }
        await route.fulfill({
          status: 200,
          contentType: "text/plain",
          body: scrollbackContent,
        });
        return;
      }

      // Sessions list: /issues/*/sessions
      if (url.includes("/sessions") && url.includes("/issues/")) {
        if (sessionsDelay > 0) {
          await new Promise((r) => setTimeout(r, sessionsDelay));
        }
        if (sessionsError) {
          await route.fulfill({
            status: 500,
            contentType: "application/json",
            body: JSON.stringify({ success: false, error: "Internal Server Error" }),
          });
          return;
        }
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: sessions }),
        });
        return;
      }

      // Issue sub-resources (tabs, comments, events, etc.)
      if (url.includes(WS_PREFIX + "/issues/") && method === "GET") {
        const afterIssues = url.split(WS_PREFIX + "/issues/")[1] ?? "";
        const pathParts = afterIssues.split("?")[0].split("/");
        if (pathParts.length > 1 && pathParts[1]) {
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

      // Issue detail: /issues/{id}
      if (url.includes(WS_PREFIX + "/issues/") && method === "GET") {
        const issueId = url.split("/issues/")[1]?.split("?")[0]?.split("/")[0];
        if (issueId) {
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ success: true, data: getIssueDetails(issueId) }),
          });
          return;
        }
      }

      // Issues graph
      if (url.includes(WS_PREFIX + "/issues/graph")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: MOCK_ISSUES }),
        });
        return;
      }

      // Ready / issues list
      if (url.includes(WS_PREFIX + "/ready") || (url.includes(WS_PREFIX + "/issues") && method === "GET")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: MOCK_ISSUES }),
        });
        return;
      }

      // Blocked
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

      // Workspace validation (catch-all for /api/workspaces/test-ws)
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

async function navigateToApp(page: Page): Promise<void> {
  await page.goto(`/ws/${WS_ID}/kanban?groupBy=none`);
  await page.waitForSelector("article", { timeout: 15000 });
}

async function openIssuePanel(page: Page, title: string): Promise<void> {
  await page.getByText(title, { exact: true }).click();
  await page.waitForSelector('[data-state="open"][data-loading="false"]', {
    timeout: 10000,
  });
}

async function expandSessionHistory(page: Page): Promise<void> {
  const section = page.getByTestId("session-history-section");
  const toggle = section.locator("button");
  await toggle.click();
  // Wait for collapsible content to appear
  await expect(section.locator("div").first()).toBeVisible({ timeout: 5000 });
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

test.describe("Session history section", () => {
  test.describe("Section visibility", () => {
    test("renders inside detail panel after expanding collapsible", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");
      await expandSessionHistory(page);

      const section = page.getByTestId("session-history-section");
      await expect(section).toBeVisible();
      // Content should be visible after expanding
      await expect(section.locator("button[aria-expanded='true']")).toBeVisible();
    });

    test("section title shows Session History", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");

      const section = page.getByTestId("session-history-section");
      await expect(section.getByText("Session History")).toBeVisible();
    });
  });

  test.describe("Loading state", () => {
    test("shows loading text while API response is pending", async ({ page }) => {
      let resolveDelay!: () => void;
      const delayPromise = new Promise<void>((r) => {
        resolveDelay = r;
      });

      await setupMocks(page);

      // Override sessions route with deferred response
      await page.route(
        (url) => {
          const s = url.toString();
          return (
            s.includes(WS_PREFIX + "/issues/") &&
            s.includes("/sessions") &&
            !s.includes("/scrollback")
          );
        },
        async (route) => {
          await delayPromise;
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ success: true, data: DEFAULT_SESSIONS }),
          });
        },
      );

      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");
      await expandSessionHistory(page);

      // Loading text should be visible while waiting
      await expect(page.getByText("Loading sessions...")).toBeVisible();

      // Resolve and verify records appear
      resolveDelay();
      await expect(page.getByText("claude").first()).toBeVisible({ timeout: 5000 });
    });
  });

  test.describe("Empty state", () => {
    test("shows empty message when no sessions exist", async ({ page }) => {
      await setupMocks(page, { sessions: [] });
      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");
      await expandSessionHistory(page);

      await expect(page.getByText("No terminal sessions yet")).toBeVisible();
    });
  });

  test.describe("Error state", () => {
    test("shows error message when session API returns 500", async ({ page }) => {
      await setupMocks(page, { sessionsError: true });
      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");
      await expandSessionHistory(page);

      // The component displays err.message from ApiError
      const section = page.getByTestId("session-history-section");
      // Should show some error text (exact text depends on ApiError construction)
      await expect(
        section.getByText(/failed|error|Internal/i),
      ).toBeVisible({ timeout: 5000 });
    });
  });

  test.describe("Session records display", () => {
    test("renders correct number of session items", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");
      await expandSessionHistory(page);

      // 3 default sessions — use backend name as proxy since each has "claude"
      // Count items by looking for the action button area pattern
      const section = page.getByTestId("session-history-section");
      // Each session item shows the backend name "claude"
      const backendTexts = section.getByText("claude");
      await expect(backendTexts).toHaveCount(3);
    });

    test("shows backend name for each record", async ({ page }) => {
      await setupMocks(page, {
        sessions: [
          createMockSession({ id: "s1", backend: "claude" }),
          createMockSession({ id: "s2", backend: "codex" }),
        ],
      });
      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");
      await expandSessionHistory(page);

      const section = page.getByTestId("session-history-section");
      await expect(section.getByText("claude")).toBeVisible();
      await expect(section.getByText("codex")).toBeVisible();
    });

    test("shows relative time for each record", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");
      await expandSessionHistory(page);

      const section = page.getByTestId("session-history-section");
      // Each record shows relative time like "30m ago", "2h ago", etc.
      await expect(section.getByText(/\d+[smhd] ago/)).toHaveCount(3);
    });

    test("shows duration for each record", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");
      await expandSessionHistory(page);

      const section = page.getByTestId("session-history-section");
      // Duration spans show values like "30m", "1h", etc.
      // Active session duration is computed from now - started_at
      // Completed sessions have ended_at so duration is fixed
      await expect(section.getByText(/^\d+[smhd]$/)).toHaveCount(3);
    });

    test("status indicator has correct data-status attribute", async ({ page }) => {
      await setupMocks(page);
      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");
      await expandSessionHistory(page);

      const section = page.getByTestId("session-history-section");
      // One active session
      await expect(section.locator('[data-status="active"]')).toHaveCount(1);
      // Two completed sessions
      await expect(section.locator('[data-status="completed"]')).toHaveCount(2);
    });
  });

  test.describe("Jump to tab button", () => {
    test("active session shows Jump to tab button", async ({ page }) => {
      await setupMocks(page, { sessions: [activeSession] });
      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");
      await expandSessionHistory(page);

      await expect(page.getByText("Jump to tab")).toBeVisible();
    });

    test("completed session does not show Jump to tab button", async ({ page }) => {
      await setupMocks(page, { sessions: [completedSessionWithScrollback] });
      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");
      await expandSessionHistory(page);

      await expect(page.getByText("Jump to tab")).not.toBeVisible();
    });
  });

  test.describe("View scrollback button", () => {
    test("completed session with scrollback_path shows View scrollback", async ({ page }) => {
      await setupMocks(page, { sessions: [completedSessionWithScrollback] });
      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");
      await expandSessionHistory(page);

      await expect(page.getByText("View scrollback")).toBeVisible();
    });

    test("completed session without scrollback_path does not show View scrollback", async ({ page }) => {
      await setupMocks(page, { sessions: [completedSessionNoScrollback] });
      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");
      await expandSessionHistory(page);

      await expect(page.getByText("View scrollback")).not.toBeVisible();
    });

    test("active session does not show View scrollback", async ({ page }) => {
      await setupMocks(page, { sessions: [activeSession] });
      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");
      await expandSessionHistory(page);

      await expect(page.getByText("View scrollback")).not.toBeVisible();
    });
  });

  test.describe("Scrollback overlay", () => {
    test("clicking View scrollback opens overlay with session name", async ({ page }) => {
      await setupMocks(page, { sessions: [completedSessionWithScrollback] });
      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");
      await expandSessionHistory(page);

      await page.getByText("View scrollback").click();
      await expect(
        page.getByText(`Scrollback: ${completedSessionWithScrollback.session_name}`),
      ).toBeVisible();
    });

    test("overlay shows Loading while fetching scrollback", async ({ page }) => {
      let resolveScrollback!: () => void;
      const scrollbackPromise = new Promise<void>((r) => {
        resolveScrollback = r;
      });

      await setupMocks(page, { sessions: [completedSessionWithScrollback] });

      // Override scrollback route with deferred response
      await page.route(
        (url) => url.toString().includes("/scrollback"),
        async (route) => {
          await scrollbackPromise;
          await route.fulfill({
            status: 200,
            contentType: "text/plain",
            body: "scrollback data",
          });
        },
      );

      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");
      await expandSessionHistory(page);

      await page.getByText("View scrollback").click();
      await expect(page.getByText("Loading...")).toBeVisible();

      resolveScrollback();
      await expect(page.getByText("scrollback data")).toBeVisible({ timeout: 5000 });
    });

    test("overlay displays scrollback content after loading", async ({ page }) => {
      const content = "$ echo hello\nhello\n$ exit";
      await setupMocks(page, {
        sessions: [completedSessionWithScrollback],
        scrollbackContent: content,
      });
      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");
      await expandSessionHistory(page);

      await page.getByText("View scrollback").click();
      await expect(page.getByText("$ echo hello")).toBeVisible({ timeout: 5000 });
    });

    test("clicking Close dismisses the overlay", async ({ page }) => {
      await setupMocks(page, { sessions: [completedSessionWithScrollback] });
      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");
      await expandSessionHistory(page);

      await page.getByText("View scrollback").click();
      await expect(
        page.getByText(`Scrollback: ${completedSessionWithScrollback.session_name}`),
      ).toBeVisible();

      await page.getByText("Close", { exact: true }).click();
      await expect(
        page.getByText(`Scrollback: ${completedSessionWithScrollback.session_name}`),
      ).not.toBeVisible();
    });

    test("shows error message on scrollback API failure", async ({ page }) => {
      await setupMocks(page, {
        sessions: [completedSessionWithScrollback],
        scrollbackError: true,
      });
      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");
      await expandSessionHistory(page);

      await page.getByText("View scrollback").click();
      await expect(
        page.getByText("Failed to load scrollback content."),
      ).toBeVisible({ timeout: 5000 });
    });
  });

  test.describe("Edge cases", () => {
    test("handles many sessions without layout issues", async ({ page }) => {
      const manySessions = Array.from({ length: 10 }, (_, i) =>
        createMockSession({
          id: `sess-${i}`,
          session_name: `session-${i}`,
          status: i % 2 === 0 ? "completed" : "active",
          started_at: new Date(Date.now() - (i + 1) * 60 * 60 * 1000).toISOString(),
          ended_at:
            i % 2 === 0
              ? new Date(Date.now() - i * 60 * 60 * 1000).toISOString()
              : undefined,
          scrollback_path: i % 2 === 0 ? `/tmp/sb-${i}.log` : undefined,
        }),
      );
      await setupMocks(page, { sessions: manySessions });
      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");
      await expandSessionHistory(page);

      const section = page.getByTestId("session-history-section");
      // All 10 sessions show "claude" backend
      await expect(section.getByText("claude")).toHaveCount(10);
    });

    test("empty scrollback content shows No content fallback", async ({ page }) => {
      await setupMocks(page, {
        sessions: [completedSessionWithScrollback],
        scrollbackContent: "",
        scrollbackLines: 0,
      });
      await navigateToApp(page);
      await openIssuePanel(page, "Session Test Issue");
      await expandSessionHistory(page);

      await page.getByText("View scrollback").click();
      await expect(page.getByText("No content")).toBeVisible({ timeout: 5000 });
    });
  });
});
