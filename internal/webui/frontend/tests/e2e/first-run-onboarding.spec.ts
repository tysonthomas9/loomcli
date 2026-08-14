import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

import { ok, setupFleetMocks } from "./helpers/fleet";

const WORKSPACE_ID = "hello-world";
const WORKSPACE_API = `/api/workspaces/${WORKSPACE_ID}`;

const templateAgents = [
  {
    name: "app-architect-1",
    role_name: "app-architect",
    repos: [],
    repo_groups: [],
    cross_repo: true,
  },
  {
    name: "frontend-dev-1",
    role_name: "frontend-dev",
    repos: [],
    repo_groups: [],
    cross_repo: true,
  },
  {
    name: "backend-dev-1",
    role_name: "backend-dev",
    repos: [],
    repo_groups: [],
    cross_repo: true,
  },
  {
    name: "qa-engineer-1",
    role_name: "qa-engineer",
    repos: [],
    repo_groups: [],
    cross_repo: true,
  },
];

const teamTemplates = [
  {
    id: "fullstack-app",
    label: "Full-Stack App Development",
    description:
      "Architect-led team with split frontend/backend implementers plus a QA and review pair.",
    revision: 1,
    schema_version: 1,
    roles: [
      {
        name: "app-architect",
        kind: "worker",
        display_label: "Architecture",
        description: "Designs full-stack changes.",
      },
      {
        name: "frontend-dev",
        kind: "worker",
        display_label: "Developer",
        description: "Implements frontend changes.",
      },
      {
        name: "backend-dev",
        kind: "worker",
        display_label: "Developer",
        description: "Implements backend changes.",
      },
      {
        name: "qa-engineer",
        kind: "worker",
        display_label: "QA",
        description: "Tests the application.",
      },
      {
        name: "code-reviewer",
        kind: "interactive",
        display_label: "QA",
        description: "Reviews changes interactively.",
      },
    ],
    agents: [
      { name: "app-architect-1", role_name: "app-architect" },
      { name: "frontend-dev-1", role_name: "frontend-dev" },
      { name: "backend-dev-1", role_name: "backend-dev" },
      { name: "qa-engineer-1", role_name: "qa-engineer" },
    ],
  },
  {
    id: "website",
    label: "Website Development",
    description: "A website delivery team.",
    revision: 1,
    schema_version: 1,
    roles: [
      {
        name: "web-designer",
        kind: "worker",
        display_label: "Architecture",
        description: "Designs websites.",
      },
      {
        name: "frontend-dev",
        kind: "worker",
        display_label: "Developer",
        description: "Implements websites.",
      },
      {
        name: "content-writer",
        kind: "worker",
        display_label: "Developer",
        description: "Writes site content.",
      },
      {
        name: "site-qa",
        kind: "worker",
        display_label: "QA",
        description: "Tests websites.",
      },
      {
        name: "code-reviewer",
        kind: "interactive",
        display_label: "QA",
        description: "Reviews changes interactively.",
      },
    ],
    agents: [
      { name: "web-designer-1", role_name: "web-designer" },
      { name: "frontend-dev-1", role_name: "frontend-dev" },
      { name: "content-writer-1", role_name: "content-writer" },
      { name: "site-qa-1", role_name: "site-qa" },
    ],
  },
  {
    id: "ai-agent",
    label: "AI Agent Development",
    description: "An AI agent delivery team.",
    revision: 1,
    schema_version: 1,
    roles: [
      {
        name: "agent-architect",
        kind: "worker",
        display_label: "Architecture",
        description: "Designs agent systems.",
      },
      {
        name: "researcher",
        kind: "worker",
        display_label: "Architecture",
        description: "Researches open questions.",
      },
      {
        name: "agent-dev",
        kind: "worker",
        display_label: "Developer",
        description: "Implements agents.",
      },
      {
        name: "eval-engineer",
        kind: "worker",
        display_label: "QA",
        description: "Evaluates agents.",
      },
      {
        name: "code-reviewer",
        kind: "interactive",
        display_label: "QA",
        description: "Reviews changes interactively.",
      },
    ],
    agents: [
      { name: "agent-architect-1", role_name: "agent-architect" },
      { name: "researcher-1", role_name: "researcher" },
      { name: "agent-dev-1", role_name: "agent-dev" },
      { name: "eval-engineer-1", role_name: "eval-engineer" },
    ],
  },
  {
    id: "backend",
    label: "Backend Development",
    description: "A backend service delivery team.",
    revision: 1,
    schema_version: 1,
    roles: [
      {
        name: "api-architect",
        kind: "worker",
        display_label: "Architecture",
        description: "Designs backend services.",
      },
      {
        name: "backend-dev",
        kind: "worker",
        display_label: "Developer",
        description: "Implements backend services.",
      },
      {
        name: "data-engineer",
        kind: "worker",
        display_label: "Developer",
        description: "Implements data changes.",
      },
      {
        name: "qa-engineer",
        kind: "worker",
        display_label: "QA",
        description: "Tests backend services.",
      },
      {
        name: "code-reviewer",
        kind: "interactive",
        display_label: "QA",
        description: "Reviews changes interactively.",
      },
    ],
    agents: [
      { name: "api-architect-1", role_name: "api-architect" },
      { name: "backend-dev-1", role_name: "backend-dev" },
      { name: "data-engineer-1", role_name: "data-engineer" },
      { name: "qa-engineer-1", role_name: "qa-engineer" },
    ],
  },
];

const applyReport = {
  template_id: "fullstack-app",
  revision: 1,
  schema_version: 1,
  workspace_key: WORKSPACE_ID,
  dry_run: false,
  steps: [
    { entity: "role", name: "app-architect", action: "created" },
    { entity: "role", name: "frontend-dev", action: "created" },
    { entity: "role", name: "backend-dev", action: "created" },
    { entity: "role", name: "qa-engineer", action: "created" },
    { entity: "role", name: "code-reviewer", action: "created" },
    { entity: "agent", name: "app-architect-1", action: "created" },
    { entity: "agent", name: "frontend-dev-1", action: "created" },
    { entity: "agent", name: "backend-dev-1", action: "created" },
    { entity: "agent", name: "qa-engineer-1", action: "created" },
  ],
  created: 9,
  skipped: 0,
  diverged: 0,
  failed: 0,
  warnings: [],
  materialized: 4,
};

const firstIssue = {
  id: "onboarding-first-task",
  title: "Explore Hello-World onboarding",
  description: "Learn the Loom workflow with the sample repository.",
  status: "open",
  priority: 2,
  issue_type: "task",
  labels: ["architect"],
  created_at: "2026-08-14T12:00:00Z",
  updated_at: "2026-08-14T12:00:00Z",
};

function workspaceData(teamApplied: boolean) {
  return {
    id: WORKSPACE_ID,
    name: "Hello-World",
    path: "/tmp/hello-world",
    repos: [
      {
        name: "Hello-World",
        path: "/tmp/hello-world/Hello-World",
        default_branch: "main",
        current_branch: "main",
        remote: "https://github.com/octocat/Hello-World",
        remote_url: "https://github.com/octocat/Hello-World",
        source_repo_id: "hello-world-repo",
        groups: [],
      },
    ],
    groups: [],
    agents: teamApplied ? templateAgents : [],
    workspaces: [
      {
        id: WORKSPACE_ID,
        name: "Hello-World",
        path: "/tmp/hello-world",
        active: true,
        repo_count: 1,
        is_default: true,
      },
    ],
    workspace_order: [WORKSPACE_ID],
    default_workspace: "Hello-World",
  };
}

function monitorStatus(issueCreated: boolean) {
  const total = issueCreated ? 1 : 0;
  return {
    workspace: { mode: "workspace", name: "Hello-World" },
    agents: [],
    tasks: {
      needs_planning: total,
      ready_to_implement: 0,
      in_progress: 0,
      need_review: 0,
      backlog: 0,
      epics: 0,
    },
    needs_planning: issueCreated ? [firstIssue] : [],
    ready_to_implement: [],
    needs_review: [],
    in_progress: [],
    backlog: [],
    closed: [],
    in_progress_list: [],
    agent_tasks: {},
    stats: {
      open: total,
      closed: 0,
      total,
      completion: 0,
      remaining: total,
      in_progress: 0,
      review: 0,
      blocked: 0,
    },
    sync: {
      db_synced: true,
      db_last_sync: "2026-08-14T12:00:00Z",
      git_needs_push: 0,
      git_needs_pull: 0,
    },
    timestamp: "2026-08-14T12:00:00Z",
  };
}

async function setupFirstRunMocks(page: Page) {
  await setupFleetMocks(page, [], undefined, { workspaceId: WORKSPACE_ID });

  let teamApplied = false;
  let issueCreated = false;
  let releaseApply: () => void = () => undefined;
  const applyGate = new Promise<void>((resolve) => {
    releaseApply = resolve;
  });

  await page.route("**/*", async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    const issues = issueCreated ? [firstIssue] : [];

    if (
      pathname === "/api/workspaces/active" ||
      (pathname === WORKSPACE_API && request.method() === "GET")
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok(workspaceData(teamApplied))),
      });
    } else if (pathname === "/api/team-templates") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ templates: teamTemplates }),
      });
    } else if (
      pathname === `${WORKSPACE_API}/team-templates/fullstack-app/apply` &&
      request.method() === "POST"
    ) {
      await applyGate;
      teamApplied = true;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ status: "done", report: applyReport }),
      });
    } else if (
      pathname === `${WORKSPACE_API}/onboarding/first-task` &&
      request.method() === "POST"
    ) {
      issueCreated = true;
      await route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          issue: firstIssue,
          agent_name: "app-architect-1",
          started: false,
          queued: false,
        }),
      });
    } else if (
      request.method() === "GET" &&
      (pathname === `${WORKSPACE_API}/issues` ||
        pathname === `${WORKSPACE_API}/ready` ||
        pathname === `${WORKSPACE_API}/issues/graph`)
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok(issues)),
      });
    } else if (pathname === `${WORKSPACE_API}/stats`) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          total_issues: issues.length,
          open_issues: issues.length,
          in_progress_issues: 0,
          closed_issues: 0,
          blocked_issues: 0,
          deferred_issues: 0,
          ready_issues: issues.length,
          tombstone_issues: 0,
          pinned_issues: 0,
          epics_eligible_for_closure: 0,
          average_lead_time_hours: 0,
        }),
      });
    } else if (pathname === `${WORKSPACE_API}/monitor/status`) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(monitorStatus(issueCreated)),
      });
    } else if (pathname === "/api/monitor/agents") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ agents: [] }),
      });
    } else if (pathname === `${WORKSPACE_API}/interactive-prompts`) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          prompts: [
            { id: "lead", label: "Lead" },
            { id: "pr-review", label: "PR Review" },
          ],
        }),
      });
    } else if (
      pathname === `${WORKSPACE_API}/terminal/sessions` ||
      pathname === `${WORKSPACE_API}/terminal/sessions/by-issue`
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok([])),
      });
    } else if (pathname === "/api/client-errors") {
      await route.fulfill({ status: 204 });
    } else if (pathname === "/health") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ status: "ok" }),
      });
    } else {
      await route.fallback();
    }
  });

  return { releaseApply };
}

test.describe("first-run onboarding journey", () => {
  test("applies a Team Template and hands the first issue to its architect", async ({
    page,
  }) => {
    const harness = await setupFirstRunMocks(page);

    await page.goto(`/ws/${WORKSPACE_ID}/kanban`, {
      waitUntil: "domcontentloaded",
    });

    const onboarding = page.getByRole("complementary", {
      name: "Onboarding checklist",
    });
    await expect(onboarding).toBeVisible();

    const teamStep = onboarding
      .getByRole("heading", { name: "Set up your team", exact: true })
      .locator("xpath=ancestor::li");
    await expect(teamStep).toContainText("Next");
    await expect(
      onboarding.getByRole("heading", { name: "Create agent", exact: true }),
    ).toHaveCount(0);

    // The explicit skip path keeps the legacy planner defaults before a Team
    // Template has been applied.
    await teamStep
      .getByRole("button", { name: "Skip — create a single agent instead" })
      .click();
    const legacyAgentDialog = page.getByRole("dialog", { name: "New Agent" });
    await expect(
      legacyAgentDialog.getByTestId("create-agent-name"),
    ).toHaveValue("planner");
    await expect(
      legacyAgentDialog.getByTestId("create-agent-template-planner"),
    ).toHaveAttribute("aria-pressed", "true");
    await legacyAgentDialog.getByTestId("create-agent-close").click();

    await teamStep.getByRole("button", { name: "Choose Template" }).click();
    const teamDialog = page.getByRole("dialog", { name: "Set up your team" });
    await expect(teamDialog).toBeVisible();

    const cardLabels = [
      "Full-Stack App Development",
      "Website Development",
      "AI Agent Development",
      "Backend Development",
    ];
    const cards = cardLabels.map((label) =>
      teamDialog.getByRole("button", { name: new RegExp(label) }),
    );
    for (const card of cards) {
      await expect(card).toBeVisible();
      await expect(card).toContainText(
        "5 agent roles · 4 agents configured to run",
      );
    }

    const cardBoxes = await Promise.all(
      cards.map((card) => card.boundingBox()),
    );
    expect(cardBoxes.every(Boolean)).toBe(true);
    expect(Math.abs(cardBoxes[0]!.y - cardBoxes[1]!.y)).toBeLessThan(2);
    expect(cardBoxes[2]!.y).toBeGreaterThan(cardBoxes[0]!.y + 20);
    expect(Math.abs(cardBoxes[2]!.y - cardBoxes[3]!.y)).toBeLessThan(2);

    const fullStackCard = cards[0]!;
    for (const group of ["Developer", "QA", "Architecture"]) {
      await expect(
        fullStackCard.getByText(group, { exact: true }),
      ).toBeVisible();
    }
    for (const agentRole of [
      "app-architect",
      "frontend-dev",
      "backend-dev",
      "qa-engineer",
    ]) {
      await expect(
        fullStackCard.getByText(agentRole, { exact: true }),
      ).toBeVisible();
    }
    const interactiveAgentRole = fullStackCard.locator(
      '[title*="no background agent"]',
    );
    await expect(interactiveAgentRole).toContainText("code-reviewer");

    await fullStackCard.click();
    const applyRequestPromise = page.waitForRequest(
      (request) =>
        new URL(request.url()).pathname ===
        `${WORKSPACE_API}/team-templates/fullstack-app/apply`,
    );
    await teamDialog
      .getByRole("button", { name: "Apply to Hello-World" })
      .click();

    const applyRequest = await applyRequestPromise;
    expect(applyRequest.method()).toBe("POST");
    await expect(teamDialog.getByRole("status")).toContainText(
      "Setting up your team",
    );
    await expect(teamDialog.getByRole("status")).toContainText(
      "Creating agent roles, then agents…",
    );
    await expect(teamDialog.getByRole("button", { name: "Done" })).toHaveCount(
      0,
    );

    harness.releaseApply();

    await expect(
      teamDialog.getByRole("heading", {
        name: "Your team is set up — 9 created, 0 already existed",
      }),
    ).toBeVisible();
    await expect(teamDialog).toContainText(
      "Applied Full-Stack App Development · revision 1",
    );
    await teamDialog
      .locator("details")
      .first()
      .getByText("9 steps", {
        exact: false,
      })
      .click();
    const architectAgentRow = teamDialog
      .getByText("app-architect-1", { exact: true })
      .locator("xpath=ancestor::li");
    await expect(architectAgentRow).toContainText("agent");
    await expect(architectAgentRow).toContainText(
      "created · configured to run",
    );

    await teamDialog.getByRole("button", { name: "Done" }).click();
    await expect(teamDialog).toBeHidden();
    await expect(teamStep).toContainText("Done");
    await expect(teamStep).toContainText("9 created.");

    const firstIssueStep = onboarding
      .getByRole("heading", { name: "Create first issue", exact: true })
      .locator("xpath=ancestor::li");
    await expect(firstIssueStep).toContainText("Next");
    await expect(
      firstIssueStep.getByRole("button", { name: "Create first issue" }),
    ).toBeEnabled();

    // Opening the ordinary agent modal after a template apply must no longer
    // inject the onboarding planner name/role defaults.
    await page
      .getByRole("button", { name: "+ Add agent", exact: true })
      .click();
    const addAgentDialog = page.getByRole("dialog", { name: "New Agent" });
    await expect(addAgentDialog.getByTestId("create-agent-name")).toHaveValue(
      "",
    );
    await expect(
      addAgentDialog.getByTestId("create-agent-template-task"),
    ).toHaveAttribute("aria-pressed", "true");
    await addAgentDialog.getByTestId("create-agent-close").click();

    const firstTaskRequestPromise = page.waitForRequest(
      (request) =>
        new URL(request.url()).pathname ===
        `${WORKSPACE_API}/onboarding/first-task`,
    );
    await firstIssueStep
      .getByRole("button", { name: "Create first issue" })
      .click();

    const firstTaskRequest = await firstTaskRequestPromise;
    expect(firstTaskRequest.method()).toBe("POST");
    const firstTaskBody = firstTaskRequest.postDataJSON() as Record<
      string,
      unknown
    >;
    expect(firstTaskBody.agent_name).toBe("app-architect-1");
    expect(firstTaskBody.labels).toEqual(["architect"]);
    expect(firstTaskBody.pin_agent).toBe(false);

    await expect(
      page.getByText(
        "Created your first issue and labeled it architect. Your architect picks it up on the next poll.",
      ),
    ).toBeVisible();
  });
});
