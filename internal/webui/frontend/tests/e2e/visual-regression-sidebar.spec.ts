/**
 * Visual regression screenshots for sidebar components:
 * AgentsSidebar, WorkspaceTree.
 *
 * These use window.__fixtureData for complex context data.
 * seedFixtureData must be called BEFORE page.goto().
 */

import { test, expect, waitForStableContent } from "../fixtures/screenshot";
import {
  agentsSidebarUrl,
  workspaceTreeUrl,
  seedFixtureData,
} from "../helpers/fixture-routes";

// --- AgentsSidebar -------------------------------------------------------

test.describe("Visual Regression - AgentsSidebar", () => {
  test.use({ viewport: { width: 1280, height: 720 } });

  test("expanded with agents", async ({ screenshotPage: page }) => {
    await seedFixtureData(page, {
      agents: [
        {
          name: "alpha",
          status: "working",
          branch: "feature-auth",
          task: "loom-001",
          ahead: 0,
          behind: 0,
          last_seen: "2026-01-24T12:00:00Z",
        },
        {
          name: "beta",
          status: "idle",
          branch: "main",
          task: "",
          ahead: 0,
          behind: 0,
          last_seen: "2026-01-24T11:30:00Z",
        },
        {
          name: "gamma",
          status: "error",
          branch: "bugfix-crash",
          task: "loom-003",
          ahead: 0,
          behind: 0,
          last_seen: "2026-01-24T11:00:00Z",
        },
      ],
      tasks: {
        needs_planning: 2,
        ready_to_implement: 3,
        in_progress: 1,
        need_review: 1,
        backlog: 0,
      },
      agentTasks: {
        alpha: { id: "loom-001", title: "Implement auth flow", priority: 1 },
        gamma: { id: "loom-003", title: "Fix crash on startup", priority: 0 },
      },
      taskLists: {
        needsPlanning: [],
        readyToImplement: [],
        needsReview: [],
        inProgress: [],
        backlog: [],
        done: [],
      },
    });
    await page.goto(agentsSidebarUrl());
    await waitForStableContent(page);

    await expect(page).toHaveScreenshot("agents-sidebar-expanded.png");
  });

  test("empty no agents", async ({ screenshotPage: page }) => {
    await seedFixtureData(page, {
      agents: [],
      tasks: {
        needs_planning: 0,
        ready_to_implement: 0,
        in_progress: 0,
        need_review: 0,
        backlog: 0,
      },
      agentTasks: {},
      taskLists: {
        needsPlanning: [],
        readyToImplement: [],
        needsReview: [],
        inProgress: [],
        backlog: [],
        done: [],
      },
    });
    await page.goto(agentsSidebarUrl());
    await waitForStableContent(page);

    await expect(page).toHaveScreenshot("agents-sidebar-empty.png");
  });
});

// --- WorkspaceTree -------------------------------------------------------

test.describe("Visual Regression - WorkspaceTree", () => {
  test.use({ viewport: { width: 1280, height: 720 } });

  test("single workspace", async ({ screenshotPage: page }) => {
    // Mock workspace API endpoints that useWorkspaceRepos calls
    await page.route((url) => url.pathname.startsWith("/api/workspace"), async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          repos: [
            { name: "loomcli", path: "/code/loomcli", groups: ["core"] },
          ],
        }),
      });
    });

    await seedFixtureData(page, {
      agents: [
        {
          name: "drift",
          status: "working",
          branch: "feature-x",
          task: "loom-010",
          ahead: 1,
          behind: 0,
          last_seen: "2026-01-24T12:00:00Z",
        },
      ],
      tasks: {
        needs_planning: 1,
        ready_to_implement: 2,
        in_progress: 1,
        need_review: 0,
        backlog: 0,
      },
      agentTasks: {},
      repos: [{ name: "loomcli", path: "/code/loomcli", groups: ["core"] }],
      workspace: {
        name: "default",
        id: "fixture-workspace",
        workspaces: [{ name: "default", id: "fixture-workspace" }],
      },
      wsAgents: [{ name: "drift", role: "dev", repo: "loomcli" }],
    });
    await page.goto(workspaceTreeUrl());
    await waitForStableContent(page);

    await expect(page).toHaveScreenshot("workspace-tree-single.png");
  });

  test("multi workspace with agents", async ({ screenshotPage: page }) => {
    await page.route((url) => url.pathname.startsWith("/api/workspace"), async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          repos: [
            { name: "loomcli", path: "/code/loomcli", groups: ["core"] },
            { name: "frontend", path: "/code/frontend", groups: ["web"] },
          ],
        }),
      });
    });

    await seedFixtureData(page, {
      agents: [
        {
          name: "alpha",
          status: "working",
          branch: "feat-auth",
          task: "loom-001",
          ahead: 0,
          behind: 0,
          last_seen: "2026-01-24T12:00:00Z",
        },
        {
          name: "beta",
          status: "idle",
          branch: "main",
          task: "",
          ahead: 0,
          behind: 0,
          last_seen: "2026-01-24T11:30:00Z",
        },
      ],
      tasks: {
        needs_planning: 1,
        ready_to_implement: 2,
        in_progress: 1,
        need_review: 0,
        backlog: 0,
      },
      agentTasks: {},
      repos: [
        { name: "loomcli", path: "/code/loomcli", groups: ["core"] },
        { name: "frontend", path: "/code/frontend", groups: ["web"] },
      ],
      workspace: {
        name: "default",
        id: "fixture-workspace",
        workspaces: [
          { name: "default", id: "fixture-workspace" },
          { name: "staging", id: "staging-workspace" },
        ],
      },
      wsAgents: [
        { name: "alpha", role: "dev", repo: "loomcli" },
        { name: "beta", role: "dev", repo: "frontend" },
      ],
    });
    await page.goto(workspaceTreeUrl());
    await waitForStableContent(page);

    await expect(page).toHaveScreenshot("workspace-tree-multi.png");
  });

  test("workspace with health colors", async ({ screenshotPage: page }) => {
    await page.route((url) => url.pathname.startsWith("/api/workspace"), async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          repos: [
            { name: "loomcli", path: "/code/loomcli", groups: ["core"] },
            { name: "api-server", path: "/code/api", groups: ["backend"] },
          ],
        }),
      });
    });

    await seedFixtureData(page, {
      agents: [
        {
          name: "dev1",
          status: "working",
          branch: "feature-x",
          task: "loom-001",
          ahead: 0,
          behind: 0,
          last_seen: "2026-01-24T12:00:00Z",
        },
        {
          name: "dev2",
          status: "error",
          branch: "bugfix-y",
          task: "loom-002",
          ahead: 0,
          behind: 0,
          last_seen: "2026-01-24T11:00:00Z",
        },
        {
          name: "dev3",
          status: "idle",
          branch: "main",
          task: "",
          ahead: 3,
          behind: 0,
          last_seen: "2026-01-24T10:00:00Z",
        },
      ],
      tasks: {
        needs_planning: 0,
        ready_to_implement: 1,
        in_progress: 2,
        need_review: 1,
        backlog: 0,
      },
      agentTasks: {},
      repos: [
        { name: "loomcli", path: "/code/loomcli", groups: ["core"] },
        { name: "api-server", path: "/code/api", groups: ["backend"] },
      ],
      workspace: {
        name: "default",
        id: "fixture-workspace",
        workspaces: [{ name: "default", id: "fixture-workspace" }],
      },
      wsAgents: [
        { name: "dev1", role: "dev", repo: "loomcli" },
        { name: "dev2", role: "dev", repo: "api-server" },
        { name: "dev3", role: "dev", repo: "loomcli" },
      ],
    });
    await page.goto(workspaceTreeUrl());
    await waitForStableContent(page);

    await expect(page).toHaveScreenshot("workspace-tree-health.png");
  });
});
