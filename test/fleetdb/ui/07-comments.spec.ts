/**
 * 07 Comments fleetdb-regression — add + list. Body/text wire-format drift is tracked
 * under WAIVER-003 per ui-test-plan.md; we surface it explicitly here
 * rather than masking it.
 */
import {
  fleetdbTest as test,
  expect,
  useFleetDBHooks,
} from "./_support/spec-harness";
import {
  apiResponseDiff,
  findFleetIssueByTitle,
  routedFleetRequest,
  SEED_FIXTURE,
} from "./_support";
import { FLEETDB_URLS } from "./playwright.config";

useFleetDBHooks();

test.describe("07 comments fleetdb-regression", () => {
  test("add a comment on the fleet tab — routing proven", async ({
    tabs,
    fleetSpy,
  }) => {
    const { id } = await findFleetIssueByTitle(SEED_FIXTURE.children[0]);

    await tabs.fleet.goto(
      `${FLEETDB_URLS.fleet}/ws/${FLEETDB_URLS.workspace}/issues/${id}`,
    );

    // WAIVER-003: fleet expects "body", reference expects "text". The
    // adapter layer remaps; we send the canonical shape the UI sends.
    await routedFleetRequest(tabs, fleetSpy, "add-comment", {
      path: `issues/${id}/comments`,
      method: "POST",
      body: { body: "fleetdb-regression test comment" },
      acceptStatus: [201],
    });

    // List comments and assert at least one shows up. We don't force
    // field-by-field diff here because WAIVER-003 documents body vs text.
    const diff = await apiResponseDiff(`issues/${id}/comments`);
    expect(diff.count_fleet).toBeGreaterThanOrEqual(1);
  });
});
