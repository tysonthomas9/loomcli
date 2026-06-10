import { expect, test, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";

import {
  BASE_URL,
  authHeaders,
  getWorkspaceById,
  resolveWorkspaceId,
} from "./helpers";

const LEAD_NAME = "lead-ui-e2e";
const EPIC_ID = "LEAD-E2E-EPIC";
const TASK_A = "LEAD-E2E-A";
const TASK_B = "LEAD-E2E-B";
const TASK_C = "LEAD-E2E-C";
const TASK_D = "LEAD-E2E-D";
const EPIC_TITLE = "Deterministic Lead Epic Runner Epic";

const skipLeadEpicRunner =
  !process.env.RUN_INTEGRATION_TESTS ||
  process.env.RUN_LEAD_EPIC_RUNNER_E2E !== "1";

test.skip(
  skipLeadEpicRunner,
  "lead epic runner E2E requires RUN_INTEGRATION_TESTS=1 and RUN_LEAD_EPIC_RUNNER_E2E=1",
);

test.describe.configure({ mode: "serial" });
test.setTimeout(180_000);

test("lead-panel Run queues and completes the built-in epic runner workflow", async ({
  page,
}) => {
  const workspaceId = await resolveWorkspaceId();
  const sourceRepo = await resolveDefaultSourceRepo(workspaceId);

  await createLead(workspaceId);
  await createEpicDag(workspaceId, sourceRepo);

  await page.goto(
    `/ws/${encodeURIComponent(workspaceId)}/agents/${encodeURIComponent(LEAD_NAME)}`,
  );
  await page.waitForLoadState("domcontentloaded");

  await expect(page.locator(`[data-agent-name="${LEAD_NAME}"]`)).toBeVisible({
    timeout: 20_000,
  });
  await expect(page.getByText(EPIC_TITLE)).toBeVisible({ timeout: 20_000 });

  const runButton = page.getByRole("button", { name: `Run epic ${EPIC_ID}` });
  await expect(runButton).toBeVisible({ timeout: 20_000 });

  const workflowPath = `/api/workspaces/${encodeURIComponent(workspaceId)}/workflows/epic-runner`;
  const [runResponse] = await Promise.all([
    page.waitForResponse((response) => {
      const url = new URL(response.url());
      return (
        response.request().method() === "POST" &&
        url.pathname === workflowPath
      );
    }),
    runButton.click(),
  ]);
  expect(runResponse.status()).toBe(202);

  const queued = (await runResponse.json()) as WorkflowRun;
  expect(queued.run_id).toBeTruthy();
  expect(queued.payload).toMatchObject({
    epicId: EPIC_ID,
    leadName: LEAD_NAME,
    requestedBy: "ui",
  });

  const completed = await waitForRunCompleted(page, workspaceId, queued.run_id);
  expect(completed.status).toBe("completed");
  expect(completed.summary ?? "").toContain(`Epic drained ${EPIC_ID}`);
  expect(completed.output?.logs_ref).toBe(
    `driver-run://${queued.run_id}/flue-local`,
  );

  const children = await listIssuesById(workspaceId, [
    TASK_A,
    TASK_B,
    TASK_C,
    TASK_D,
  ]);
  expect(children.map((issue) => issue.id).sort()).toEqual(
    [TASK_A, TASK_B, TASK_C, TASK_D].sort(),
  );
  expect(children.every((issue) => issue.status === "closed")).toBe(true);

  await expectTaskRunnerOrder([TASK_A, TASK_B, TASK_C, TASK_D]);
});

async function resolveDefaultSourceRepo(
  workspaceId: string,
): Promise<string | undefined> {
  const workspace = await getWorkspaceById(workspaceId);
  return workspace.data?.repos?.[0]?.name;
}

async function createLead(workspaceId: string): Promise<void> {
  const created = await postJson<AgentRecord>(
    `/api/workspaces/${encodeURIComponent(workspaceId)}/agents`,
    {
      name: LEAD_NAME,
      role_name: "lead",
      backend: "codex",
      auto: false,
      cross_repo: true,
      desired_state: "running",
    },
    201,
  );
  expect(created.name).toBe(LEAD_NAME);
  expect(created.role_name).toBe("lead");
}

async function createEpicDag(
  workspaceId: string,
  sourceRepo: string | undefined,
): Promise<void> {
  await createIssue(workspaceId, {
    id: EPIC_ID,
    title: EPIC_TITLE,
    issue_type: "epic",
    priority: 1,
    source_repo: sourceRepo,
  });
  await createIssue(workspaceId, {
    id: TASK_A,
    title: "Deterministic task A",
    issue_type: "task",
    priority: 1,
    parent: EPIC_ID,
    source_repo: sourceRepo,
  });
  await createIssue(workspaceId, {
    id: TASK_B,
    title: "Deterministic task B",
    issue_type: "task",
    priority: 1,
    parent: EPIC_ID,
    dependencies: [TASK_A],
    source_repo: sourceRepo,
  });
  await createIssue(workspaceId, {
    id: TASK_C,
    title: "Deterministic task C",
    issue_type: "task",
    priority: 1,
    parent: EPIC_ID,
    dependencies: [TASK_A],
    source_repo: sourceRepo,
  });
  await createIssue(workspaceId, {
    id: TASK_D,
    title: "Deterministic task D",
    issue_type: "task",
    priority: 1,
    parent: EPIC_ID,
    dependencies: [TASK_B, TASK_C],
    source_repo: sourceRepo,
  });
}

async function createIssue(
  workspaceId: string,
  input: IssueCreateInput,
): Promise<IssueRecord> {
  const body = Object.fromEntries(
    Object.entries(input).filter(([, value]) => value !== undefined),
  );
  const result = await postJson<ApiEnvelope<IssueRecord>>(
    `/api/workspaces/${encodeURIComponent(workspaceId)}/issues`,
    body,
    201,
  );
  if (!result.success || !result.data) {
    throw new Error(`issue create failed: ${result.error ?? "missing data"}`);
  }
  if (result.data.id !== input.id) {
    throw new Error(`created issue ${result.data.id}; expected ${input.id}`);
  }
  return result.data;
}

async function waitForRunCompleted(
  page: Page,
  workspaceId: string,
  runId: string,
): Promise<WorkflowRun> {
  const deadline = Date.now() + 120_000;
  let lastRun: WorkflowRun | undefined;

  while (Date.now() < deadline) {
    const run = await getJson<WorkflowRun>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/runs/${encodeURIComponent(runId)}`,
    );
    lastRun = run;
    if (run.status === "completed") return run;
    if (
      run.status === "failed" ||
      run.status === "needs_review" ||
      run.status === "cancelled"
    ) {
      throw new Error(
        `workflow run ${runId} reached ${run.status}: ${run.summary ?? run.error_class ?? "no summary"}`,
      );
    }
    await page.waitForTimeout(500);
  }

  throw new Error(
    `workflow run ${runId} did not complete; last status was ${lastRun?.status ?? "unknown"}`,
  );
}

async function listIssuesById(
  workspaceId: string,
  issueIds: string[],
): Promise<IssueRecord[]> {
  const params = new URLSearchParams({
    include_blocked: "true",
    exclude_status: "tombstone",
    limit: "1000",
  });
  const result = await getJson<ApiEnvelope<IssueRecord[]>>(
    `/api/workspaces/${encodeURIComponent(workspaceId)}/issues?${params}`,
  );
  if (!result.success || !Array.isArray(result.data)) {
    throw new Error(`issue list failed: ${result.error ?? "missing data"}`);
  }
  const wanted = new Set(issueIds);
  return result.data.filter((issue) => wanted.has(issue.id));
}

async function expectTaskRunnerOrder(expected: string[]): Promise<void> {
  const logPath = process.env.LEAD_EPIC_RUNNER_TASK_LOG;
  if (!logPath) {
    throw new Error("LEAD_EPIC_RUNNER_TASK_LOG is required");
  }
  const lines = (await readFile(logPath, "utf8"))
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);

  expect(lines).toHaveLength(expected.length);
  expect(lines[0]).toBe(TASK_A);
  expect(lines[lines.length - 1]).toBe(TASK_D);
  expect(lines.slice(1, 3).sort()).toEqual([TASK_B, TASK_C].sort());
}

async function getJson<T>(path: string): Promise<T> {
  const response = await fetch(`${BASE_URL}${path}`, {
    headers: authHeaders(),
  });
  return parseJsonResponse<T>(response, [200]);
}

async function postJson<T>(
  path: string,
  body: unknown,
  expectedStatus: number,
): Promise<T> {
  const response = await fetch(`${BASE_URL}${path}`, {
    method: "POST",
    headers: authHeaders({ "Content-Type": "application/json" }),
    body: JSON.stringify(body),
  });
  return parseJsonResponse<T>(response, [expectedStatus]);
}

async function parseJsonResponse<T>(
  response: Response,
  expectedStatuses: number[],
): Promise<T> {
  const text = await response.text();
  if (!expectedStatuses.includes(response.status)) {
    throw new Error(
      `${response.url} returned ${response.status}; expected ${expectedStatuses.join(", ")}: ${text}`,
    );
  }
  return JSON.parse(text) as T;
}

interface ApiEnvelope<T> {
  success: boolean;
  data?: T;
  error?: string;
}

interface AgentRecord {
  name: string;
  role_name: string;
}

interface IssueRecord {
  id: string;
  title?: string;
  status?: string;
}

interface IssueCreateInput {
  id: string;
  title: string;
  issue_type: "epic" | "task";
  priority: number;
  parent?: string;
  dependencies?: string[];
  source_repo?: string;
}

interface WorkflowRun {
  run_id: string;
  status:
    | "queued"
    | "running"
    | "completed"
    | "failed"
    | "needs_review"
    | "cancelled";
  payload?: Record<string, unknown>;
  output?: Record<string, string>;
  summary?: string;
  error_class?: string;
}
