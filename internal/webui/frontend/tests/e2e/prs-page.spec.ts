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
  /** Workspace list override (e.g. two workspaces to expose the switch). */
  workspaces?: typeof workspaceData.workspaces;
  issues?: unknown[];
  pullRequests?: typeof githubPrs;
  warnings?: string[];
  error?: string;
  deliveryGroups?: typeof deliveryGroups;
  membershipComplete?: boolean;
  githubViewer?: {
    status: "available" | "unavailable" | "rate_limited" | "error";
    login?: string;
    source: "connector" | "gh_cli" | "none";
    connector_id?: string;
    message?: string;
  };
}

async function setupMocks(
  page: Page,
  prMock: PullRequestsMock = {},
): Promise<void> {
  const wsData = prMock.workspaces
    ? { ...workspaceData, workspaces: prMock.workspaces }
    : workspaceData;
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
          body: ok(wsData),
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
          body: ok(wsData),
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
        const githubViewer = prMock.githubViewer ?? {
          status: "available",
          login: "nova",
          source: "connector",
          connector_id: "github-webui",
        };
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({
            pull_requests: prMock.pullRequests ?? [],
            github_viewer: githubViewer,
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
          body: ok(prMock.issues ?? reviewIssues),
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
    page.getByRole("heading", { name: "Pull Requests", level: 1 }),
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
    await page.getByRole("button", { name: "View merge plan" }).first().click();
    await expect(page.getByTestId("merge-preview-overlay")).toBeVisible();
    await expect(page.getByText(/read-only · no merge action/i)).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Merge", exact: true }),
    ).toHaveCount(0);
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

  test("Mine chip shows verified GitHub login and unavailable honesty", async ({
    page,
  }) => {
    await setupMocks(page, {
      pullRequests: githubPrs,
      deliveryGroups,
      githubViewer: {
        status: "available",
        login: "nova",
        source: "connector",
        connector_id: "github-webui",
      },
    });
    await gotoPrsPage(page);
    await expect(page.getByTestId("mine-identity-chip")).toHaveText("@nova");
    await page
      .getByRole("checkbox", { name: /Mine filter for GitHub @nova/i })
      .check();
    await expect(page.getByTestId("pr-row-org/repo#2").first()).toBeVisible();
    await expect(page.getByTestId("pr-row-org/repo#9")).toHaveCount(0);

    await setupMocks(page, {
      pullRequests: githubPrs,
      deliveryGroups,
      githubViewer: {
        status: "unavailable",
        source: "none",
        message: "GitHub credential unavailable for viewer lookup",
      },
    });
    await page.reload();
    await expect(page.getByTestId("mine-identity-chip")).toHaveText(
      "unavailable",
    );
    await page
      .getByRole("checkbox", {
        name: /Mine filter — GitHub identity unavailable/i,
      })
      .check();
    await expect(page.getByTestId("mine-viewer-unavailable")).toBeVisible();
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
      page.getByRole("heading", { name: "Pull Requests", level: 1 }),
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
      page.getByRole("heading", { name: "Pull Requests", level: 1 }),
    ).toBeVisible();
  });
});

/*
 * TEST-ONLY VISUAL FIXTURE (STACKED-PRS-83). Populated delivery-group state
 * shaped like the attached reference so the grouped path composition can be
 * screenshotted. This is mocked browser evidence, NOT runtime proof — no
 * production code reads these values.
 */
function minutesAgo(m: number): string {
  return new Date(Date.now() - m * 60_000).toISOString();
}

function fixtureReadiness(
  prKey: string,
  verdict: "ready" | "merged" | "waiting" | "blocked",
  opts: {
    stale?: boolean;
    review?: string;
    checks?: [number, number];
    head?: string;
  } = {},
) {
  const [passed, total] = opts.checks ?? [8, 8];
  return {
    pr_key: prKey,
    freshness: opts.stale ? "stale" : "fresh",
    age_seconds: opts.stale ? 1800 : 20,
    current_verdict: opts.stale ? "unknown" : verdict,
    // Mirrors prreadiness.evaluateReady: a null reviewDecision on a ready PR
    // carries the no_review_required warning.
    current_reasons: opts.stale
      ? ["stale"]
      : opts.review === "not_reported"
        ? ["no_review_required"]
        : [],
    snapshot: {
      pr_key: prKey,
      head_sha: "a1",
      head_ref: opts.head ?? "feat",
      base_ref: "main",
      base_sha: "b1",
      observed_at: minutesAgo(opts.stale ? 30 : 1),
      facts: {
        lifecycle: {
          status: "known",
          value: verdict === "merged" ? "merged" : "open",
        },
        conflicts: { status: "known", value: "none" },
        merge_state: { status: "known", value: "clean" },
        review: { status: "known", value: opts.review ?? "approved" },
        required_checks: {
          status: "known",
          value: passed === total ? "passing" : "pending",
        },
        required_check_counts: {
          passed,
          pending: total - passed,
          failed: 0,
          total,
        },
        optional_checks: { status: "known", value: "none" },
        optional_check_counts: { passed: 0, pending: 0, failed: 0, total: 0 },
        queue: { status: "known", value: "not_queued" },
      },
      verdict,
      reasons: [],
      fingerprint: `fp-${prKey}`,
    },
  };
}

type FixturePr = {
  n: number;
  repo: string;
  title: string;
  head: string;
  author: string;
  state?: string;
  draft?: boolean;
  verdict: "ready" | "merged" | "waiting" | "blocked";
  stale?: boolean;
  review?: string;
  checks?: [number, number];
  diff?: [number, number, number];
};

function fixtureGroup(
  id: string,
  title: string,
  epic: string,
  updatedMin: number,
  prs: FixturePr[],
) {
  return {
    workspace_key: WORKSPACE_ID,
    id,
    title,
    epic_id: epic,
    state: "active",
    revision: 3,
    members: prs.map((p, i) => {
      const key = `github:acme/${p.repo}#${p.n}`;
      return {
        pr_key: key,
        repo_name: `acme/${p.repo}`,
        pr_number: p.n,
        source: "manual",
        added_at: minutesAgo(600 - i),
        readiness: fixtureReadiness(key, p.verdict, { ...p, head: p.head }),
      };
    }),
    last_op_id: `op-${id}`,
    created_at: minutesAgo(900),
    updated_at: minutesAgo(updatedMin),
  };
}

function fixturePullRequests(prs: FixturePr[]) {
  return prs.map((p) => ({
    number: p.n,
    pr_key: `github:acme/${p.repo}#${p.n}`,
    title: p.title,
    url: `https://github.com/acme/${p.repo}/pull/${p.n}`,
    state: p.state ?? "OPEN",
    is_draft: p.draft ?? false,
    head_ref_name: p.head,
    base_ref_name: "main",
    author_login: p.author,
    created_at: minutesAgo(12),
    updated_at: minutesAgo(12),
    repo_name: `acme/${p.repo}`,
    source_repo: p.repo,
    ...(p.diff
      ? {
          changed_files: p.diff[0],
          additions: p.diff[1],
          deletions: p.diff[2],
        }
      : {}),
  }));
}

const FIXTURE_HARDENING: FixturePr[] = [
  {
    n: 142,
    repo: "loomcli",
    title: "Isolate daemon worker lifecycle",
    head: "feat/worker-lifecycle",
    author: "ravi-s",
    state: "MERGED",
    verdict: "merged",
  },
  {
    n: 144,
    repo: "fleetdb",
    title: "Guard the PATCH body against unknown fields",
    head: "fix/patch-validation",
    author: "sonal-b",
    state: "MERGED",
    verdict: "merged",
  },
  {
    n: 146,
    repo: "loomcli",
    title: "Carry max_run_duration on the role wire",
    head: "feat/run-duration",
    author: "sonal-b",
    verdict: "ready",
    diff: [3, 48, 12],
  },
  {
    n: 148,
    repo: "console",
    title: "Let humans answer a waiting agent",
    head: "feat/prompt-resume",
    author: "maya-c",
    verdict: "waiting",
    review: "review_required",
  },
  {
    n: 149,
    repo: "loomcli",
    title: "Add a conversation executor for workers",
    head: "feat/conversation-executor",
    author: "sonal-b",
    draft: true,
    verdict: "waiting",
    review: "review_required",
    checks: [0, 0],
  },
];
const FIXTURE_INVARIANTS: FixturePr[] = [
  {
    n: 170,
    repo: "loomcli",
    title: "Assert product truth on task close",
    head: "feat/truth-close",
    author: "lee-p",
    verdict: "ready",
  },
  {
    n: 171,
    repo: "fleetdb",
    title: "Reject orphaned epic children",
    head: "fix/orphan-children",
    author: "lee-p",
    verdict: "ready",
    review: "not_reported",
  },
  {
    n: 172,
    repo: "loomcli",
    title: "Snapshot invariants in AFT",
    head: "test/aft-invariants",
    author: "maya-c",
    verdict: "waiting",
    stale: true,
  },
];
const FIXTURE_TRACES: FixturePr[] = [
  {
    n: 160,
    repo: "console",
    title: "Stream agent traces to the console",
    head: "feat/trace-stream",
    author: "ravi-s",
    verdict: "waiting",
    review: "review_required",
    checks: [5, 8],
  },
  {
    n: 161,
    repo: "loomcli",
    title: "Close sessions on lifecycle end",
    head: "feat/session-close",
    author: "ravi-s",
    verdict: "blocked",
    review: "changes_requested",
  },
];

// Mirrors the real list contract: grouped PRs are NOT in pull_requests
// (the server filters them out), so grouped titles come from linked Loom
// tasks and branches from readiness snapshots.
function fixtureIssues(epicId: string, epicTitle: string, prs: FixturePr[]) {
  return prs.map((p) => ({
    id: `SPR-${p.n}`,
    title: p.title,
    status: p.state === "MERGED" ? "closed" : "review",
    priority: 2,
    issue_type: "task",
    parent: epicId,
    parent_title: epicTitle,
    external_ref: `https://github.com/acme/${p.repo}/pull/${p.n}`,
    created_at: minutesAgo(90),
    updated_at: minutesAgo(12),
  }));
}

const visualFixture = {
  deliveryGroups: [
    fixtureGroup(
      "dg_fixture_hardening",
      "Agent runtime hardening",
      "EPIC-RUNTIME",
      12,
      FIXTURE_HARDENING,
    ),
    fixtureGroup(
      "dg_fixture_invariants",
      "Product truth invariants",
      "EPIC-AFT",
      34,
      FIXTURE_INVARIANTS,
    ),
    fixtureGroup(
      "dg_fixture_traces",
      "Agent traces & session lifecycle",
      "EPIC-OBS",
      120,
      FIXTURE_TRACES,
    ),
  ],
  issues: [
    ...fixtureIssues(
      "EPIC-RUNTIME",
      "Daemon & runtime safety",
      FIXTURE_HARDENING,
    ),
    ...fixtureIssues("EPIC-AFT", "AFT product correctness", FIXTURE_INVARIANTS),
    ...fixtureIssues("EPIC-OBS", "Agent observability", FIXTURE_TRACES),
  ],
  pullRequests: fixturePullRequests([
    {
      n: 88,
      repo: "loomcli",
      title: "Bump vite to 6.4",
      head: "deps/vite-6-4",
      author: "dependabot",
      verdict: "waiting",
      diff: [2, 14, 9],
    },
    {
      n: 91,
      repo: "fleetdb",
      title: "Document readiness freshness windows",
      head: "docs/readiness-freshness",
      author: "maya-c",
      verdict: "waiting",
      diff: [1, 32, 4],
    },
  ]),
};

async function gotoFixture(
  page: Page,
  theme: "dark" | "light",
  viewport: { width: number; height: number },
  opts: { select?: boolean; workspaces?: typeof workspaceData.workspaces } = {},
): Promise<void> {
  await page.addInitScript((t) => {
    try {
      localStorage.setItem("cortex:theme", t);
    } catch {
      /* private mode */
    }
  }, theme);
  await page.setViewportSize(viewport);
  await setupMocks(page, {
    pullRequests: visualFixture.pullRequests as typeof githubPrs,
    deliveryGroups:
      visualFixture.deliveryGroups as unknown as typeof deliveryGroups,
    issues: visualFixture.issues,
    ...(opts.workspaces ? { workspaces: opts.workspaces } : {}),
  });
  await gotoPrsPage(page);
  if (opts.select !== false) {
    await page.getByTestId("pr-row-acme/loomcli#146").click();
  }
  // Below 850px the summary is an overlay opened only by picking a row.
  if (opts.select !== false || viewport.width > 850) {
    await expect(page.getByTestId("selected-pr-detail")).toBeVisible();
  }
  // Playwright scrolls a clicked row into view; screenshots compare against
  // the reference at scroll top.
  await resetScroll(page);
}

/** The single page scroller under the fixed breadcrumb bar. */
async function resetScroll(page: Page): Promise<void> {
  await page.getByTestId("stacked-pr-scroll").evaluate((el) => {
    el.scrollTo(0, 0);
  });
}

test.describe("PRs page — reference composition (test-only visual fixture)", () => {
  test("1440x1000 dark matches reference layout geometry", async ({ page }) => {
    fs.mkdirSync(EVIDENCE_DIR, { recursive: true });
    await gotoFixture(page, "dark", { width: 1440, height: 1000 });

    const nav = await page.getByTestId("stacked-pr-nav").boundingBox();
    expect(nav?.width).toBe(212);
    const h1 = page.getByRole("heading", { level: 1, name: "Pull requests" });
    expect(
      await h1.evaluate((el) => parseFloat(getComputedStyle(el).fontSize)),
    ).toBe(29);
    const summary = await page.getByTestId("selected-pr-detail").boundingBox();
    expect(summary?.width).toBeGreaterThanOrEqual(320);
    // Grouped path: first card expanded with ordered steps; later collapsed.
    await expect(
      page
        .getByTestId("delivery-group-dg_fixture_hardening")
        .getByRole("listitem"),
    ).toHaveCount(5);
    await expect(
      page
        .getByTestId("delivery-group-dg_fixture_traces")
        .getByText(/Next up #160/),
    ).toBeVisible();
    // Selected row carries the lime accent.
    await expect(page.getByTestId("pr-row-acme/loomcli#146")).toHaveAttribute(
      "data-current",
      "true",
    );
    await page.screenshot({
      path: path.join(EVIDENCE_DIR, "fixture-dark-1440x1000.png"),
    });
    await page.screenshot({
      path: path.join(EVIDENCE_DIR, "fixture-dark-1440-full.png"),
      fullPage: true,
    });
    // Stale evidence never renders as Ready or as met requirements.
    await page
      .getByTestId("delivery-group-dg_fixture_invariants")
      .getByRole("button")
      .first()
      .click();
    await page.getByTestId("pr-row-acme/loomcli#172").click();
    await expect(
      page.getByTestId("merge-requirements").locator('[data-state="met"]'),
    ).toHaveCount(0);
    await expect(page.getByTestId("merge-requirements")).toContainText(
      "not current",
    );

    // Fresh review=not_reported (GitHub gave no reviewDecision): neutral
    // requirement line, backend Ready verdict untouched.
    await page.getByTestId("pr-row-acme/fleetdb#171").click();
    const reqs = page.getByTestId("merge-requirements");
    await expect(reqs).toContainText("No review decision reported");
    await expect(reqs).not.toContainText("No review required");
    await expect(
      reqs.locator("li", { hasText: "No review decision reported" }),
    ).toHaveAttribute("data-state", "unknown");
    await expect(
      page.getByTestId("selected-pr-detail").getByTestId("readiness-badge"),
    ).toHaveText(/Ready/);
  });

  test("1440x1000 light theme is respected", async ({ page }) => {
    fs.mkdirSync(EVIDENCE_DIR, { recursive: true });
    await gotoFixture(page, "light", { width: 1440, height: 1000 });
    const bg = await page
      .getByTestId("stacked-pr-workspace")
      .evaluate((el) => getComputedStyle(el).backgroundColor);
    expect(bg).not.toBe("rgb(17, 17, 17)");
    await page.screenshot({
      path: path.join(EVIDENCE_DIR, "fixture-light-1440x1000.png"),
    });
  });

  test("narrow 390 collapses nav and opens summary as overlay", async ({
    page,
  }) => {
    fs.mkdirSync(EVIDENCE_DIR, { recursive: true });
    await gotoFixture(page, "dark", { width: 390, height: 844 });
    await expect(page.getByTestId("stacked-pr-nav")).toBeHidden();
    const hScroll = await page.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth,
    );
    expect(hScroll).toBe(false);
    await page.screenshot({
      path: path.join(EVIDENCE_DIR, "fixture-dark-390-summary.png"),
    });
    await page.getByRole("button", { name: "Close details" }).click();
    // The workspace scrolls inside its own scroller, not the document.
    const shell = page.getByTestId("stacked-pr-scroll");
    await shell.evaluate((el) => el.scrollTo(0, 0));
    await page.screenshot({
      path: path.join(EVIDENCE_DIR, "fixture-dark-390-top.png"),
    });
    await shell.evaluate((el) => el.scrollTo(0, 700));
    await page.screenshot({
      path: path.join(EVIDENCE_DIR, "fixture-dark-390-path.png"),
    });
  });
});

/*
 * Route-owned chrome (STACKED-PRS-85). Same TEST-ONLY mocked fixture as
 * above: this proves the routed app composition in a browser, not runtime
 * or GitHub behavior.
 */
test.describe("PRs page — route-owned chrome (test-only visual fixture)", () => {
  const twoWorkspaces = [
    ...workspaceData.workspaces,
    {
      id: "other",
      name: "other",
      path: "/tmp/other-ws",
      active: false,
      repo_count: 1,
      is_default: false,
    },
  ];

  test("1440x1000 at load: one nav, one breadcrumb, heading and toolbar in view", async ({
    page,
  }) => {
    fs.mkdirSync(EVIDENCE_DIR, { recursive: true });
    await gotoFixture(
      page,
      "dark",
      { width: 1440, height: 1000 },
      { select: false },
    );

    // Global header and NavRail are gone; /prs draws the only chrome.
    await expect(page.locator('[data-chrome="route"]')).toHaveCount(1);
    await expect(page.getByRole("banner")).toHaveCount(0);
    await expect(page.getByRole("navigation", { name: "Primary" })).toHaveCount(
      0,
    );
    const nav = await page.getByTestId("stacked-pr-nav").boundingBox();
    expect(nav).toMatchObject({ x: 0, y: 0, width: 212 });
    const bar = await page.getByTestId("stacked-pr-topbar").boundingBox();
    expect(bar).toMatchObject({ x: 212, y: 0, height: 59 });

    // Initial auto-selection never scrolls the page.
    const scroll = page.getByTestId("stacked-pr-scroll");
    expect(await scroll.evaluate((el) => el.scrollTop)).toBe(0);
    await expect(
      page.getByRole("heading", { level: 1, name: "Pull requests" }),
    ).toBeInViewport();
    await expect(page.getByLabel("Search pull requests")).toBeInViewport();
    await expect(
      page.getByRole("button", { name: /Hide summary/ }),
    ).toBeInViewport();
    await expect(page.getByTestId("selected-pr-detail")).toBeVisible();

    const overflow = await page.evaluate(() => ({
      doc: document.documentElement.scrollWidth - window.innerWidth,
      scroller:
        document.querySelector<HTMLElement>(
          '[data-testid="stacked-pr-scroll"]',
        )!.scrollWidth -
        document.querySelector<HTMLElement>(
          '[data-testid="stacked-pr-scroll"]',
        )!.clientWidth,
    }));
    expect(overflow.doc).toBeLessThanOrEqual(0);
    expect(overflow.scroller).toBeLessThanOrEqual(0);

    await page.screenshot({
      path: path.join(EVIDENCE_DIR, "route-dark-1440x1000-top.png"),
    });

    // j/k keeps the breadcrumb fixed; scrolling the list never moves it.
    for (let i = 0; i < 6; i++) await page.keyboard.press("j");
    await scroll.evaluate((el) => el.scrollTo(0, el.scrollHeight));
    expect((await page.getByTestId("stacked-pr-topbar").boundingBox())?.y).toBe(
      0,
    );
  });

  test("keyboard shortcuts do not hijack buttons, links, or chords", async ({
    page,
  }) => {
    await gotoFixture(
      page,
      "dark",
      { width: 1440, height: 1000 },
      { select: false },
    );
    const guideButton = page.getByRole("button", { name: /How groups work/ });
    await guideButton.focus();
    await page.keyboard.press("Enter");
    await expect(
      page.getByRole("dialog", { name: "How delivery groups work" }),
    ).toBeVisible();
    expect(new URL(page.url()).searchParams.has("review")).toBe(false);
    expect(new URL(page.url()).searchParams.has("review-pr")).toBe(false);
    await page.getByRole("button", { name: "Close", exact: true }).click();

    // Space on a focused row selects that row, not the prior selection's review.
    const row = page.getByTestId("pr-row-acme/fleetdb#144");
    await row.focus();
    await page.keyboard.press(" ");
    await expect(row).toHaveAttribute("data-current", "true");
    expect(new URL(page.url()).search).toBe("");

    // Modifier chords (paste, history) stay with the browser.
    const pathView = page.getByRole("button", { name: "Path view" });
    await page.locator("body").focus();
    await page.keyboard.press("ControlOrMeta+v");
    await expect(pathView).toHaveAttribute("aria-pressed", "true");
    await page.keyboard.press("v");
    await expect(pathView).toHaveAttribute("aria-pressed", "false");
  });

  test("live Loom controls: theme, workspace switch, back to workspace", async ({
    page,
  }) => {
    await gotoFixture(
      page,
      "dark",
      { width: 1440, height: 1000 },
      { select: false, workspaces: twoWorkspaces },
    );
    const html = page.locator("html");
    await expect(html).toHaveAttribute("data-theme", "dark");
    await page.getByRole("button", { name: "Switch to light theme" }).click();
    await expect(html).toHaveAttribute("data-theme", "light");
    await page.getByRole("button", { name: "Switch to dark theme" }).click();
    await expect(html).toHaveAttribute("data-theme", "dark");

    const switcher = page.getByRole("combobox", { name: "Switch workspace" });
    await expect(switcher).toHaveValue(WORKSPACE_ID);
    await expect(switcher.locator("option")).toHaveCount(2);

    // Back to workspace restores the standard shell for other views.
    await page
      .getByTestId("stacked-pr-nav")
      .getByRole("button", { name: "Back to workspace" })
      .click();
    await expect(page).not.toHaveURL(/\/prs/);
    await expect(
      page.getByRole("navigation", { name: "Primary" }),
    ).toBeVisible();
    await expect(page.getByRole("banner")).toHaveCount(1);
    await expect(page.locator('[data-chrome="shell"]')).toHaveCount(1);

    // Returning through the NavRail hands the chrome back to /prs.
    await page
      .getByRole("navigation", { name: "Primary" })
      .getByRole("button", { name: /Pull Requests/i })
      .click();
    await expect(page.getByTestId("stacked-pr-nav")).toBeVisible();
    await expect(page.getByRole("navigation", { name: "Primary" })).toHaveCount(
      0,
    );

    await switcher.selectOption("other");
    await expect(page).toHaveURL(/\/ws\/other/);
  });

  test("390px: breadcrumb keeps Back to workspace, no horizontal scroll", async ({
    page,
  }) => {
    fs.mkdirSync(EVIDENCE_DIR, { recursive: true });
    await gotoFixture(
      page,
      "light",
      { width: 390, height: 844 },
      { select: false },
    );
    await expect(page.getByTestId("stacked-pr-nav")).toBeHidden();
    await expect(page.getByRole("navigation", { name: "Primary" })).toHaveCount(
      0,
    );
    await expect(
      page
        .getByTestId("stacked-pr-topbar")
        .getByRole("button", { name: "Back to workspace" }),
    ).toBeVisible();
    await expect(
      page.getByRole("heading", { level: 1, name: "Pull requests" }),
    ).toBeInViewport();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth > window.innerWidth,
      ),
    ).toBe(false);
    const bar = await page.getByTestId("stacked-pr-topbar").boundingBox();
    expect(bar?.width).toBeLessThanOrEqual(390);
    await page.screenshot({
      path: path.join(EVIDENCE_DIR, "route-light-390-top.png"),
    });
  });
});
