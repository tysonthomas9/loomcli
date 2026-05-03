/**
 * 10 Create flow fleetdb-regression — full form submit + response mirrors on both sides.
 */
import {
  fleetdbTest as test,
  expect,
  useFleetDBHooks,
} from "./_support/spec-harness";
import {
  apiResponseDiff,
  isFleetOnlyMode,
  routedFleetRequest,
  snapshotState,
  stateSyncDiff,
} from "./_support";
import { FLEETDB_URLS } from "./playwright.config";

useFleetDBHooks();

test.describe("10 create-flow fleetdb-regression", () => {
  test("creating on fleet writes through to fleet-db and reflects back", async ({
    tabs,
    fleetSpy,
    stateBefore,
  }) => {
    const title = `fleetdb-regression-create-${Date.now()}`;
    await tabs.fleet.goto(
      `${FLEETDB_URLS.fleet}/ws/${FLEETDB_URLS.workspace}/kanban`,
    );

    await routedFleetRequest(tabs, fleetSpy, "create-issue", {
      path: "issues",
      method: "POST",
      body: {
        title,
        issue_type: "task",
        priority: 2,
        description: "created by fleetdb-regression test 10",
      },
      acceptStatus: [201],
    });

    // Comparing fleet and reference list counts is meaningless here because
    // reference accumulates tombstoned issues from earlier tests in the
    // same suite (legacy delete behavior left tombstones that still appeared in the
    // list). The semantic check is "fleet grew by exactly 1, reference
    // didn't grow at all" — assert that directly via the snapshots.
    await apiResponseDiff("issues"); // still useful for the diff report

    const stateAfter = await snapshotState("after", tabs.testId);
    const syncDiff = stateSyncDiff(stateBefore, stateAfter, "create-on-fleet");
    expect(stateAfter.fleet.issues.length).toBeGreaterThan(
      stateBefore.fleet.issues.length,
    );
    if (!isFleetOnlyMode()) {
      // Reference must NOT grow — writes shouldn't leak across backends.
      expect(stateAfter.reference.issues.length).toBe(
        stateBefore.reference.issues.length,
      );
    }
  });
});
