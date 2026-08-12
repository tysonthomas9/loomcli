import { expect, test, type Page } from "@playwright/test";

const workspace = "TEST";
const otherWorkspace = "OTHER";
const workflow = "demo";
const versionId = "version-1";
const proofPath = "/tests/e2e/fixtures/workflow-catalog-local-operator.html";

interface RequestRecord {
  method: string;
  path: string;
  authorization: string;
}

function driver(approved: boolean, active: boolean) {
  return {
    workspace_key: workspace,
    driver_id: "driver-1",
    name: workflow,
    active_version_id: active ? versionId : "",
    status: active ? "active" : "draft",
    trust_level: approved ? "trusted" : "untrusted",
    metadata: approved
      ? { [`approved_version:${versionId}`]: "source-digest" }
      : {},
    revision: active ? 3 : approved ? 2 : 1,
    created_at: "2026-07-15T00:00:00Z",
    updated_at: "2026-07-15T00:00:00Z",
  };
}

const version = {
  workspace_key: workspace,
  version_id: versionId,
  driver_id: "driver-1",
  version: 1,
  source_ref: "builtin:demo",
  source_digest: "source-digest",
  bundle_ref: "memory:demo",
  bundle_digest: "bundle-digest",
  runtime: "node",
  manifest: {},
  validation_status: "passed",
  created_by: "open-mode-proof",
  created_at: "2026-07-15T00:00:00Z",
};

async function installCatalogMocks(page: Page): Promise<RequestRecord[]> {
  const requests: RequestRecord[] = [];
  let approved = false;
  let active = false;

  await page.route("**/api/workspaces/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    requests.push({
      method: request.method(),
      path,
      authorization: request.headers()["authorization"] ?? "",
    });

    const fulfill = (body: unknown, status = 200) =>
      route.fulfill({
        status,
        contentType: "application/json",
        body: JSON.stringify(body),
      });
    const requestedWorkspace = path.split("/")[3];

    if (
      request.method() === "GET" &&
      path ===
        `/api/workspaces/${requestedWorkspace}/workflows/${workflow}/source`
    ) {
      await fulfill({
        name: workflow,
        builtin: true,
        entrypoint: "workflow.ts",
        files: { "workflow.ts": "export default {};" },
      });
      return;
    }

    if (
      request.method() === "GET" &&
      path ===
        `/api/workspaces/${requestedWorkspace}/workflows/${workflow}/versions`
    ) {
      await fulfill({
        driver: driver(approved, active),
        driver_id: "driver-1",
        active_version_id: active ? versionId : "",
        revision: active ? 3 : approved ? 2 : 1,
        versions: [version],
      });
      return;
    }

    const lifecyclePrefix = `/api/workspaces/${requestedWorkspace}/workflows/${workflow}/versions/${versionId}`;
    if (
      request.method() === "POST" &&
      (path === `${lifecyclePrefix}/approve` ||
        path === `${lifecyclePrefix}/activate`)
    ) {
      if (request.headers()["authorization"]) {
        await fulfill({ error: "open mode must not send a credential" }, 400);
        return;
      }
      if (path.endsWith("/approve")) {
        approved = true;
        await fulfill({
          action: "approve",
          driver: driver(true, false),
          version,
        });
        return;
      }
      if (!approved) {
        await fulfill({ error: "approval required" }, 409);
        return;
      }
      active = true;
      await fulfill({
        action: "activate",
        driver: driver(true, true),
        version,
      });
      return;
    }

    await fulfill(
      { error: `unexpected request: ${request.method()} ${path}` },
      404,
    );
  });

  return requests;
}

test.describe("Workflow Catalog local open mode", () => {
  test("a raw browser approves and activates without a credential exchange", async ({
    page,
  }) => {
    const requests = await installCatalogMocks(page);
    await page.goto(proofPath);

    const approve = page.getByTestId(`workflow-version-approve-${versionId}`);
    const activate = page.getByTestId(`workflow-version-activate-${versionId}`);
    await expect(page.getByRole("dialog")).toBeVisible();
    await expect(page.getByTestId("workflow-source-editor")).toHaveValue(
      "export default {};",
    );
    await expect(approve).toBeEnabled();
    await expect(activate).toBeDisabled();

    await approve.click();
    await expect(approve).toHaveText("Approved");
    await expect(activate).toBeEnabled();
    await activate.click();
    await expect(activate).toHaveText("Active");

    const lifecycleRequests = requests.filter(
      (request) =>
        request.method === "POST" &&
        (request.path.endsWith("/approve") ||
          request.path.endsWith("/activate")),
    );
    expect(lifecycleRequests.map((request) => request.path)).toEqual([
      `/api/workspaces/${workspace}/workflows/${workflow}/versions/${versionId}/approve`,
      `/api/workspaces/${workspace}/workflows/${workflow}/versions/${versionId}/activate`,
    ]);
    expect(requests.every((request) => request.authorization === "")).toBe(
      true,
    );
    expect(
      requests.some((request) => request.path.includes("operator-sessions")),
    ).toBe(false);
  });

  test("refresh and workspace navigation need no Desktop authorization", async ({
    page,
  }) => {
    const requests = await installCatalogMocks(page);
    await page.goto(`${proofPath}?workspace=${otherWorkspace}`);
    await expect(
      page.getByTestId(`workflow-version-approve-${versionId}`),
    ).toBeEnabled();

    await page.reload();
    await expect(
      page.getByTestId(`workflow-version-approve-${versionId}`),
    ).toBeEnabled();
    expect(requests.every((request) => request.authorization === "")).toBe(
      true,
    );
    expect(
      requests.some((request) => request.path.includes("operator-sessions")),
    ).toBe(false);
  });
});
