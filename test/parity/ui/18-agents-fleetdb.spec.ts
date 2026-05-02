/**
 * 18 Agent views fleet-db acceptance — verifies monitor/sidebar agent state
 * and fleet-db-backed agent assignment APIs do not fall back to daemon/reference
 * control paths.
 */
import type { Page } from "@playwright/test";
import {
  parityTest as test,
  expect,
  useParityHooks,
} from "./_support/spec-harness";
import { discoverWorkspaceId } from "./_support";
import { PARITY_URLS } from "./playwright.config";

useParityHooks();

type RouteHitRecorder = (url: string) => void;

test.describe("18 agents fleet-db acceptance", () => {
  test.describe.configure({ timeout: 150_000 });

  test("agent monitor, sidebar, assignments, and daemon-free controls work in fleet mode", async ({
    tabs,
    recordRouteHit,
  }) => {
    const [referenceWs, fleetWs] = await Promise.all([
      discoverWorkspaceId(PARITY_URLS.reference),
      discoverWorkspaceId(PARITY_URLS.fleet),
    ]);

    await Promise.all([
      tabs.reference.goto(`${PARITY_URLS.reference}/ws/${referenceWs}/monitor`),
      tabs.fleet.goto(`${PARITY_URLS.fleet}/ws/${fleetWs}/monitor`),
    ]);
    await Promise.all([
      assertMonitorAndSidebar(tabs.reference),
      assertMonitorAndSidebar(tabs.fleet),
    ]);

    const status = await apiJson(
      `${PARITY_URLS.fleet}/api/monitor/status`,
      recordRouteHit,
    );
    expect(status.workspace?.name, "monitor workspace").toBe(fleetWs);
    expect(
      status.agents?.some((agent: any) => agent.name === "workspace"),
    ).toBeTruthy();
    expect(status.tasks?.needs_planning, "monitor queue count").toBeGreaterThan(
      0,
    );

    const agents = await apiJson(
      `${PARITY_URLS.fleet}/api/monitor/agents`,
      recordRouteHit,
    );
    expect(
      agents.by_workspace?.[fleetWs]?.some(
        (agent: any) => agent.name === "workspace",
      ),
    ).toBeTruthy();

    const tasks = await apiJson(
      `${PARITY_URLS.fleet}/api/monitor/tasks`,
      recordRouteHit,
    );
    expect(tasks.summary?.needs_planning, "task queue summary").toBeGreaterThan(
      0,
    );

    const stamp = Date.now();
    const roleName = `parity-role-${stamp}`;
    const agentName = `parity-agent-${stamp}`;
    await createFleetRole(fleetWs, roleName);

    const created = await apiJson(
      `${PARITY_URLS.fleet}/api/workspaces/${fleetWs}/agents`,
      recordRouteHit,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: agentName,
          role_name: roleName,
          auto: true,
          backend: "claude",
        }),
      },
    );
    expect(created.workspace_key, "created agent workspace").toBe(fleetWs);
    expect(created.name, "created agent name").toBe(agentName);
    expect(created.state, "created agent state").toBe("idle");

    const patched = await apiJson(
      `${PARITY_URLS.fleet}/api/workspaces/${fleetWs}/agents/${agentName}`,
      recordRouteHit,
      {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ state: "active" }),
      },
    );
    expect(patched.state, "patched agent state").toBe("active");

    const listed = await apiJson(
      `${PARITY_URLS.fleet}/api/workspaces/${fleetWs}/agents`,
      recordRouteHit,
    );
    expect(listed.success, "agent list success envelope").toBe(true);
    expect(
      listed.data?.some(
        (agent: any) => agent.name === agentName && agent.state === "active",
      ),
    ).toBeTruthy();

    const queue = await fetchReachable(
      `${PARITY_URLS.fleet}/api/workspaces/${fleetWs}/agents/${agentName}/queue`,
    );
    recordRouteHit(
      `${PARITY_URLS.fleet}/api/workspaces/${fleetWs}/agents/${agentName}/queue`,
    );
    expect(queue.status, "fleet agent queue unsupported status").toBe(501);
    const queueBody = await queue.json();
    expect(queueBody.kind, "fleet agent queue error kind").toBe(
      "not_implemented",
    );
    expect(
      queueBody.error,
      "fleet agent queue does not mention daemon",
    ).not.toContain("daemon");

    const control = await fetchReachable(
      `${PARITY_URLS.fleet}/api/workspaces/${fleetWs}/agents/${agentName}/stop`,
      { method: "POST" },
    );
    recordRouteHit(
      `${PARITY_URLS.fleet}/api/workspaces/${fleetWs}/agents/${agentName}/stop`,
    );
    expect(control.status, "fleet agent control unsupported status").toBe(501);
    const controlBody = await control.json();
    expect(controlBody.kind, "fleet agent control error kind").toBe(
      "not_implemented",
    );
    expect(
      controlBody.error,
      "fleet agent control does not report daemon unavailable",
    ).not.toContain("daemon is not running");

    await tabs.fleet.goto(`${PARITY_URLS.fleet}/ws/${fleetWs}/monitor`);
    await assertMonitorAndSidebar(tabs.fleet);
    await expect(tabs.fleet.getByText(agentName).first()).toBeVisible({
      timeout: 15_000,
    });

    await apiJson(
      `${PARITY_URLS.fleet}/api/workspaces/${fleetWs}/agents/${agentName}`,
      recordRouteHit,
      { method: "DELETE" },
    );
  });
});

async function assertMonitorAndSidebar(page: Page): Promise<void> {
  await page.waitForLoadState("domcontentloaded").catch(() => undefined);
  await expect(page.getByTestId("monitor-dashboard")).toBeVisible({
    timeout: 15_000,
  });
  await expect(page.getByTestId("agent-activity-panel")).toBeVisible({
    timeout: 15_000,
  });
  await expect(page.getByText("Agents").first()).toBeVisible({
    timeout: 15_000,
  });
  await expect(page.getByText("workspace").first()).toBeVisible({
    timeout: 15_000,
  });
}

async function createFleetRole(
  workspace: string,
  roleName: string,
): Promise<void> {
  const response = await fetchReachable(
    `${PARITY_URLS.fleetDB}/api/v1/${workspace}/roles`,
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Actor": "parity-harness",
      },
      body: JSON.stringify({
        name: roleName,
        description:
          "Parity browser role for fleet-db agent assignment coverage",
        backend: "claude",
      }),
    },
  );
  expect(
    response.ok,
    `create fleet role status=${response.status}`,
  ).toBeTruthy();
  await response.body?.cancel().catch(() => undefined);
}

async function apiJson(
  url: string,
  recordRouteHit: RouteHitRecorder,
  init?: RequestInit,
): Promise<any> {
  recordRouteHit(url);
  const response = await fetchReachable(url, init);
  expect(response.ok, `${url} status=${response.status}`).toBeTruthy();
  return response.json();
}

async function fetchReachable(
  url: string,
  init?: RequestInit,
): Promise<Response> {
  let last: Response | null = null;
  for (let attempt = 0; attempt < 8; attempt++) {
    const response = await fetch(url, init);
    last = response;
    if (response.ok || ![429, 500, 503].includes(response.status)) {
      return response;
    }
    await response.body?.cancel().catch(() => undefined);
    await new Promise((resolve) => setTimeout(resolve, 500 + attempt * 250));
  }
  return last ?? fetch(url, init);
}
