/**
 * Harness-only browser regression for agent transcript switching.
 *
 * This test runs the production Agents page against Playwright route mocks. It
 * deliberately keeps canonical task-session metadata pending while asserting
 * that history-derived transcript routes and rendered bodies stay scoped to
 * the selected agent across A → B → C navigation. It does not prove a live
 * Loom/FleetDB deployment.
 */

import { expect, test, type Page } from "@playwright/test";

const WORKSPACE_ID = "default";
const WORKSPACE_API = `/api/workspaces/${WORKSPACE_ID}`;

const SWITCH_CASES = [
  {
    name: "switch-a",
    taskId: "TASK-A",
    sessionId: "session-a",
    body: "Transcript body for switch-a",
  },
  {
    name: "switch-b",
    taskId: "TASK-B",
    sessionId: "session-b",
    body: "Transcript body for switch-b",
  },
  {
    name: "switch-c",
    taskId: "TASK-C",
    sessionId: "session-c",
    body: "Transcript body for switch-c",
  },
] as const;

type SwitchCase = (typeof SWITCH_CASES)[number];

interface TranscriptHarnessOptions {
  /**
   * Hold one task transcript request until the test releases it. This lets the
   * browser prove that an old agent response cannot render after navigation.
   */
  holdTranscriptForTaskId?: string;
  onHeldTranscriptRequest?: (() => void) | undefined;
  waitForHeldTranscript?: Promise<void> | undefined;
  /**
   * Fail the first transcript read for one task with a transient server error.
   * The production hook must recover without requiring another navigation.
   */
  failFirstTranscriptForTaskId?: string;
}

const monitorAgents = SWITCH_CASES.map(({ name }) => ({
  name,
  branch: "",
  status: "ready",
  ahead: 0,
  behind: 0,
  role: "task",
  workspace: "",
  repo: "",
}));

const workspaceData = {
  id: WORKSPACE_ID,
  name: WORKSPACE_ID,
  path: "/tmp/transcript-switching",
  repos: [
    {
      name: "loomcli",
      path: "/repos/loomcli",
      default_branch: "main",
      remote: "origin",
      groups: [],
    },
  ],
  groups: [],
  agents: SWITCH_CASES.map(({ name }) => ({
    name,
    repos: [],
    repo_groups: [],
    cross_repo: false,
  })),
  workspaces: [
    {
      id: WORKSPACE_ID,
      name: WORKSPACE_ID,
      path: "/tmp/transcript-switching",
      active: true,
      repo_count: 1,
      is_default: true,
    },
  ],
  workspace_order: [WORKSPACE_ID],
  default_workspace: WORKSPACE_ID,
};

const monitorStatus = {
  agents: monitorAgents,
  tasks: {
    needs_planning: 0,
    ready_to_implement: 0,
    in_progress: 0,
    need_review: 0,
    backlog: 0,
  },
  in_progress_list: [],
  agent_tasks: {},
  sync: {
    db_synced: true,
    db_last_sync: "2026-07-30T12:00:00Z",
    git_needs_push: 0,
    git_needs_pull: 0,
  },
  stats: {
    open: 0,
    closed: 3,
    total: 3,
    completion: 100,
    remaining: 0,
    in_progress: 0,
    review: 0,
    blocked: 0,
  },
  timestamp: "2026-07-30T12:00:00Z",
};

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

function switchCaseByAgent(agentName: string): SwitchCase | undefined {
  return SWITCH_CASES.find(({ name }) => name === agentName);
}

function switchCaseByTask(taskId: string): SwitchCase | undefined {
  return SWITCH_CASES.find((item) => item.taskId === taskId);
}

function historySession(item: SwitchCase) {
  return {
    workspace_key: WORKSPACE_ID,
    session_id: item.sessionId,
    agent_id: item.name,
    kind: "task",
    task_id: item.taskId,
    status: "completed",
    started_at: "2026-07-30T12:00:00Z",
    finished_at: "2026-07-30T12:00:01Z",
    exit_code: 0,
    metadata: { backend: "codex" },
    created_at: "2026-07-30T12:00:00Z",
    updated_at: "2026-07-30T12:00:01Z",
  };
}

function canonicalSession(item: SwitchCase) {
  return {
    session_id: item.sessionId,
    task_id: item.taskId,
    agent_name: item.name,
    backend: "codex",
    started_at: "2026-07-30T12:00:00Z",
    ended_at: "2026-07-30T12:00:01Z",
    duration_s: 1,
    status: "completed",
    exit_code: 0,
    input_tokens: 0,
    output_tokens: 0,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    estimated_cost_usd: 0,
    files_changed: 0,
    lines_added: 0,
    lines_removed: 0,
    files_touched: [],
    attempt_num: 1,
    is_active: false,
    has_transcript: true,
    has_diff: false,
  };
}

async function setupHarness(
  page: Page,
  transcriptRequestPaths: string[],
  options: TranscriptHarnessOptions = {},
): Promise<void> {
  const failedTranscriptTasks = new Set<string>();
  await page.addInitScript(() => window.localStorage.clear());
  await page.route(
    (url) => url.pathname.startsWith("/api/"),
    async (route) => {
      const url = new URL(route.request().url());
      const path = url.pathname;

      if (path === "/api/config") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ mode: "open" }),
        });
        return;
      }
      if (path === "/api/auth/token") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ token: "transcript-switch-harness" }),
        });
        return;
      }
      if (path === "/api/health") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ status: "ok", daemon: true }),
        });
        return;
      }
      if (path === "/api/local/settings") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({
            version: 1,
            fleetdb_redis: {
              enabled: false,
              db: 0,
              tls: false,
              password_set: false,
            },
            agent_runtime: { default: "local" },
            local_task_runner: {},
            runtime_credentials: {
              daytona: { configured: false, usable: false },
              github: { configured: false, usable: false },
            },
          }),
        });
        return;
      }
      if (path === "/api/workspaces/active") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(workspaceData),
        });
        return;
      }
      if (path === WORKSPACE_API || path === `${WORKSPACE_API}/`) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(workspaceData),
        });
        return;
      }
      if (
        path === `${WORKSPACE_API}/monitor/status` ||
        path === "/api/monitor/status"
      ) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(monitorStatus),
        });
        return;
      }
      if (path === "/api/monitor/agents") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            agents: monitorAgents,
            timestamp: monitorStatus.timestamp,
          }),
        });
        return;
      }
      if (path === "/api/monitor/tasks") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            summary: monitorStatus.tasks,
            needs_planning: [],
            ready_to_implement: [],
            needs_review: [],
            in_progress: [],
            backlog: [],
            closed: [],
            timestamp: monitorStatus.timestamp,
          }),
        });
        return;
      }
      if (path === `${WORKSPACE_API}/events`) {
        await route.abort();
        return;
      }

      const historyMatch = path.match(
        new RegExp(`^${WORKSPACE_API}/agents/([^/]+)/runs$`),
      );
      if (historyMatch) {
        const item = switchCaseByAgent(
          decodeURIComponent(historyMatch[1] ?? ""),
        );
        await route.fulfill({
          status: item ? 200 : 404,
          contentType: "application/json",
          body: item
            ? JSON.stringify({
                agent_id: item.name,
                runs: [],
                sessions: [historySession(item)],
              })
            : JSON.stringify({ error: "agent not found" }),
        });
        return;
      }

      const transcriptMatch = path.match(
        new RegExp(
          `^${WORKSPACE_API}/tasks/([^/]+)/sessions/([^/]+)/transcript$`,
        ),
      );
      if (transcriptMatch) {
        const taskId = decodeURIComponent(transcriptMatch[1] ?? "");
        const sessionId = decodeURIComponent(transcriptMatch[2] ?? "");
        const item = switchCaseByTask(taskId);
        transcriptRequestPaths.push(path);
        if (
          taskId === options.failFirstTranscriptForTaskId &&
          !failedTranscriptTasks.has(taskId)
        ) {
          failedTranscriptTasks.add(taskId);
          await route.fulfill({
            status: 500,
            contentType: "application/json",
            body: JSON.stringify({
              success: false,
              error: "temporary FleetDB outage",
            }),
          });
          return;
        }
        if (
          taskId === options.holdTranscriptForTaskId &&
          options.waitForHeldTranscript
        ) {
          options.onHeldTranscriptRequest?.();
          await options.waitForHeldTranscript;
        }
        await route.fulfill({
          status: item?.sessionId === sessionId ? 200 : 404,
          contentType: "application/json",
          body:
            item?.sessionId === sessionId
              ? ok({
                  session_id: item.sessionId,
                  entries: [
                    {
                      seq: 1,
                      timestamp: "2026-07-30T12:00:00Z",
                      role: "assistant",
                      type: "text",
                      text: item.body,
                    },
                  ],
                })
              : JSON.stringify({ success: false, error: "session not found" }),
        });
        return;
      }

      const taskSessionsMatch = path.match(
        new RegExp(`^${WORKSPACE_API}/tasks/([^/]+)/sessions$`),
      );
      if (taskSessionsMatch) {
        const taskId = decodeURIComponent(taskSessionsMatch[1] ?? "");
        const item = switchCaseByTask(taskId);
        // Keep metadata pending long enough to prove the history-derived
        // SessionRunDetail mounts and starts its transcript request immediately.
        await new Promise((resolve) => setTimeout(resolve, 4_000));
        await route.fulfill({
          status: item ? 200 : 404,
          contentType: "application/json",
          body: item
            ? ok({ task_id: item.taskId, sessions: [canonicalSession(item)] })
            : JSON.stringify({ success: false, error: "task not found" }),
        });
        return;
      }

      if (path === `${WORKSPACE_API}/workflows`) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ workflows: [] }),
        });
        return;
      }
      if (path === `${WORKSPACE_API}/trigger-bindings`) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ bindings: [] }),
        });
        return;
      }
      if (path === `${WORKSPACE_API}/agents`) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            success: true,
            data: [],
            total: 0,
          }),
        });
        return;
      }
      if (path === `${WORKSPACE_API}/stats`) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({
            total_issues: 0,
            open_issues: 0,
            in_progress_issues: 0,
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
        return;
      }
      if (
        path === `${WORKSPACE_API}/ready` ||
        path === `${WORKSPACE_API}/blocked` ||
        path === `${WORKSPACE_API}/issues`
      ) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok([]),
        });
        return;
      }
      if (path === `${WORKSPACE_API}/issues/graph`) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, issues: [] }),
        });
        return;
      }
      if (path === `${WORKSPACE_API}/terminal/sessions/by-issue`) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({}),
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
}

function deferred(): { promise: Promise<void>; resolve: () => void } {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

test.describe("Agent transcript switching", () => {
  test("harness-only: A → B → C keeps transcript URLs and bodies agent-scoped", async ({
    page,
  }) => {
    const transcriptRequestPaths: string[] = [];
    const browserErrors: string[] = [];
    page.on("pageerror", (error) =>
      browserErrors.push(error.stack ?? error.message),
    );
    page.on("console", (message) => {
      if (message.type() === "error") browserErrors.push(message.text());
    });
    await setupHarness(page, transcriptRequestPaths);

    await page.goto(`/ws/${WORKSPACE_ID}/agents/${SWITCH_CASES[0].name}`, {
      waitUntil: "domcontentloaded",
    });
    await page.waitForTimeout(300);
    await expect(
      page.getByTestId("agents-page"),
      `Agents page failed to boot: ${browserErrors.join(" | ")}`,
    ).toBeVisible({ timeout: 2_000 });

    for (const [index, item] of SWITCH_CASES.entries()) {
      if (index > 0) {
        await page.getByRole("button", { name: `Agent: ${item.name}` }).click();
      }
      await expect(page).toHaveURL(
        new RegExp(`/agents/${item.name.replace("-", "\\-")}$`),
      );
      await page.waitForTimeout(200);
      const renderFailure = page.getByRole("heading", {
        name: "Something went wrong",
      });
      if (await renderFailure.isVisible()) {
        await page.getByText("Technical details", { exact: true }).click();
        const details =
          (await page.getByRole("alert").textContent()) ?? "unknown error";
        throw new Error(
          `Agents page failed after selecting ${item.name}: ${details} | ${browserErrors.join(" | ")}`,
        );
      }
      await expect(
        page.getByText(item.body, { exact: true }),
        `Transcript for ${item.name} did not render: ${browserErrors.join(" | ")}`,
      ).toBeVisible({ timeout: 2_000 });
      for (const other of SWITCH_CASES) {
        if (other.name !== item.name) {
          await expect(
            page.getByText(other.body, { exact: true }),
          ).not.toBeVisible();
        }
      }
    }

    const uniquePaths = [...new Set(transcriptRequestPaths)];
    expect(uniquePaths).toEqual(
      SWITCH_CASES.map(
        (item) =>
          `${WORKSPACE_API}/tasks/${item.taskId}/sessions/${item.sessionId}/transcript`,
      ),
    );
  });

  test("harness-only: a late A transcript cannot replace C after A → B → C", async ({
    page,
  }) => {
    const transcriptRequestPaths: string[] = [];
    const heldTranscript = deferred();
    let heldRequestStarted = false;
    await setupHarness(page, transcriptRequestPaths, {
      holdTranscriptForTaskId: SWITCH_CASES[0].taskId,
      onHeldTranscriptRequest: () => {
        heldRequestStarted = true;
      },
      waitForHeldTranscript: heldTranscript.promise,
    });

    await page.goto(`/ws/${WORKSPACE_ID}/agents/${SWITCH_CASES[0].name}`, {
      waitUntil: "domcontentloaded",
    });
    await expect(page.getByTestId("agents-page")).toBeVisible({
      timeout: 2_000,
    });
    await expect.poll(() => heldRequestStarted).toBe(true);

    await page
      .getByRole("button", { name: `Agent: ${SWITCH_CASES[1].name}` })
      .click();
    await expect(
      page.getByText(SWITCH_CASES[1].body, { exact: true }),
    ).toBeVisible({ timeout: 2_000 });

    await page
      .getByRole("button", { name: `Agent: ${SWITCH_CASES[2].name}` })
      .click();
    await expect(
      page.getByText(SWITCH_CASES[2].body, { exact: true }),
    ).toBeVisible({ timeout: 2_000 });

    heldTranscript.resolve();
    await page.waitForTimeout(100);

    await expect(
      page.getByText(SWITCH_CASES[2].body, { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText(SWITCH_CASES[0].body, { exact: true }),
    ).not.toBeVisible();
    await expect(
      page.getByText(SWITCH_CASES[1].body, { exact: true }),
    ).not.toBeVisible();
    expect([...new Set(transcriptRequestPaths)]).toEqual(
      SWITCH_CASES.map(
        (item) =>
          `${WORKSPACE_API}/tasks/${item.taskId}/sessions/${item.sessionId}/transcript`,
      ),
    );
  });

  test("harness-only: a transient transcript 500 recovers in the selected agent pane", async ({
    page,
  }) => {
    const transcriptRequestPaths: string[] = [];
    const item = SWITCH_CASES[1];
    const expectedPath =
      `${WORKSPACE_API}/tasks/${item.taskId}/sessions/` +
      `${item.sessionId}/transcript`;
    await setupHarness(page, transcriptRequestPaths, {
      failFirstTranscriptForTaskId: item.taskId,
    });

    await page.goto(`/ws/${WORKSPACE_ID}/agents/${item.name}`, {
      waitUntil: "domcontentloaded",
    });
    await expect(page.getByTestId("agents-page")).toBeVisible({
      timeout: 2_000,
    });
    await expect(page.getByText(item.body, { exact: true })).toBeVisible({
      timeout: 8_000,
    });

    expect(
      transcriptRequestPaths.filter((path) => path === expectedPath),
    ).toHaveLength(2);
    await expect(page).toHaveURL(
      new RegExp(`/agents/${item.name.replace("-", "\\-")}$`),
    );
  });
});
