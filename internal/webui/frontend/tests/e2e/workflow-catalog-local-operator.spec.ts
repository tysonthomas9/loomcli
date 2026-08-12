import { expect, test, type Page } from "@playwright/test";

const workspace = "TEST";
const otherWorkspace = "OTHER";
const workflow = "demo";
const versionId = "version-1";
const launchCode = "ab".repeat(32);
const lifecycleBearer = "cd".repeat(32);
const durableTokenSentinel = "durable-operator-token-must-never-enter-browser";
const proofPath = "/tests/e2e/fixtures/workflow-catalog-local-operator.html";

interface RequestRecord {
  method: string;
  path: string;
  authorization: string;
  body: string;
}

interface MockCatalog {
  requests: RequestRecord[];
  exchangeCount: () => number;
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
  created_by: "phase2-proof",
  created_at: "2026-07-15T00:00:00Z",
};

async function installCatalogMocks(page: Page): Promise<MockCatalog> {
  const requests: RequestRecord[] = [];
  let exchanges = 0;
  let approved = false;
  let active = false;

  await page.route("**/api/workspaces/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const authorization = request.headers()["authorization"] ?? "";
    const body = request.postData() ?? "";
    requests.push({ method: request.method(), path, authorization, body });

    const fulfill = (responseBody: unknown, status = 200) =>
      route.fulfill({
        status,
        contentType: "application/json",
        body: JSON.stringify(responseBody),
      });

    if (
      request.method() === "POST" &&
      path === `/api/workspaces/${workspace}/operator-sessions/exchange`
    ) {
      exchanges += 1;
      const suppliedLaunch = (
        request.postDataJSON() as { launch_code?: string }
      ).launch_code;
      if (exchanges !== 1 || suppliedLaunch !== launchCode) {
        await fulfill({ error: "invalid launch" }, 401);
        return;
      }
      await fulfill({
        access_token: lifecycleBearer,
        token_type: "Bearer",
        workspace,
        expires_at: new Date(Date.now() + 60_000).toISOString(),
      });
      return;
    }

    if (
      request.method() === "GET" &&
      (path === `/api/workspaces/${workspace}/workflows/${workflow}/source` ||
        path ===
          `/api/workspaces/${otherWorkspace}/workflows/${workflow}/source`)
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
      (path === `/api/workspaces/${workspace}/workflows/${workflow}/versions` ||
        path ===
          `/api/workspaces/${otherWorkspace}/workflows/${workflow}/versions`)
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

    const lifecyclePrefix = `/api/workspaces/${workspace}/workflows/${workflow}/versions/${versionId}`;
    if (
      request.method() === "POST" &&
      (path === `${lifecyclePrefix}/approve` ||
        path === `${lifecyclePrefix}/activate`)
    ) {
      if (authorization !== `Bearer ${lifecycleBearer}`) {
        await fulfill({ error: "missing lifecycle bearer" }, 401);
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

  return { requests, exchangeCount: () => exchanges };
}

async function expectBrowserSurfacesNotToContain(
  page: Page,
  secrets: string[],
): Promise<void> {
  const surfaces = await page.evaluate(() => ({
    href: window.location.href,
    hash: window.location.hash,
    historyState: JSON.stringify(window.history.state),
    dom: document.documentElement.outerHTML,
    localStorage: JSON.stringify({ ...window.localStorage }),
    sessionStorage: JSON.stringify({ ...window.sessionStorage }),
    cookie: document.cookie,
  }));
  const browserHistory = await page.context().newCDPSession(page);
  const navigationHistory = await browserHistory.send(
    "Page.getNavigationHistory",
  );
  const cookies = await page.context().cookies();
  const serialized = JSON.stringify({
    surfaces,
    navigationHistory: navigationHistory.entries.map((entry) => entry.url),
    cookies,
  });

  for (const secret of secrets) {
    expect(serialized).not.toContain(secret);
  }
}

test.describe("Workflow Catalog local Desktop lifecycle authority", () => {
  test("consumes the launch fragment and drives Approve then Activate", async ({
    page,
  }) => {
    const catalog = await installCatalogMocks(page);

    await page.goto(
      `${proofPath}#section=versions&loom_launch=${launchCode}&loom_workspace=${workspace}`,
    );

    const approve = page.getByTestId(`workflow-version-approve-${versionId}`);
    const activate = page.getByTestId(`workflow-version-activate-${versionId}`);
    await expect(page.getByRole("dialog")).toBeVisible();
    await expect(page.getByTestId("workflow-source-editor")).toHaveValue(
      "export default {};",
    );
    await expect(approve).toBeVisible();
    await expect(approve).toBeEnabled();
    await expect(activate).toBeVisible();
    await expect(activate).toBeDisabled();

    const proof = await page.evaluate(
      () =>
        (
          window as unknown as {
            __workflowCatalogProof: {
              events: Array<Record<string, unknown>>;
              error?: string;
            };
          }
        ).__workflowCatalogProof,
    );
    expect(proof.error).toBeUndefined();
    expect(proof.events.map((event) => event.stage)).toEqual([
      "capture-start",
      "fragment-checked",
      "exchange-start",
      "exchange-complete",
      "render-start",
    ]);
    expect(proof.events[0]).toMatchObject({ hadLaunchMaterial: true });
    expect(proof.events[1]).toMatchObject({
      launchCaptured: true,
      launchMaterialErased: true,
      hash: "#section=versions",
    });
    expect(catalog.exchangeCount()).toBe(1);

    await approve.click();
    await expect(approve).toHaveText("Approved");
    await expect(activate).toBeEnabled();
    await activate.click();
    await expect(activate).toHaveText("Active");
    await expect(activate).toBeDisabled();

    const sourceAndVersionReads = catalog.requests.filter(
      (request) =>
        request.method === "GET" && request.path.includes("/workflows/"),
    );
    expect(sourceAndVersionReads.length).toBeGreaterThanOrEqual(3);
    expect(
      sourceAndVersionReads.every((request) => request.authorization === ""),
    ).toBe(true);

    const lifecycleRequests = catalog.requests.filter(
      (request) =>
        request.method === "POST" &&
        (request.path.endsWith("/approve") ||
          request.path.endsWith("/activate")),
    );
    expect(
      lifecycleRequests.map((request) => ({
        path: request.path,
        authorization: request.authorization,
      })),
    ).toEqual([
      {
        path: `/api/workspaces/${workspace}/workflows/${workflow}/versions/${versionId}/approve`,
        authorization: `Bearer ${lifecycleBearer}`,
      },
      {
        path: `/api/workspaces/${workspace}/workflows/${workflow}/versions/${versionId}/activate`,
        authorization: `Bearer ${lifecycleBearer}`,
      },
    ]);
    expect(
      catalog.requests
        .filter((request) => request.authorization !== "")
        .map((request) => request.path),
    ).toEqual(lifecycleRequests.map((request) => request.path));
    expect(JSON.stringify(catalog.requests)).not.toContain(
      durableTokenSentinel,
    );
    await expectBrowserSurfacesNotToContain(page, [
      launchCode,
      lifecycleBearer,
      durableTokenSentinel,
    ]);

    // The cleaned history entry cannot replay the one-time launch on reload.
    await page.reload();
    await expect(
      page.getByTestId("workflow-lifecycle-desktop-required"),
    ).toBeVisible();
    expect(catalog.exchangeCount()).toBe(1);
    await expectBrowserSurfacesNotToContain(page, [
      launchCode,
      lifecycleBearer,
      durableTokenSentinel,
    ]);
  });

  test("keeps lifecycle controls unavailable without a Desktop launch", async ({
    page,
  }) => {
    const catalog = await installCatalogMocks(page);

    await page.goto(proofPath);

    await expect(page.getByRole("dialog")).toBeVisible();
    await expect(
      page.getByTestId("workflow-lifecycle-desktop-required"),
    ).toContainText(
      "lifecycle controls are unavailable in a raw loopback browser",
    );
    await expect(
      page.getByTestId(`workflow-version-approve-${versionId}`),
    ).toBeDisabled();
    await expect(
      page.getByTestId(`workflow-version-activate-${versionId}`),
    ).toBeDisabled();
    expect(catalog.exchangeCount()).toBe(0);
    expect(
      catalog.requests.every((request) => request.authorization === ""),
    ).toBe(true);
    expect(
      catalog.requests.some(
        (request) =>
          request.path.endsWith("/approve") ||
          request.path.endsWith("/activate"),
      ),
    ).toBe(false);

    const proof = await page.evaluate(
      () =>
        (
          window as unknown as {
            __workflowCatalogProof: {
              events: Array<Record<string, unknown>>;
              error?: string;
            };
          }
        ).__workflowCatalogProof,
    );
    expect(proof.error).toBeUndefined();
    expect(proof.events.map((event) => event.stage)).toEqual([
      "capture-start",
      "fragment-checked",
      "render-start",
    ]);
    expect(proof.events[0]).toMatchObject({ hadLaunchMaterial: false });
    expect(proof.events[1]).toMatchObject({
      launchCaptured: false,
      launchMaterialErased: true,
      hash: "",
    });
    expect(JSON.stringify(catalog.requests)).not.toContain(
      durableTokenSentinel,
    );
    await expectBrowserSurfacesNotToContain(page, [
      launchCode,
      lifecycleBearer,
      durableTokenSentinel,
    ]);
  });

  test("clears Desktop lifecycle authority after a workspace switch", async ({
    page,
  }) => {
    const catalog = await installCatalogMocks(page);

    await page.goto(
      `${proofPath}?workspace=${otherWorkspace}#loom_launch=${launchCode}&loom_workspace=${workspace}`,
    );

    await expect(page.getByRole("dialog")).toBeVisible();
    await expect(
      page.getByTestId("workflow-lifecycle-desktop-required"),
    ).toBeVisible();
    await expect(
      page.getByTestId(`workflow-version-approve-${versionId}`),
    ).toBeDisabled();
    await expect(
      page.getByTestId(`workflow-version-activate-${versionId}`),
    ).toBeDisabled();
    expect(catalog.exchangeCount()).toBe(1);
    expect(
      catalog.requests.some(
        (request) =>
          request.path.endsWith("/approve") ||
          request.path.endsWith("/activate"),
      ),
    ).toBe(false);
    await expectBrowserSurfacesNotToContain(page, [
      launchCode,
      lifecycleBearer,
      durableTokenSentinel,
    ]);
  });
});
