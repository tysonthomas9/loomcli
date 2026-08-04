/**
 * 11 Update flow fleetdb-regression — PATCH priority + description on fleet; state
 * reflects the update and routing proofs confirm fleet-db saw it.
 */
import {
  fleetdbTest as test,
  expect,
  useFleetDBHooks,
} from "./_support/spec-harness";
import {
  findFleetIssueByTitle,
  routedFleetRequest,
  SEED_FIXTURE,
} from "./_support";
import { FLEETDB_URLS } from "./playwright.config";

useFleetDBHooks();

test.describe("11 update-flow fleetdb-regression", () => {
  test("PATCH priority and description routes to fleet-db", async ({
    tabs,
    fleetSpy,
  }) => {
    const { id } = await findFleetIssueByTitle(SEED_FIXTURE.children[2]); // "Refactor auth middleware"

    await tabs.fleet.goto(
      `${FLEETDB_URLS.fleet}/ws/${FLEETDB_URLS.workspace}/issues/${id}`,
    );

    await routedFleetRequest(tabs, fleetSpy, "update-priority-description", {
      path: `issues/${id}`,
      method: "PATCH",
      body: {
        priority: 1,
        description: "bumped to P1 via fleetdb-regression-11 test",
      },
    });

    // Verify via API that the change landed.
    const after = await fetch(
      `${FLEETDB_URLS.fleet}/api/workspaces/${FLEETDB_URLS.workspace}/issues/${id}`,
    ).then((r) => r.json());
    const updated = after?.data ?? after;
    expect(updated.priority).toBe(1);
    expect(
      typeof updated.description === "string" &&
        updated.description.includes("bumped to P1"),
    ).toBeTruthy();
  });
});
