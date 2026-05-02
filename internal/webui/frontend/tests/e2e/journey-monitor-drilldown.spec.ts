import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

/**
 * E2E Journey: Monitor dashboard drilldown.
 *
 * Tests the monitor workflow:
 *   1. Navigate to monitor view, verify dashboard renders
 *   2. Verify project health panel with completion stats
 *   3. Verify agent activity panel with agent cards
 *   4. Click a bottleneck issue to open detail panel
 *   5. Close panel and verify monitor state is preserved
 */

const WS_ID = "ws-monitor";

const WORKSPACE_DATA = {
  id: WS_ID, name: "Monitor Test Workspace", path: "/tmp/ws-monitor",
  repos: [], groups: [], agents: [],
  workspaces: [{ id: WS_ID, name: "Monitor Test Workspace", path: "/tmp/ws-monitor", active: true, repo_count: 1, is_default: true }],
  workspace_order: [WS_ID], default_workspace: WS_ID,
};

const ISSUES = [
  { id: "mon-001", title: "API rate limiting", status: "open", priority: 1, issue_type: "task", created_at: "2026-01-15T10:00:00Z", updated_at: "2026-01-15T10:00:00Z" },
  { id: "mon-003", title: "Database connection pooling", status: "open", priority: 0, issue_type: "bug", created_at: "2026-01-15T10:00:00Z", updated_at: "2026-01-15T10:00:00Z" },
  { id: "mon-004", title: "Implement caching layer", status: "in_progress", priority: 1, issue_type: "feature", assignee: "alpha", created_at: "2026-01-15T10:00:00Z", updated_at: "2026-01-15T10:00:00Z" },
  { id: "mon-005", title: "Blocked on DB schema", status: "blocked", priority: 1, issue_type: "task", depends_on: ["mon-003"], created_at: "2026-01-15T10:00:00Z", updated_at: "2026-01-15T10:00:00Z" },
  { id: "mon-006", title: "Initial setup complete", status: "closed", priority: 3, issue_type: "task", created_at: "2026-01-15T10:00:00Z", updated_at: "2026-01-15T10:00:00Z" },
];

const BLOCKED_ISSUES = [
  { id: "mon-005", title: "Blocked on DB schema", status: "blocked", priority: 1, blocked_by_count: 1, blocked_by: ["mon-003"], blocked_by_details: [{ id: "mon-003", title: "Database connection pooling", priority: 0 }] },
  { id: "mon-001", title: "API rate limiting", status: "blocked", priority: 1, blocked_by_count: 1, blocked_by: ["mon-003"], blocked_by_details: [{ id: "mon-003", title: "Database connection pooling", priority: 0 }] },
];

const AGENTS = [
  { name: "alpha", status: "working: mon-004 (2m)", branch: "feature-cache", path: "/tmp/worktrees/alpha", repo: "loomcli", cross_repo: false, ahead: 2, behind: 0, role: "task", commits: [], changes: [] },
  { name: "beta", status: "idle", branch: "main", path: "/tmp/worktrees/beta", repo: "loomcli", cross_repo: false, ahead: 0, behind: 0, role: "task", commits: [], changes: [] },
  { name: "gamma", status: "planning: mon-001 (30s)", branch: "feature-rate-limit", path: "/tmp/worktrees/gamma", repo: "loomcli", cross_repo: false, ahead: 1, behind: 0, role: "plan", commits: [], changes: [] },
];

const LOOM_STATUS = {
  agents: AGENTS,
  tasks: { needs_planning: 1, ready_to_implement: 1, in_progress: 1, need_review: 0, backlog: 1 },
  in_progress_list: [{ id: "mon-004", title: "Implement caching layer", priority: 1 }],
  agent_tasks: { alpha: { id: "mon-004", title: "Implement caching layer", priority: 1 }, gamma: { id: "mon-001", title: "API rate limiting", priority: 1 } },
  stats: { open: 2, closed: 1, total: 5, completion: 20, remaining: 4, in_progress: 1, review: 0, blocked: 1 },
  sync: { db_synced: true, db_last_sync: "2026-01-15T10:00:00Z", git_needs_push: 0, git_needs_pull: 0, git_push_details: [] },
  timestamp: "2026-01-15T10:00:00Z",
};

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

async function setupMocks(page: Page) {
  await page.route("**/api/config", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname !== "/api/config") { await route.fallback(); return; }
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ mode: "open" }) });
  });
  await page.route("**/api/backends", async (route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: ok([{ name: "claude", available: true, display_name: "Claude" }]) });
  });
  await page.route("**/api/config/backend", async (route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: ok({ backend: "claude", source: "workspace", available: ["claude"], agents: [] }) });
  });
  await page.route("**/api/health", async (route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ status: "ok", daemon: true }) });
  });

  // Workspace sub-routes
  await page.route("**/api/workspaces/**", async (route) => {
    const url = route.request().url();
    const method = route.request().method();

    if (url.includes("/api/workspaces/active")) {
      await route.fulfill({ status: 200, contentType: "application/json", body: ok(WORKSPACE_DATA) });
      return;
    }
    if (url.includes("/events")) { await route.abort(); return; }
    if (url.includes(WS_ID + "/config/backend")) {
      await route.fulfill({ status: 200, contentType: "application/json", body: ok({ backend: "claude", source: "workspace", available: ["claude"], agents: [] }) });
      return;
    }
    // Issue sub-resources
    if (url.includes("/issues/") && method === "GET") {
      const afterIssues = url.split("/issues/")[1] ?? "";
      const pathParts = afterIssues.split("?")[0].split("/");
      if (pathParts.length > 1 && pathParts[1]) {
        const sub = pathParts[1];
        await route.fulfill({ status: 200, contentType: "application/json", body: ok(sub === "tabs" ? null : []) });
        return;
      }
      // Single issue detail
      if (pathParts[0] === "mon-003") {
        await route.fulfill({ status: 200, contentType: "application/json", body: ok({ ...ISSUES[1], description: "Fix the DB pooling issue.", labels: [], dependencies: [], dependents: [], comments: [], events: [] }) });
        return;
      }
    }
    if (url.includes("/issues") && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: ok(ISSUES) });
      return;
    }
    if (url.includes("/ready") && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: ok(ISSUES) });
      return;
    }
    if (url.includes("/blocked")) {
      await route.fulfill({ status: 200, contentType: "application/json", body: ok(BLOCKED_ISSUES) });
      return;
    }
    if (url.includes("/stats")) {
      await route.fulfill({ status: 200, contentType: "application/json", body: ok({ total_issues: 5, open_issues: 2, in_progress_issues: 1, closed_issues: 1, blocked_issues: 1, deferred_issues: 0, ready_issues: 2, tombstone_issues: 0, pinned_issues: 0, epics_eligible_for_closure: 0, average_lead_time_hours: 24 }) });
      return;
    }
    if (url.includes("/terminal/tabs")) { await route.fulfill({ status: 200, contentType: "application/json", body: ok([]) }); return; }
    if (url.includes("/terminal/state")) { await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ active_tab: "" }) }); return; }
    if (url.includes("/terminal/sessions/by-issue")) { await route.fulfill({ status: 200, contentType: "application/json", body: ok({}) }); return; }
    await route.fulfill({ status: 200, contentType: "application/json", body: ok(WORKSPACE_DATA) });
  });

  // Monitor catch-all (lowest priority)
  await page.route("**/api/monitor/**", async (route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({}) });
  });
  // Health endpoint
  await page.route("**/health", async (route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ status: "ok" }) });
  });
  // Specific monitor endpoints (higher priority — LIFO)
  await page.route("**/api/monitor/agents", async (route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ agents: AGENTS, timestamp: "2026-01-15T10:00:00Z" }) });
  });
  await page.route("**/api/monitor/status", async (route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(LOOM_STATUS) });
  });
  await page.route("**/api/monitor/tasks", async (route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ summary: LOOM_STATUS.tasks, needs_planning: [], ready_to_implement: [], needs_review: [], in_progress: [], backlog: [], closed: [], timestamp: "2026-01-15T10:00:00Z" }) });
  });
  await page.route("**/api/monitor/usage**", async (route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({
      total_input_tokens: 0, total_output_tokens: 0, total_cache_read_tokens: 0,
      total_cache_write_tokens: 0, total_cost: 0, session_count: 0,
      by_agent: [], by_backend: [], daily_costs: [], sessions: [],
      timestamp: "2026-01-15T10:00:00Z",
    }) });
  });
}

test.describe("E2E Journey: Monitor dashboard drilldown", () => {
  test.describe.configure({ mode: "serial" });

  let page: Page;

  test.beforeAll(async ({ browser }) => {
    page = await browser.newPage();
    await setupMocks(page);
    await page.goto(`/ws/${WS_ID}/monitor`, { waitUntil: "domcontentloaded" });
  });

  test.afterAll(async () => {
    await page.close();
  });

  test("Monitor dashboard renders", async () => {
    await expect(page.getByTestId("monitor-dashboard")).toBeVisible({ timeout: 15000 });
  });

  test("Verify project health panel with completion stats", async () => {
    await expect(page.getByTestId("project-health-panel")).toBeVisible();
    await expect(page.getByTestId("project-health-panel")).toContainText("20%");
    await expect(page.getByTestId("project-health-panel")).toContainText("Remaining");
  });

  test("Verify agent activity panel", async () => {
    const agentPanel = page.getByTestId("agent-activity-panel");
    await expect(agentPanel).toBeVisible({ timeout: 10000 });
    // Summary bar shows "N active · N idle" — use .first() to avoid strict mode
    await expect(agentPanel.locator("[data-type='active']").first()).toBeVisible({ timeout: 10000 });
    await expect(agentPanel.locator("[data-type='idle']").first()).toBeVisible();
  });

  test("Click bottleneck issue to open detail panel", async () => {
    const healthPanel = page.getByTestId("project-health-panel");
    await expect(healthPanel.getByText("Bottlenecks")).toBeVisible({ timeout: 10000 });
    await expect(healthPanel.getByText("mon-003")).toBeVisible({ timeout: 5000 });
    await healthPanel.getByText("mon-003").click();

    const detailPanel = page.getByTestId("issue-detail-panel");
    await expect(detailPanel).toHaveAttribute("data-state", "open", { timeout: 5000 });
    await expect(page.getByTestId("issue-id")).toContainText("mon-003");
  });

  test("Close panel and verify monitor state preserved", async () => {
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("issue-detail-panel")).toHaveAttribute("data-state", "closed");
    await expect(page.getByTestId("monitor-dashboard")).toBeVisible();
    await expect(page.getByTestId("project-health-panel")).toBeVisible();
    await expect(page.getByTestId("agent-activity-panel")).toBeVisible();
  });
});
