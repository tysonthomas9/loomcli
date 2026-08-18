import { expect, test } from "@playwright/test";

import {
  ok,
  setupFleetMocks,
  workspaceApi,
  workspaceData,
  workspacePath,
} from "./helpers/fleet";

test("workspace Activity shows history, pagination, filters, and live SSE events", async ({
  page,
}) => {
  const api = workspaceApi();
  const historyEvents = [
    {
      cursor: "100-0",
      timestamp: "2026-08-14T17:00:00Z",
      actor: "operator",
      action: "label.remove",
      entity_type: "issue",
      entity_id: "TEAMBACKEND-1",
      details: { label: "architect" },
    },
    {
      cursor: "101-0",
      timestamp: "2026-08-14T18:00:00Z",
      actor: "api-architect-1",
      action: "issue.claim",
      entity_type: "issue",
      entity_id: "TEAMBACKEND-1",
      details: {},
    },
  ];
  const auditRequests: URLSearchParams[] = [];
  let releaseSSE!: () => void;
  const sseRelease = new Promise<void>((resolve) => {
    releaseSSE = resolve;
  });
  let sseRequests = 0;

  await setupFleetMocks(page, [
    {
      id: "TEAMBACKEND-1",
      title: "Build workspace audit API",
      status: "in_progress",
      priority: 1,
      issue_type: "task",
      created_at: "2026-08-14T16:00:00Z",
      updated_at: "2026-08-14T18:00:00Z",
    },
  ]);

  await page.route(`**${api}`, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(
        ok({
          ...workspaceData(),
          repos: [{ name: "loomcli", path: "/tmp/default/loomcli" }],
          agents: [
            {
              name: "api-architect-1",
              role_name: "api-architect",
              enabled: true,
            },
          ],
        }),
      ),
    });
  });

  await page.route(`**${api}/audit**`, async (route) => {
    const url = new URL(route.request().url());
    auditRequests.push(url.searchParams);

    if (url.searchParams.get("since") === "older-page-cursor") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(
          ok({
            events: [
              {
                cursor: "99-0",
                timestamp: "2026-08-14T16:00:00Z",
                actor: "operator",
                action: "issue.create",
                entity_type: "issue",
                entity_id: "TEAMBACKEND-0",
                details: {},
              },
            ],
            next_cursor: "",
          }),
        ),
      });
      return;
    }

    const actor = url.searchParams.get("actor");
    const entity = url.searchParams.get("entity");
    const filtered = historyEvents.filter(
      (event) =>
        (!actor || event.actor === actor) &&
        (!entity || event.entity_id === entity),
    );
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(
        ok({
          events: filtered,
          next_cursor: actor || entity ? "" : "older-page-cursor",
        }),
      ),
    });
  });

  await page.route("**/api/workspaces/*/events**", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    if (pathname.endsWith("/events/token")) {
      await route.fulfill({ status: 404, body: "not found" });
      return;
    }

    sseRequests++;
    if (sseRequests > 1) {
      await route.abort();
      return;
    }

    await sseRelease;
    await route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      body: [
        "event: connected\ndata: {}\n\n",
        `event: mutation\ndata: ${JSON.stringify({
          type: "update",
          cursor: "101-0",
          action: "issue.claim",
          entity_type: "issue",
          entity_id: "TEAMBACKEND-1",
          actor: "api-architect-1",
          timestamp: "2026-08-14T18:00:00Z",
        })}\n\n`,
        `event: mutation\ndata: ${JSON.stringify({
          type: "status",
          cursor: "102-0",
          action: "issue.update",
          entity_type: "issue",
          entity_id: "TEAMBACKEND-1",
          actor: "api-architect-1",
          timestamp: "2026-08-14T18:05:00Z",
          old_status: "in_progress",
          new_status: "review",
        })}\n\n`,
      ].join(""),
    });
  });

  await page.goto(workspacePath("/"));
  await page.getByRole("button", { name: "Activity" }).click();

  await expect(page).toHaveURL(/\/ws\/default\/activity$/);
  await expect(page.getByRole("heading", { name: "Activity" })).toBeVisible();
  const initialRows = page.getByRole("listitem");
  await expect(initialRows).toHaveCount(2);
  await expect(initialRows.nth(0)).toHaveAccessibleName(
    "api-architect-1 claimed TEAMBACKEND-1",
  );
  await expect(initialRows.nth(1)).toHaveAccessibleName(
    "operator label architect removed from TEAMBACKEND-1",
  );

  await page.getByRole("button", { name: "Load more activity" }).click();
  await expect(
    page.getByRole("listitem", { name: "operator created TEAMBACKEND-0" }),
  ).toBeVisible();
  expect(
    auditRequests.some((params) => params.get("since") === "older-page-cursor"),
  ).toBe(true);

  await page.getByLabel("Filter by actor").selectOption("api-architect-1");
  await page.getByLabel("Filter by issue").selectOption("TEAMBACKEND-1");
  await expect
    .poll(() =>
      auditRequests.some(
        (params) =>
          params.get("actor") === "api-architect-1" &&
          params.get("entity") === "TEAMBACKEND-1",
      ),
    )
    .toBe(true);

  releaseSSE();

  const liveRow = page.getByRole("listitem", {
    name: "api-architect-1 moved TEAMBACKEND-1 to review",
  });
  await expect(liveRow).toBeVisible();
  await expect(liveRow).toHaveAttribute("data-live", "true");
  await expect(liveRow.getByText("api-architect-1")).toHaveAttribute(
    "data-actor-kind",
    "agent",
  );
  await expect(page.getByRole("listitem")).toHaveCount(2);
});
