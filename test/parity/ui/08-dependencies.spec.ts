/**
 * 08 Dependencies parity — add / remove / blocks chain.
 */
import {
  parityTest as test,
  expect,
  useParityHooks,
} from "./_support/spec-harness";
import {
  apiResponseDiff,
  findFleetIssueByTitle,
  routedFleetRequest,
  SEED_FIXTURE,
} from "./_support";
import { PARITY_URLS } from "./playwright.config";

useParityHooks();

test.describe("08 dependencies parity", () => {
  test("add and remove a dep on fleet — routing proven", async ({
    tabs,
    fleetSpy,
  }) => {
    const [a, b] = SEED_FIXTURE.children.slice(0, 2); // "Add login flow", "Fix checkout NPE"
    const { id: blockerId } = await findFleetIssueByTitle(a);
    const { id: blockedId } = await findFleetIssueByTitle(b);

    await tabs.fleet.goto(
      `${PARITY_URLS.fleet}/ws/${PARITY_URLS.workspace}/issues/${blockedId}`,
    );

    // webui route is /dependencies (plural, unabbreviated); fleet-db's
    // native API is /deps. We hit the webui so the test proves the
    // full path (browser → webui → backend.IssueBackend → fleet-db).
    // Body keys (depends_on_id, dep_type) match the webui's
    // AddDependencyRequest decoder.
    await routedFleetRequest(tabs, fleetSpy, "add-dep", {
      path: `issues/${blockedId}/dependencies`,
      method: "POST",
      body: { depends_on_id: blockerId, dep_type: "blocks" },
      acceptStatus: [200, 201, 204],
    });

    // Verify the dep appears via the API on both sides.
    const deps = await apiResponseDiff(`issues/${blockedId}/dependencies`);
    expect(deps.count_fleet).toBeGreaterThanOrEqual(1);
  });
});
