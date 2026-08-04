/**
 * E2E: PRs page — loom-first review queue with GitHub enrichment.
 *
 * Covers the regression fixes:
 *  - review-stage loom issues render even when gh returns nothing
 *  - gh failures degrade to a warning banner, not a blank error page
 *  - GitHub metadata joins to issues by owner/repo#number (URL variants OK)
 *  - GitHub PRs without a linked issue render as "Unlinked" rows
 *
 * Mocks: /api/config, /api/auth/token, /api/health, workspace-scoped routes
 * (active, data, issues, pull-requests), and the loom monitor endpoints.
 */

import { test, expect, type Page } from "@playwright/test";

const WORKSPACE_ID = "default";
const WS_API = `/api/workspaces/${WORKSPACE_ID}`;

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

const workspaceData = {
  id: WORKSPACE_ID,
  name: "default",
  path: "/tmp/test-ws",
  repos: [
    {
      name: "repo",
      path: "/repos/repo",
      default_branch: "main",
      remote: "origin",
      groups: [],
    },
  ],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: WORKSPACE_ID,
      name: "default",
      path: "/tmp/test-ws",
      active: true,
      repo_count: 1,
      is_default: true,
    },
  ],
  workspace_order: ["default"],
  default_workspace: "default",
};

const reviewIssues = [
  {
    id: "plan-1",
    title: "Plan review task without a PR",
    status: "review",
    priority: 2,
    issue_type: "task",
    created_at: "2026-06-01T10:00:00Z",
    updated_at: "2026-06-01T10:00:00Z",
  },
  {
    id: "task-2",
    title: "Code review task linked to PR",
    status: "review",
    priority: 1,
    issue_type: "task",
    // URL variant (www + trailing slash) must still join to the gh entry.
    external_ref: "https://www.github.com/org/repo/pull/2/",
    created_at: "2026-06-02T10:00:00Z",
    updated_at: "2026-06-02T10:00:00Z",
  },
  {
    id: "open-1",
    title: "Open task not in review",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-06-03T10:00:00Z",
    updated_at: "2026-06-03T10:00:00Z",
  },
];

const githubPrs = [
  {
    number: 2,
    title: "Implement linked feature",
    url: "https://github.com/org/repo/pull/2",
    state: "OPEN",
    is_draft: false,
    head_ref_name: "feat-2",
    base_ref_name: "main",
    author_login: "nova",
    updated_at: "2026-06-05T00:00:00Z",
    repo_name: "repo",
  },
  {
    number: 9,
    title: "Dependabot bump with no loom issue",
    url: "https://github.com/org/repo/pull/9",
    state: "OPEN",
    is_draft: false,
    head_ref_name: "dep-9",
    base_ref_name: "main",
    author_login: "dependabot",
    updated_at: "2026-06-04T00:00:00Z",
    repo_name: "repo",
  },
];

interface PullRequestsMock {
  status?: number;
  pullRequests?: typeof githubPrs;
  warnings?: string[];
  error?: string;
}

async function setupMocks(
  page: Page,
  prMock: PullRequestsMock = {},
): Promise<void> {
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
      body: JSON.stringify({ token: "test-token-e2e" }),
    });
  });

  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    });
  });

  await page.route(
    (url) => {
      const s = url.toString();
      return s.includes("/api/workspaces/") && !s.includes("/src/");
    },
    async (route) => {
      const url = route.request().url();

      if (url.includes("/api/workspaces/active")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(workspaceData),
        });
        return;
      }

      if (url.includes(WS_API + "/events")) {
        await route.abort();
        return;
      }

      const afterWs = url.split(WS_API)[1] || "";
      if (
        afterWs === "" ||
        afterWs === "/" ||
        afterWs.startsWith("?") ||
        afterWs.startsWith("/?")
      ) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(workspaceData),
        });
        return;
      }

      if (afterWs.startsWith("/pull-requests")) {
        if (prMock.status && prMock.status >= 400) {
          await route.fulfill({
            status: prMock.status,
            contentType: "application/json",
            body: JSON.stringify({
              success: false,
              error: prMock.error ?? "bad gateway",
            }),
          });
          return;
        }
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({
            pull_requests: prMock.pullRequests ?? [],
            ...(prMock.warnings?.length ? { warnings: prMock.warnings } : {}),
          }),
        });
        return;
      }

      if (afterWs.startsWith("/issues/graph")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, issues: [] }),
        });
        return;
      }

      if (afterWs.startsWith("/ready") || afterWs.startsWith("/issues")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(reviewIssues),
        });
        return;
      }

      if (afterWs.startsWith("/stats")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({ total_issues: 3, open_issues: 1 }),
        });
        return;
      }

      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok([]),
      });
    },
  );

  await page.route("**/api/monitor/**", async (route) => {
    const url = route.request().url();
    if (url.includes("/api/monitor/agents")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ agents: [], timestamp: "2026-06-05T00:00:00Z" }),
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ agents: [], tasks: {}, agent_tasks: {} }),
    });
  });
}

async function gotoPrsPage(page: Page): Promise<void> {
  await page.goto(`/ws/${WORKSPACE_ID}/prs`);
  await expect(
    page.getByRole("heading", { name: "Pull Requests" }),
  ).toBeVisible();
}

test.describe("PRs page — loom-first rows", () => {
  test("renders review-stage issues when GitHub returns no PRs", async ({
    page,
  }) => {
    await setupMocks(page, { pullRequests: [] });
    await gotoPrsPage(page);

    await expect(
      page.getByRole("button", { name: "Review Plan review task without a PR" }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Review Code review task linked to PR" }),
    ).toBeVisible();
    // Non-review issues stay off the queue.
    await expect(
      page.getByRole("button", { name: "Review Open task not in review" }),
    ).toHaveCount(0);
  });

  test("keeps loom rows and shows a warning when gh is unavailable", async ({
    page,
  }) => {
    await setupMocks(page, {
      pullRequests: [],
      warnings: ["gh CLI not installed: install from https://cli.github.com/"],
    });
    await gotoPrsPage(page);

    await expect(
      page.getByRole("button", { name: "Review Plan review task without a PR" }),
    ).toBeVisible();
    await expect(page.getByTestId("prs-github-warning")).toContainText(
      "GitHub metadata incomplete",
    );
  });

  test("degrades to a warning (not a blank page) on a PR API error", async ({
    page,
  }) => {
    await setupMocks(page, { status: 502, error: "upstream broke" });
    await gotoPrsPage(page);

    await expect(
      page.getByRole("button", { name: "Review Plan review task without a PR" }),
    ).toBeVisible();
    await expect(page.getByTestId("prs-github-warning")).toContainText(
      "GitHub metadata unavailable",
    );
  });
});

test.describe("PRs page — GitHub enrichment", () => {
  test("joins gh metadata onto linked issues and lists unlinked PRs", async ({
    page,
  }) => {
    await setupMocks(page, { pullRequests: githubPrs });
    await gotoPrsPage(page);

    // Linked: GitHub title wins, ticket chip present, joined despite the
    // www + trailing-slash external_ref variant.
    const linked = page.getByRole("button", {
      name: "Review Implement linked feature",
    });
    await expect(linked).toBeVisible();
    await expect(linked).toContainText("#2");
    await expect(linked).toContainText("task-2");

    // Unlinked PR appears with an Unlinked chip.
    const unlinked = page.getByRole("button", {
      name: "Review Dependabot bump with no loom issue",
    });
    await expect(unlinked).toBeVisible();
    await expect(unlinked).toContainText("Unlinked");

    // Plan-review issue (no PR) still renders alongside.
    await expect(
      page.getByRole("button", { name: "Review Plan review task without a PR" }),
    ).toBeVisible();
  });
});
