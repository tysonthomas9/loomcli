/**
 * Shared mock setup for terminal E2E tests.
 * Intercepts all API routes needed to render the terminal view
 * with mocked data, following the inline page.route() pattern
 * established in task-log-tabs.spec.ts.
 */

import { expect, type Page } from "@playwright/test";

export interface MockSession {
  name: string;
  label: string;
  created: number;
}

export interface MockTabMetadata {
  session_name: string;
  label: string;
  notes: string;
  sort_order: number;
  created_at: string;
  updated_at: string;
}

export interface TerminalMockOptions {
  sessions?: MockSession[];
  tabMetadata?: MockTabMetadata[];
}

const DEFAULT_SESSIONS: MockSession[] = [
  { name: "session-1", label: "Session 1", created: 1 },
];

const DEFAULT_TAB_METADATA: MockTabMetadata[] = [
  {
    session_name: "session-1",
    label: "Session 1",
    notes: "",
    sort_order: 0,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
];

/**
 * Set up all API route mocks needed to render the terminal view.
 * Call before page.goto('/').
 */
export async function setupTerminalMocks(
  page: Page,
  options?: TerminalMockOptions,
) {
  const sessions = options?.sessions ?? DEFAULT_SESSIONS;
  const tabs = options?.tabMetadata ?? DEFAULT_TAB_METADATA;

  // -- Standard app mocks --
  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 404,
      contentType: "application/json",
      body: JSON.stringify({ error: "Not found" }),
    });
  });

  await page.route("**/api/ready", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: [] }),
    });
  });

  await page.route("**/api/blocked", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: [] }),
    });
  });

  await page.route("**/api/issues/graph", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, issues: [] }),
    });
  });

  await page.route(
    (url) => url.pathname === "/api/issues",
    async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: [] }),
      });
    },
  );

  await page.route("**/api/events", async (route) => {
    await route.abort();
  });

  await page.route("**/api/stats", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: { open: 0, closed: 0, total: 0, completion: 0 },
      }),
    });
  });

  await page.route("**/api/loom/api/agents", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ agents: [] }),
    });
  });

  await page.route("**/api/loom/api/status", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        agents: [],
        tasks: {
          needs_planning: 0,
          ready_to_implement: 0,
          in_progress: 0,
          need_review: 0,
          backlog: 0,
        },
        agent_tasks: {},
        sync: {
          db_synced: true,
          db_last_sync: "",
          git_needs_push: 0,
          git_needs_pull: 0,
        },
        stats: { open: 0, closed: 0, total: 0, completion: 0 },
        timestamp: new Date().toISOString(),
      }),
    });
  });

  await page.route("**/api/loom/api/tasks", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        needs_planning: [],
        ready_to_implement: [],
        in_progress: [],
        needs_review: [],
        backlog: [],
      }),
    });
  });

  // -- Terminal-specific mocks --
  await page.route("**/api/terminal/sessions", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: { sessions } }),
    });
  });

  await page.route(
    (url) => url.pathname === "/api/terminal/tabs",
    async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: tabs }),
      });
    },
  );

  await page.route("**/api/terminal/tabs/*", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: {} }),
    });
  });

  await page.route("**/api/terminal/token**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "mock-token" }),
    });
  });

  await page.route("**/api/terminal/spawn", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: { session_name: "new-session" },
      }),
    });
  });

  await page.route("**/api/config/backend", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: { backend: "claude", available: ["claude"] },
      }),
    });
  });
}

/**
 * Navigate to the terminal view by clicking the NavRail terminal button.
 * Waits for the tab bar to be visible.
 */
export async function navigateToTerminal(page: Page) {
  await page.goto("/");
  await page.locator('button[aria-label="Terminal"]').click();
  await expect(page.getByTestId("terminal-tab-bar")).toBeVisible({
    timeout: 10000,
  });
}
