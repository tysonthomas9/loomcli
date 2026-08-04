/**
 * 18 Agent views fleet-db acceptance — verifies monitor/sidebar agent state
 * and fleet-db-backed agent assignment APIs do not fall back to daemon/reference
 * control paths.
 */
import type { Page } from "@playwright/test";
import {
  fleetdbTest as test,
  expect,
  useFleetDBHooks,
} from "./_support/spec-harness";
import { discoverWorkspaceId } from "./_support";
import { FLEETDB_URLS } from "./playwright.config";

useFleetDBHooks();

type RouteHitRecorder = (url: string) => void;

test.describe("18 agents fleet-db acceptance", () => {
  test.describe.configure({ timeout: 150_000 });

  test("agent monitor, sidebar, assignments, and daemon-free controls work in fleet mode", async ({
    tabs,
    recordRouteHit,
  }) => {
    const [referenceWs, fleetWs] = await Promise.all([
      discoverWorkspaceId(FLEETDB_URLS.reference),
      discoverWorkspaceId(FLEETDB_URLS.fleet),
    ]);

    await Promise.all([
      tabs.reference.goto(`${FLEETDB_URLS.reference}/ws/${referenceWs}/monitor`),
      tabs.fleet.goto(`${FLEETDB_URLS.fleet}/ws/${fleetWs}/monitor`),
    ]);
    await Promise.all([
      assertMonitorAndSidebar(tabs.reference),
      assertMonitorAndSidebar(tabs.fleet),
    ]);

    const status = await apiJson(
      `${FLEETDB_URLS.fleet}/api/monitor/status`,
      recordRouteHit,
    );
    const monitorWorkspaceName = status.workspace?.name;
    expect(monitorWorkspaceName, "monitor workspace name").toBeTruthy();
    expect(
      status.workspace?.workspaces ?? [],
      "monitor workspace list",
    ).toContain(monitorWorkspaceName);
    expect(
      status.agents?.some((agent: any) => agent.name === "workspace"),
    ).toBeTruthy();
    expect(status.tasks?.needs_planning, "monitor queue count").toBeGreaterThan(
      0,
    );

    const agents = await apiJson(
      `${FLEETDB_URLS.fleet}/api/monitor/agents`,
      recordRouteHit,
    );
    const workspaceAgents =
      agents.by_workspace?.[monitorWorkspaceName] ??
      agents.by_workspace?.[fleetWs] ??
      [];
    expect(
      workspaceAgents.some((agent: any) => agent.name === "workspace"),
    ).toBeTruthy();

    const tasks = await apiJson(
      `${FLEETDB_URLS.fleet}/api/monitor/tasks`,
      recordRouteHit,
    );
    expect(tasks.summary?.needs_planning, "task queue summary").toBeGreaterThan(
      0,
    );

    const stamp = Date.now();
    const roleName = `fleetdb-regression-role-${stamp}`;
    const agentName = `fleetdb-regression-agent-${stamp}`;
    await createFleetRole(fleetWs, roleName);

    const created = await apiJson(
      `${FLEETDB_URLS.fleet}/api/workspaces/${fleetWs}/agents`,
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
      `${FLEETDB_URLS.fleet}/api/workspaces/${fleetWs}/agents/${agentName}`,
      recordRouteHit,
      {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ state: "active" }),
      },
    );
    expect(patched.state, "patched agent state").toBe("active");

    const listed = await apiJson(
      `${FLEETDB_URLS.fleet}/api/workspaces/${fleetWs}/agents`,
      recordRouteHit,
    );
    expect(listed.success, "agent list success envelope").toBe(true);
    expect(
      listed.data?.some(
        (agent: any) => agent.name === agentName && agent.state === "active",
      ),
    ).toBeTruthy();

    const queue = await fetchReachable(
      `${FLEETDB_URLS.fleet}/api/workspaces/${fleetWs}/agents/${agentName}/queue`,
    );
    recordRouteHit(
      `${FLEETDB_URLS.fleet}/api/workspaces/${fleetWs}/agents/${agentName}/queue`,
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
      `${FLEETDB_URLS.fleet}/api/workspaces/${fleetWs}/agents/${agentName}/stop`,
      { method: "POST" },
    );
    recordRouteHit(
      `${FLEETDB_URLS.fleet}/api/workspaces/${fleetWs}/agents/${agentName}/stop`,
    );
    expect(control.status, "fleet agent control status").toBe(200);
    const controlBody = await control.json();
    expect(controlBody.message, "fleet agent control message").toContain(
      "stopped",
    );

    await tabs.fleet.goto(`${FLEETDB_URLS.fleet}/ws/${fleetWs}/monitor`);
    await assertMonitorAndSidebar(tabs.fleet);
    await expect(tabs.fleet.getByText(agentName).first()).toBeVisible({
      timeout: 15_000,
    });

    await apiJson(
      `${FLEETDB_URLS.fleet}/api/workspaces/${fleetWs}/agents/${agentName}`,
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
    `${FLEETDB_URLS.fleetDB}/api/v1/${workspace}/roles`,
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Actor": "fleetdb-regression-harness",
      },
      body: JSON.stringify({
        name: roleName,
        description:
          "FleetDB Regression browser role for fleet-db agent assignment coverage",
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
