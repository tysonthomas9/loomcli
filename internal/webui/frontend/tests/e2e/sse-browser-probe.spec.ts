import { expect, test, type Route } from "@playwright/test";

import { createSSEBrowserProbe } from "./integration/sse-browser-probe";

test("passive read completion is fenced by request watermark and valid snapshot body", async ({
  page,
}) => {
  const held: Route[] = [];
  let holdFirst = true;
  let nextStatus = 200;
  await page.route("**/probe-fixture", (route) =>
    route.fulfill({
      status: 200,
      contentType: "text/html",
      body: "<main>probe</main>",
    }),
  );
  await page.route("**/api/workspaces/probe/issues/probe-1", async (route) => {
    if (holdFirst) {
      holdFirst = false;
      held.push(route);
      return;
    }
    const status = nextStatus;
    await route.fulfill({
      status,
      contentType: status === 200 ? "application/json" : "text/plain",
      body:
        status === 200
          ? JSON.stringify({ success: true, data: { id: "probe-1" } })
          : "injected failure",
    });
  });
  await page.goto("/probe-fixture");
  const probe = await createSSEBrowserProbe(page, "probe");
  probe.ownIssue("probe-1");

  await page.evaluate(() => {
    void fetch("/api/workspaces/probe/issues/probe-1").then((response) =>
      response.text(),
    );
  });
  await expect.poll(() => probe.requests.length).toBe(1);
  const watermark = probe.watermark();
  await held.shift()!.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({ success: true, data: { id: "probe-1" } }),
  });
  await expect.poll(() => probe.completions.length).toBe(1);
  expect(
    probe.completedReadsAfter(watermark, {
      issueId: "probe-1",
      method: "GET",
      status: 200,
      successfulSnapshot: true,
    }),
  ).toHaveLength(0);

  nextStatus = 500;
  await page.evaluate(() =>
    fetch("/api/workspaces/probe/issues/probe-1").then((response) =>
      response.text(),
    ),
  );
  await expect.poll(() => probe.completions.length).toBe(2);
  const failed = probe.completions.at(-1)!;
  expect(failed).toMatchObject({
    workspace: "probe",
    method: "GET",
    status: 500,
    issueId: "probe-1",
    bodyComplete: true,
    bodyJson: false,
    successfulSnapshot: false,
  });

  nextStatus = 200;
  await page.evaluate(() =>
    fetch("/api/workspaces/probe/issues/probe-1").then((response) =>
      response.text(),
    ),
  );
  await expect.poll(() => probe.completions.length).toBe(3);
  expect(
    probe.completedReadsAfter(watermark, {
      path: "/api/workspaces/probe/issues/probe-1",
      issueId: "probe-1",
      method: "GET",
      status: 200,
      successfulSnapshot: true,
    }),
  ).toHaveLength(1);
  probe.assertHealthy();
  await probe.dispose();
});
