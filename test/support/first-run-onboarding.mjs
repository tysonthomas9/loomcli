const WS_ID = "HELLO-WORLD";
const WS_NAME = "Hello-World";
const REPO_URL = "https://github.com/octocat/Hello-World";
const ISSUE_ID = "HELLO-WORLD-1";
const ISSUE_TITLE = "Explore Hello-World onboarding";
const AGENT_NAME = "planner";
const BACKEND_NAME = "opencode";
const SESSION_ID = "onboarding-run-1";
const NOW = "2026-05-11T12:00:00.000Z";

export async function installFirstRunOnboardingMocks(page) {
  const state = {
    workspaceCreated: false,
    agentCreated: false,
    issueCreated: false,
  };
  const harness = {
    createWorkspaceRequests: [],
    createAgentRequests: [],
    firstTaskRequests: [],
  };

  await page.addInitScript(() => {
    window.localStorage.clear();
    window.sessionStorage.clear();
  });

  await page.route("**/*", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const pathname = url.pathname;

    if (!pathname.startsWith("/api/") && pathname !== "/health") {
      await route.continue();
      return;
    }

    if (pathname === "/api/client-errors") {
      await route.fulfill({ status: 204, body: "" });
      return;
    }

    if (pathname === "/api/config") {
      await fulfillJson(route, { mode: "open" });
      return;
    }

    if (pathname === "/health" || pathname === "/api/health") {
      await fulfillJson(route, { status: "ok", daemon: true });
      return;
    }

    if (pathname === "/api/backends") {
      await fulfillJson(route, ok([backendHealth()]));
      return;
    }

    if (pathname.startsWith("/api/monitor/")) {
      await fulfillMonitor(route, pathname, state);
      return;
    }

    if (pathname === "/api/workspaces/active") {
      await fulfillJson(route, ok(workspaceData(state)));
      return;
    }

    if (pathname === "/api/workspaces") {
      if (request.method() === "GET") {
        await fulfillJson(route, {
          workspaces: workspaceData(state).workspaces,
        });
        return;
      }

      if (request.method() === "POST") {
        const body = request.postDataJSON();
        harness.createWorkspaceRequests.push(body);
        state.workspaceCreated = true;
        await fulfillJson(route, ok(workspaceData(state)), 201);
        return;
      }
    }

    const workspaceMatch = pathname.match(
      /^\/api\/workspaces\/([^/]+)(?:\/(.*))?$/,
    );
    if (!workspaceMatch) {
      await fulfillJson(
        route,
        { success: false, error: "Unhandled API route" },
        404,
      );
      return;
    }

    const workspaceId = decodeURIComponent(workspaceMatch[1] ?? "");
    const suffix = workspaceMatch[2] ?? "";
    if (workspaceId !== WS_ID) {
      await fulfillJson(
        route,
        { success: false, error: "Workspace not found" },
        404,
      );
      return;
    }

    await fulfillWorkspaceRoute(route, suffix, state, harness);
  });

  return harness;
}

export async function driveFirstRunOnboardingJourney(
  page,
  assert,
  startUrl = "/",
) {
  await page.goto(startUrl, { waitUntil: "domcontentloaded" });

  await assert(page.getByTestId("onboarding-flow")).toBeVisible({
    timeout: 15_000,
  });
  await assert(
    page.getByRole("heading", { name: "No workspaces found" }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Create Workspace" }).click();
  const workspaceDialog = page.getByRole("dialog", {
    name: "New Workspace",
  });
  await assert(workspaceDialog).toBeVisible();
  await assert(
    workspaceDialog.getByTestId("create-workspace-name"),
  ).toHaveValue(WS_NAME);
  await assert(
    workspaceDialog.getByTestId("create-workspace-clone-url"),
  ).toHaveValue(REPO_URL);

  const createWorkspaceResponse = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === "/api/workspaces" &&
      response.request().method() === "POST",
  );
  await workspaceDialog.getByTestId("create-workspace-submit").click();
  await createWorkspaceResponse;

  await assert(page).toHaveURL(new RegExp(`/ws/${WS_ID}/kanban$`), {
    timeout: 15_000,
  });
  await assert(
    page.getByRole("heading", { name: "Finish onboarding" }),
  ).toBeVisible();
  await assert(
    page.getByRole("heading", { name: "Create agent" }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Create Agent" }).click();
  const agentDialog = page.getByRole("dialog", { name: "New Agent" });
  await assert(agentDialog).toBeVisible();
  await assert(agentDialog.locator("#agent-name")).toHaveValue(AGENT_NAME);
  await assert(agentDialog.locator("#agent-role")).toHaveValue("plan");
  await assert(agentDialog.locator("#agent-backend")).toHaveValue(BACKEND_NAME);

  const createAgentResponse = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === `/api/workspaces/${WS_ID}/agents` &&
      response.request().method() === "POST",
  );
  await agentDialog.getByRole("button", { name: "Create Agent" }).click();
  await createAgentResponse;

  await assert(agentDialog).not.toBeVisible();
  await assert(page.getByRole("dialog", { name: "Create Issue" })).toHaveCount(
    0,
  );

  const createRunButton = page.getByRole("button", { name: "Create & Run" });
  await assert(createRunButton).toBeEnabled({ timeout: 15_000 });

  const firstTaskResponse = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname ===
        `/api/workspaces/${WS_ID}/onboarding/first-task` &&
      response.request().method() === "POST",
  );
  await createRunButton.click();
  await firstTaskResponse;

  const issueCard = page.locator("article", { hasText: ISSUE_TITLE }).first();
  await assert(issueCard).toBeVisible({ timeout: 15_000 });
  await assert(page).toHaveURL(new RegExp(`/ws/${WS_ID}/kanban$`));

  await issueCard.click();
  await assert(page.getByTestId("issue-detail-panel")).toBeVisible({
    timeout: 15_000,
  });
  await assert(page.getByTestId("latest-run-failure-banner")).toContainText(
    "AuthFailure",
    { timeout: 15_000 },
  );

  await page.getByRole("button", { name: "View run" }).click();
  await assert(page.getByTestId("sessions-tab")).toBeVisible();
  await assert(page.getByTestId(`session-row-${SESSION_ID}`)).toContainText(
    "AuthFailure",
  );

  await page.getByTestId(`session-row-${SESSION_ID}`).click();
  await assert(page.getByTestId("run-error-banner")).toContainText(
    "AuthFailure",
  );
}

async function fulfillWorkspaceRoute(route, suffix, state, harness) {
  const request = route.request();

  if (suffix === "") {
    await fulfillJson(route, ok(workspaceData(state)));
    return;
  }

  if (suffix === "events/token") {
    await fulfillJson(route, { disabled: true });
    return;
  }

  if (suffix.startsWith("events")) {
    await route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      headers: { "Cache-Control": "no-cache" },
      body: 'event: connected\ndata: {"message":"connected"}\n\n',
    });
    return;
  }

  if (suffix === "config/backend") {
    await fulfillJson(route, ok(backendConfig(state)));
    return;
  }

  if (suffix === "issues" && request.method() === "GET") {
    await fulfillJson(route, ok(issues(state)));
    return;
  }

  if (suffix === "ready" || suffix === "issues/graph") {
    await fulfillJson(route, ok(issues(state)));
    return;
  }

  if (suffix === "blocked") {
    await fulfillJson(route, ok([]));
    return;
  }

  if (suffix === "stats") {
    await fulfillJson(route, stats(state));
    return;
  }

  if (suffix === "agents") {
    if (request.method() === "GET") {
      await fulfillJson(route, ok(agents(state)));
      return;
    }

    if (request.method() === "POST") {
      const body = request.postDataJSON();
      harness.createAgentRequests.push(body);
      state.agentCreated = true;
      await fulfillJson(route, agent());
      return;
    }
  }

  if (suffix === "onboarding/first-task" && request.method() === "POST") {
    const body = request.postDataJSON();
    harness.firstTaskRequests.push(body);
    state.issueCreated = true;
    await fulfillJson(route, {
      success: true,
      issue: issue(),
      agent_name: AGENT_NAME,
      started: true,
    });
    return;
  }

  if (suffix === "terminal/tabs" || suffix === "terminal/sessions") {
    await fulfillJson(route, ok([]));
    return;
  }

  if (suffix === "terminal/state") {
    await fulfillJson(route, { active_tab: "" });
    return;
  }

  if (suffix === "terminal/sessions/by-issue") {
    await fulfillJson(route, ok({}));
    return;
  }

  const tabsMatch = suffix.match(/^issues\/([^/]+)\/tabs$/);
  if (tabsMatch) {
    if (request.method() === "GET") {
      await fulfillJson(route, ok(null));
      return;
    }
    await fulfillJson(route, ok(null));
    return;
  }

  const issueMatch = suffix.match(/^issues\/([^/]+)$/);
  if (issueMatch && request.method() === "GET") {
    const issueId = decodeURIComponent(issueMatch[1] ?? "");
    await fulfillJson(
      route,
      issueId === ISSUE_ID
        ? ok(issue())
        : { success: false, error: "Issue not found" },
      issueId === ISSUE_ID ? 200 : 404,
    );
    return;
  }

  const taskLogsMatch = suffix.match(/^tasks\/([^/]+)\/logs$/);
  if (taskLogsMatch) {
    await fulfillJson(route, { success: true, data: { phases: [] } });
    return;
  }

  const taskSessionsMatch = suffix.match(/^tasks\/([^/]+)\/sessions$/);
  if (taskSessionsMatch) {
    const taskId = decodeURIComponent(taskSessionsMatch[1] ?? "");
    await fulfillJson(route, {
      success: true,
      data: {
        task_id: taskId,
        sessions: taskId === ISSUE_ID && state.issueCreated ? [session()] : [],
      },
    });
    return;
  }

  await fulfillJson(
    route,
    { success: false, error: "Unhandled API route" },
    404,
  );
}

async function fulfillMonitor(route, pathname, state) {
  if (pathname.endsWith("/agents")) {
    await fulfillJson(route, { agents: agents(state) });
    return;
  }

  if (pathname.endsWith("/tasks")) {
    await fulfillJson(route, monitorTasks(state));
    return;
  }

  if (pathname.endsWith("/workspaces")) {
    await fulfillJson(route, {
      default: state.workspaceCreated ? WS_ID : "",
      workspaces: state.workspaceCreated ? { [WS_ID]: {} } : {},
    });
    return;
  }

  await fulfillJson(route, {
    agents: agents(state),
    tasks: monitorTasks(state),
    agent_tasks: state.issueCreated
      ? { [AGENT_NAME]: { id: ISSUE_ID, title: ISSUE_TITLE, status: "failed" } }
      : {},
    sync: {},
    stats: stats(state),
    timestamp: NOW,
  });
}

function workspaceData(state) {
  return {
    id: state.workspaceCreated ? WS_ID : "",
    name: state.workspaceCreated ? WS_NAME : "",
    path: state.workspaceCreated ? `/tmp/${WS_NAME}` : "",
    repos: state.workspaceCreated ? [repo()] : [],
    groups: [],
    agents: agents(state),
    workspaces: state.workspaceCreated
      ? [
          {
            id: WS_ID,
            name: WS_NAME,
            path: `/tmp/${WS_NAME}`,
            active: true,
            repo_count: 1,
            is_default: true,
          },
        ]
      : [],
    workspace_order: state.workspaceCreated ? [WS_ID] : [],
    default_workspace: state.workspaceCreated ? WS_ID : "",
  };
}

function repo() {
  return {
    name: WS_NAME,
    path: `/tmp/${WS_NAME}`,
    default_branch: "master",
    current_branch: "master",
    remote: REPO_URL,
    source_repo_id: WS_NAME,
    groups: [],
  };
}

function agent() {
  return {
    name: AGENT_NAME,
    repos: [WS_NAME],
    repo_groups: [],
    cross_repo: false,
    role_name: "plan",
    backend: BACKEND_NAME,
  };
}

function agents(state) {
  return state.agentCreated ? [agent()] : [];
}

function issue() {
  return {
    id: ISSUE_ID,
    title: ISSUE_TITLE,
    description: `Use the prefilled sample repo at ${REPO_URL}.`,
    status: "in_progress",
    priority: 2,
    issue_type: "task",
    assignee: AGENT_NAME,
    owner: AGENT_NAME,
    source_repo: WS_NAME,
    repo: WS_NAME,
    labels: [`repo:${WS_NAME}`],
    created_at: NOW,
    updated_at: NOW,
    comments: [],
    dependencies: [],
    dependents: [],
    events: [],
  };
}

function issues(state) {
  return state.issueCreated ? [issue()] : [];
}

function session() {
  return {
    session_id: SESSION_ID,
    task_id: ISSUE_ID,
    agent_name: AGENT_NAME,
    backend: "codex",
    phase: "planning",
    started_at: NOW,
    ended_at: NOW,
    duration_s: 1,
    status: "failed",
    exit_code: 1,
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
    error_class: "AuthFailure",
    last_error: "AuthFailure",
    is_active: false,
    has_transcript: false,
    has_diff: false,
  };
}

function backendHealth() {
  return {
    name: BACKEND_NAME,
    display_name: "OpenCode",
    available: true,
    installed: true,
    api_key_set: true,
    version: "test",
  };
}

function backendConfig(state) {
  return {
    backend: BACKEND_NAME,
    source: "workspace",
    available: [BACKEND_NAME],
    agents: agents(state),
  };
}

function monitorTasks(state) {
  const task = state.issueCreated
    ? { id: ISSUE_ID, title: ISSUE_TITLE, status: "failed" }
    : null;
  return {
    needs_planning: [],
    ready_to_implement: [],
    needs_review: [],
    in_progress: task ? [task] : [],
    closed: [],
    backlog: [],
  };
}

function stats(state) {
  const total = state.issueCreated ? 1 : 0;
  return {
    total_issues: total,
    open_issues: 0,
    in_progress_issues: total,
    closed_issues: 0,
    blocked_issues: 0,
    deferred_issues: 0,
    ready_issues: 0,
    tombstone_issues: 0,
    pinned_issues: 0,
    epics_eligible_for_closure: 0,
    average_lead_time_hours: 0,
    total,
    open: 0,
    in_progress: total,
    closed: 0,
    completion: 0,
    remaining: total,
  };
}

function ok(data) {
  return { success: true, data };
}

async function fulfillJson(route, body, status = 200) {
  await route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}

export default {
  driveFirstRunOnboardingJourney,
  installFirstRunOnboardingMocks,
};
