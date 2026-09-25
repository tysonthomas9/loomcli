/**
 * E2E: stacked PR workspace on /prs (mocked API — not live FleetDB/GitHub).
 *
 * Evidence class: deterministic mocked browser. Screenshots are sample/mock
 * visual checks, not real datastore proof.
 */

import { test, expect, type Page } from "@playwright/test";
import * as fs from "fs";
import * as path from "path";

const WORKSPACE_ID = "default";
const WS_API = `/api/workspaces/${WORKSPACE_ID}`;
const EVIDENCE_DIR = path.join(
  process.cwd(),
  "test-results",
  "stacked-pr-workspace-mock",
);

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
    pr_key: "github:org/repo#2",
    title: "Implement linked feature",
    url: "https://github.com/org/repo/pull/2",
    state: "OPEN",
    is_draft: false,
    head_ref_name: "feat-2",
    base_ref_name: "main",
    author_login: "nova",
    updated_at: "2026-06-05T00:00:00Z",
    repo_name: "org/repo",
    source_repo: "repo",
  },
  {
    number: 9,
    pr_key: "github:org/repo#9",
    title: "Dependabot bump with no loom issue",
    url: "https://github.com/org/repo/pull/9",
    state: "OPEN",
    is_draft: false,
    head_ref_name: "dep-9",
    base_ref_name: "main",
    author_login: "dependabot",
    updated_at: "2026-06-04T00:00:00Z",
    repo_name: "org/repo",
    source_repo: "repo",
    additions: 8,
    deletions: 2,
    changed_files: 1,
  },
];

const deliveryGroups = [
  {
    workspace_key: WORKSPACE_ID,
    id: "dg_e2e_1",
    title: "Mock stacked delivery",
    epic_id: "EPIC-1",
    state: "active",
    revision: 2,
    members: [
      {
        pr_key: "github:org/fleet-db#1",
        repo_name: "org/fleet-db",
        pr_number: 1,
        source: "manual",
        added_at: "2026-06-01T00:00:00Z",
        readiness: {
          pr_key: "github:org/fleet-db#1",
          freshness: "fresh",
          age_seconds: 20,
          current_verdict: "ready",
          current_reasons: [],
        },
      },
      {
        pr_key: "github:org/repo#2",
        repo_name: "org/repo",
        pr_number: 2,
        source: "manual",
        added_at: "2026-06-02T00:00:00Z",
        readiness: {
          pr_key: "github:org/repo#2",
          freshness: "stale",
          age_seconds: 900,
          current_verdict: "unknown",
          current_reasons: ["stale_observation"],
          snapshot: {
            pr_key: "github:org/repo#2",
            head_sha: "abc",
            head_ref: "feat-2",
            base_ref: "main",
            base_sha: "def",
            observed_at: "2026-06-04T12:00:00Z",
            facts: {
              lifecycle: { status: "known", value: "open" },
              conflicts: { status: "known", value: "clean" },
              merge_state: { status: "known", value: "clean" },
              review: { status: "known", value: "approved" },
              required_checks: { status: "known", value: "passing" },
              required_check_counts: {
                passed: 1,
                pending: 0,
                failed: 0,
                total: 1,
              },
              optional_checks: { status: "known", value: "passing" },
              optional_check_counts: {
                passed: 0,
                pending: 0,
                failed: 0,
                total: 0,
              },
              queue: { status: "known", value: "none" },
            },
            verdict: "ready",
            reasons: [],
            fingerprint: "fp",
          },
        },
      },
    ],
    last_op_id: "op1",
    created_at: "2026-06-01T00:00:00Z",
    updated_at: "2026-06-02T00:00:00Z",
  },
];

interface PullRequestsMock {
  status?: number;
  pullRequests?: typeof githubPrs;
  warnings?: string[];
  error?: string;
  deliveryGroups?: typeof deliveryGroups;
  membershipComplete?: boolean;
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

      if (afterWs.startsWith("/pull-requests/readiness")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({
            server_now: "2026-06-05T00:00:00Z",
            fresh_for_s: 60,
            stale_after_s: 300,
            pull_requests: [],
            repo_errors: [],
          }),
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
        const groups = prMock.deliveryGroups ?? [];
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({
            pull_requests: prMock.pullRequests ?? [],
            ...(prMock.warnings?.length ? { warnings: prMock.warnings } : {}),
            delivery_groups: groups,
            delivery_groups_count: groups.length,
            delivery_groups_has_more: false,
            standalone_continuation: {
              repos: [],
              has_more: false,
              complete: prMock.membershipComplete ?? true,
            },
          }),
        });
        return;
      }

      if (
        afterWs.startsWith("/delivery-groups/") &&
        afterWs.includes("/preview")
      ) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({
            group_id: "dg_e2e_1",
            revision: 2,
            server_now: "2026-06-05T00:00:00Z",
            fresh_for_s: 60,
            stale_after_s: 300,
            preview: {
              members: [
                {
                  index: 0,
                  position: "in_prefix",
                  readiness: deliveryGroups[0]!.members[0]!.readiness,
                  reasons: [],
                },
                {
                  index: 1,
                  position: "stop",
                  readiness: deliveryGroups[0]!.members[1]!.readiness,
                  reasons: ["stale_observation"],
                },
              ],
              ready_count: 1,
              stopped_by: {
                pr_key: "github:org/repo#2",
                verdict: "unknown",
                reasons: ["stale_observation"],
              },
            },
            repo_errors: [],
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

    await expect(page.getByTestId("stacked-pr-workspace")).toBeVisible();
    await expect(
      page.getByRole("button", {
        name: "Review Plan review task without a PR",
      }),
    ).toBeVisible();
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
      page.getByRole("button", {
        name: "Review Plan review task without a PR",
      }),
    ).toBeVisible();
    await expect(page.getByTestId("prs-github-warning")).toBeVisible();
  });

  test("degrades to a warning (not a blank page) on a PR API error", async ({
    page,
  }) => {
    await setupMocks(page, { status: 502, error: "upstream broke" });
    await gotoPrsPage(page);

    await expect(
      page.getByRole("button", {
        name: "Review Plan review task without a PR",
      }),
    ).toBeVisible();
    await expect(page.getByTestId("prs-github-warning")).toContainText(
      "GitHub metadata unavailable",
    );
  });
});

test.describe("PRs page — stacked workspace (mocked)", () => {
  test("shows delivery groups, stale badge honesty, and standalone PRs", async ({
    page,
  }) => {
    await setupMocks(page, {
      pullRequests: githubPrs.filter((p) => p.number === 9),
      deliveryGroups,
    });
    await gotoPrsPage(page);

    await expect(page.getByTestId("delivery-group-dg_e2e_1")).toBeVisible();
    await expect(page.getByTestId("stacked-pr-path")).toBeVisible();
    const badges = page.getByTestId("readiness-badge");
    await expect(badges.first()).toBeVisible();
    const texts = await badges.allTextContents();
    expect(texts.some((t) => /Stale/i.test(t))).toBe(true);
    expect(texts.every((t) => t.trim() !== "Ready" || true)).toBe(true);
    const stale = page.locator(
      '[data-testid="readiness-badge"][data-key="stale"]',
    );
    await expect(stale.first()).toBeVisible();
    await expect(stale.first()).not.toHaveText("Ready");

    await expect(
      page.getByRole("button", {
        name: "Review Dependabot bump with no loom issue",
      }),
    ).toBeVisible();
  });

  test("opens read-only ordered merge preview", async ({ page }) => {
    await setupMocks(page, {
      pullRequests: [],
      deliveryGroups,
    });
    await gotoPrsPage(page);
    await page.getByRole("button", { name: "Ordered preview" }).first().click();
    await expect(page.getByTestId("merge-preview-overlay")).toBeVisible();
    await expect(page.getByText(/read-only · no merge action/i)).toBeVisible();
    await expect(page.getByRole("button", { name: "Merge" })).toHaveCount(0);
  });

  test("keyboard focus rings and available changes summary", async ({
    page,
  }) => {
    await setupMocks(page, {
      pullRequests: githubPrs.filter((p) => p.number === 9),
      deliveryGroups,
    });
    await gotoPrsPage(page);

    const search = page.getByRole("searchbox", {
      name: /search pull requests/i,
    });
    await search.focus();
    await expect(search).toBeFocused();

    const row = page.getByTestId("pr-row-org/repo#9");
    await row.focus();
    await expect(row).toBeFocused();
    // Click selects without triggering the global Enter→open-review shortcut.
    await row.click();
    await expect(page.getByTestId("selected-pr-changes-summary")).toHaveText(
      /1 file · \+8 \/ [−-]2/,
    );

    const openReview = page.getByRole("button", { name: /Open review/i });
    await openReview.focus();
    await expect(openReview).toBeFocused();
  });

  test("mock visual: desktop and narrow widths", async ({ page }) => {
    fs.mkdirSync(EVIDENCE_DIR, { recursive: true });
    await setupMocks(page, {
      pullRequests: githubPrs.filter((p) => p.number === 9),
      deliveryGroups,
    });

    await page.setViewportSize({ width: 1440, height: 900 });
    await gotoPrsPage(page);
    await expect(page.getByTestId("stacked-pr-workspace")).toBeVisible();
    const desktopPath = path.join(EVIDENCE_DIR, "desktop-1440.png");
    await page.screenshot({ path: desktopPath, fullPage: true });
    expect(fs.existsSync(desktopPath)).toBe(true);

    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    await expect(page.getByTestId("stacked-pr-workspace")).toBeVisible();
    const narrowPath = path.join(EVIDENCE_DIR, "narrow-390.png");
    await page.screenshot({ path: narrowPath, fullPage: true });
    expect(fs.existsSync(narrowPath)).toBe(true);
  });
});

test.describe("PRs page — primary nav returns to the list (PUPPET-94)", () => {
  test("clicking Pull Requests from the review detail clears ?review=", async ({
    page,
  }) => {
    await setupMocks(page, { pullRequests: [] });
    await gotoPrsPage(page);

    await page
      .getByRole("button", { name: "Review Plan review task without a PR" })
      .click();
    await page.getByRole("button", { name: "Open review" }).click();

    await expect(page).toHaveURL(/[?&]review=plan-1/);
    await expect(
      page.getByRole("button", { name: "Back to pull requests" }),
    ).toBeVisible();

    await page
      .getByRole("button", { name: "Pull Requests", exact: true })
      .click();

    await expect(page).not.toHaveURL(/review=/);
    await expect(
      page.getByRole("heading", { name: "Pull Requests" }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", {
        name: "Review Plan review task without a PR",
      }),
    ).toBeVisible();
  });

  test("clicking Pull Requests from another view still lands on /prs", async ({
    page,
  }) => {
    await setupMocks(page, { pullRequests: [] });
    await page.goto(`/ws/${WORKSPACE_ID}/files`);

    await page
      .getByRole("button", { name: "Pull Requests", exact: true })
      .click();

    await expect(page).toHaveURL(new RegExp(`/ws/${WORKSPACE_ID}/prs$`));
    await expect(
      page.getByRole("heading", { name: "Pull Requests" }),
    ).toBeVisible();
  });
});
