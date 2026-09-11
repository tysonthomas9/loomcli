import { expect, test, type Page } from "@playwright/test";

import { observeTransition } from "../../helpers/ui-transition-probe";
import {
  closeTestIssueInWorkspace,
  generateTestId,
  getWsIssue,
} from "./helpers";
import { createSSEBrowserProbe } from "./sse-browser-probe";

const ENABLED = process.env.RUN_SSE_WORKFLOW_TRANSITION_TESTS === "1";
const WORKSPACE = process.env.LOOM_SSE_WORKFLOW_WORKSPACE ?? "LOCALMODE";
const API_BASE = (process.env.LOOM_BASE_URL ?? "http://localhost:8080").replace(
  /\/$/,
  "",
);

test.skip(!ENABLED, "requires the deterministic localdogfood workflow stack");
test.describe.configure({ mode: "serial", timeout: 180_000 });

type Session = {
  session_id: string;
  agent_name: string;
  phase?: string;
  status: string;
  is_active: boolean;
  has_transcript: boolean;
  has_diff: boolean;
};

async function sessions(taskId: string): Promise<Session[]> {
  const response = await fetch(
    `${API_BASE}/api/workspaces/${encodeURIComponent(WORKSPACE)}/tasks/${encodeURIComponent(taskId)}/sessions`,
  );
  if (!response.ok) {
    throw new Error(`sessions read failed: ${response.status}`);
  }
  const body = (await response.json()) as {
    success?: boolean;
    data?: { sessions?: Session[] };
  };
  if (body.success !== true || !Array.isArray(body.data?.sessions)) {
    throw new Error("sessions read returned an invalid envelope");
  }
  return body.data.sessions;
}

async function createTaskThroughUI(page: Page, title: string) {
  await page.getByTestId("new-issue-button").click();
  const modal = page.getByRole("dialog", { name: "Create Issue" });
  await expect(modal).toBeVisible();
  const sourceRepo = modal.getByTestId("create-issue-source-repo");
  await expect(sourceRepo).toHaveValue("source-repo");
  await modal.getByTestId("create-issue-title").fill(title);
  const created = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" &&
      new URL(response.url()).pathname ===
        `/api/workspaces/${WORKSPACE}/issues`,
  );
  await modal.getByTestId("create-issue-submit").click();
  const response = await created;
  expect(response.ok()).toBe(true);
  const body = (await response.json()) as {
    success?: boolean;
    data?: { id?: string };
  };
  expect(body.success).toBe(true);
  expect(body.data?.id).toBeTruthy();
  return body.data!.id!;
}

async function openTaskPanel(page: Page, title: string, taskId: string) {
  const card = page.locator("article", { hasText: title });
  await expect(card).toBeVisible({ timeout: 20_000 });
  const panel = page
    .getByTestId("issue-detail-overlay")
    .getByTestId("issue-detail-panel");
  if ((await panel.getAttribute("data-state")) === "open") {
    await expect(panel.getByTestId("issue-id")).toContainText(taskId);
    return panel;
  }
  const sameIssueRemainsMounted = await panel
    .getByTestId("issue-id")
    .filter({ hasText: taskId })
    .count();
  const tabsLoaded = sameIssueRemainsMounted
    ? null
    : page.waitForResponse(
        (response) =>
          response.request().method() === "GET" &&
          new URL(response.url()).pathname ===
            `/api/workspaces/${WORKSPACE}/issues/${taskId}/tabs`,
      );
  await card.click();
  await expect(panel.getByTestId("issue-id")).toContainText(taskId);
  if (tabsLoaded) await tabsLoaded;
  return panel;
}

test("one UI-created task plans, approves, implements, and preserves loaded run history @sse-ui-transition @T12", async ({
  page,
}, testInfo) => {
  const title = `SSE T12 workflow ${generateTestId()}`;
  const stream = await createSSEBrowserProbe(page, WORKSPACE);
  let taskId: string | null = null;
  let boardTransition: Awaited<ReturnType<typeof observeTransition>> | null =
    null;
  try {
    await page.goto(`/ws/${encodeURIComponent(WORKSPACE)}/kanban?groupBy=none`);
    await expect(page.getByTestId("new-issue-button")).toBeVisible({
      timeout: 15_000,
    });
    await expect(
      page.locator('[aria-label^="Issue: Backlog grooming placeholder"]'),
    ).toBeVisible();
    boardTransition = await observeTransition(page, {
      root: "#main-content",
      protectedNodes: ['[aria-label^="Issue: Backlog grooming placeholder"]'],
      forbiddenWithinRoot: ['[data-testid="loading-container"]'],
      scope: { workspace: WORKSPACE, query: "groupBy=none" },
    });
    taskId = await createTaskThroughUI(page, title);
    stream.ownIssue(taskId);
    const sessionsPath = `/api/workspaces/${WORKSPACE}/tasks/${taskId}/sessions`;
    stream.enrollRead(sessionsPath, taskId);

    await expect
      .poll(
        async () => {
          const current = await getWsIssue(WORKSPACE, taskId!);
          return {
            status: current.status,
            hasDesign:
              typeof current.design === "string" && current.design.length > 0,
          };
        },
        { timeout: 45_000 },
      )
      .toEqual({ status: "review", hasDesign: true });
    await expect
      .poll(
        async () =>
          (await sessions(taskId!)).some(
            (session) =>
              session.agent_name === "local-planner" &&
              session.phase === "planning" &&
              session.status === "completed" &&
              !session.is_active &&
              session.has_transcript,
          ),
        { timeout: 30_000 },
      )
      .toBe(true);
    await expect
      .poll(
        () =>
          stream.frames.filter(
            (frame) => frame.event === "mutation" && frame.issueId === taskId,
          ).length,
        { timeout: 15_000 },
      )
      .toBeGreaterThan(0);

    let panel = await openTaskPanel(page, title, taskId);
    await expect(panel.getByTestId("panel-approve-button")).toBeVisible();
    await panel.getByRole("tab", { name: "Runs" }).click();
    await expect(panel.getByRole("tab", { name: "Runs" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    const plannerRow = panel.getByRole("button", {
      name: /Run by local-planner, Completed/,
    });
    await expect(plannerRow).toBeVisible({ timeout: 15_000 });
    await plannerRow.click();
    await expect(panel.getByTestId("session-detail-view")).toContainText(
      "local-planner",
    );
    await expect(panel.getByTestId("session-transcript")).toContainText(
      "Planner local-planner",
    );

    const approvalWatermark = stream.watermark();
    const approved = page.waitForResponse(
      (response) =>
        response.request().method() === "PATCH" &&
        new URL(response.url()).pathname ===
          `/api/workspaces/${WORKSPACE}/issues/${taskId}` &&
        response.status() >= 200 &&
        response.status() < 300,
    );
    await panel.getByTestId("panel-approve-button").click();
    await approved;
    await expect(panel).toHaveAttribute("data-state", "closed");

    panel = await openTaskPanel(page, title, taskId);
    await panel.getByRole("tab", { name: "Runs" }).click();
    await expect(panel.getByRole("tab", { name: "Runs" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    const retainedPlannerRow = panel.getByRole("button", {
      name: /Run by local-planner, Completed/,
    });
    await expect(retainedPlannerRow).toBeVisible();
    await retainedPlannerRow.click();
    const retainedPlannerDetail = panel.getByTestId("session-detail-view");
    const retainedPlannerTranscript = panel.getByTestId("session-transcript");
    await expect(retainedPlannerDetail).toContainText("local-planner");
    await expect(retainedPlannerTranscript).toContainText(
      "Planner local-planner",
    );
    await boardTransition.assertSatisfied();
    await boardTransition.dispose();
    boardTransition = null;
    const transition = await observeTransition(page, {
      root: "body",
      protectedNodes: [
        '[aria-label^="Run by local-planner, Completed"]',
        '[data-testid="session-detail-view"]',
        '[data-testid="session-transcript"]',
        '[aria-label^="Issue: Backlog grooming placeholder"]',
      ],
      forbiddenWithinRoot: [
        '[data-testid="panel-loading"]',
        '[data-testid="loading-container"]',
      ],
      scope: {
        workspace: WORKSPACE,
        selectedIssue: taskId,
        phase: "implementation",
      },
    });
    expect((await getWsIssue(WORKSPACE, taskId)).status).not.toBe("closed");

    await expect
      .poll(async () => (await getWsIssue(WORKSPACE, taskId!)).status, {
        timeout: 45_000,
      })
      .toBe("closed");
    const isCompletedCoder = (session: Session) =>
      session.agent_name === "local-coder" &&
      session.phase === "implementation" &&
      session.status === "completed" &&
      !session.is_active &&
      session.has_transcript &&
      session.has_diff;
    await expect
      .poll(async () => (await sessions(taskId!)).some(isCompletedCoder), {
        timeout: 30_000,
      })
      .toBe(true);
    const completedCoder = (await sessions(taskId)).find(isCompletedCoder)!;

    await expect
      .poll(
        () =>
          [...stream.frames]
            .reverse()
            .find(
              (frame) =>
                frame.sequence > approvalWatermark &&
                frame.event === "mutation" &&
                frame.issueId === taskId &&
                frame.entityId === completedCoder.session_id &&
                frame.workspaceId === WORKSPACE &&
                frame.action === "session.change" &&
                !!frame.data &&
                typeof frame.data === "object" &&
                (frame.data as { type?: unknown }).type === "session_change" &&
                (frame.data as { new_status?: unknown }).new_status ===
                  "completed",
            )?.sequence ?? 0,
        { timeout: 15_000 },
      )
      .toBeGreaterThan(approvalWatermark);
    const completedCoderFrame = [...stream.frames]
      .reverse()
      .find(
        (frame) =>
          frame.sequence > approvalWatermark &&
          frame.event === "mutation" &&
          frame.issueId === taskId &&
          frame.entityId === completedCoder.session_id &&
          frame.workspaceId === WORKSPACE &&
          frame.action === "session.change" &&
          !!frame.data &&
          typeof frame.data === "object" &&
          (frame.data as { type?: unknown }).type === "session_change" &&
          (frame.data as { new_status?: unknown }).new_status === "completed",
      )!.sequence;
    await expect
      .poll(
        () =>
          stream.completedReadsAfter(completedCoderFrame, {
            path: sessionsPath,
            issueId: taskId!,
            method: "GET",
            status: 200,
            successfulSnapshot: true,
          }).length,
        { timeout: 15_000 },
      )
      .toBeGreaterThan(0);
    await expect
      .poll(
        () =>
          stream.frames.filter(
            (frame) => frame.event === "mutation" && frame.issueId === taskId,
          ).length,
        { timeout: 15_000 },
      )
      .toBeGreaterThanOrEqual(2);

    const coderRow = panel.getByRole("button", {
      name: /Run by local-coder, Completed/,
    });
    await expect(coderRow).toBeVisible({ timeout: 20_000 });
    await expect(retainedPlannerDetail).toContainText("local-planner");
    await expect(retainedPlannerTranscript).toContainText(
      "Planner local-planner",
    );
    await transition.assertSatisfied();
    await transition.dispose();
    await coderRow.click();
    const detail = panel.getByTestId("session-detail-view");
    await expect(detail).toContainText("local-coder");
    await expect(detail.getByTestId("session-transcript")).toContainText(
      "Coder local-coder",
    );
    await detail.getByTestId("session-inner-tab-diff").click();
    await expect(detail.getByTestId("session-diff")).toContainText(
      "local-mode-agent-output.txt",
    );
    stream.assertHealthy();
  } finally {
    await testInfo.attach("sse-probe.json", {
      body: Buffer.from(JSON.stringify(stream.snapshot(), null, 2)),
      contentType: "application/json",
    });
    await boardTransition?.dispose().catch(() => {});
    await stream.dispose().catch(() => {});
    if (taskId) {
      await closeTestIssueInWorkspace(WORKSPACE, taskId).catch(() => {});
    }
  }
});
