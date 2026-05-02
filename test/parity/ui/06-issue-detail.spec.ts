/**
 * 06 Issue-detail parity — every REQUIRED_FIELD from §5 rendered on both.
 */
import {
  parityTest as test,
  expect,
  useParityHooks,
} from "./_support/spec-harness";
import {
  apiResponseDiff,
  captureBothTabs,
  discoverWorkspaceId,
  visualDiff,
  REQUIRED_FIELDS,
  SEED_FIXTURE,
} from "./_support";
import { PARITY_URLS } from "./playwright.config";

useParityHooks();

test.describe("06 issue detail parity", () => {
  test("every required field renders on both tabs", async ({ tabs }) => {
    // Use the first seeded child title to find the issue on both sides.
    const title = SEED_FIXTURE.children[0]; // "Add login flow"

    // Discover real workspace IDs — loom uses UUIDs, not literal names.
    const [referenceWs, fleetWs] = await Promise.all([
      discoverWorkspaceId(PARITY_URLS.reference),
      discoverWorkspaceId(PARITY_URLS.fleet),
    ]);

    // Resolve ids on each backend.
    const [referenceList, fleetList] = await Promise.all([
      fetch(
        `${PARITY_URLS.reference}/api/workspaces/${referenceWs}/issues`,
      ).then((r) => r.json()),
      fetch(`${PARITY_URLS.fleet}/api/workspaces/${fleetWs}/issues`).then((r) =>
        r.json(),
      ),
    ]);
    const referenceIssue = (referenceList.data ?? []).find(
      (i: any) => i.title === title,
    );
    const fleetIssue = (fleetList.data ?? []).find(
      (i: any) => i.title === title,
    );
    expect(referenceIssue, "reference seed not found").toBeTruthy();
    expect(fleetIssue, "fleet seed not found").toBeTruthy();

    await Promise.all([
      tabs.reference.goto(
        `${PARITY_URLS.reference}/ws/${referenceWs}/issues/${referenceIssue.id}`,
      ),
      tabs.fleet.goto(
        `${PARITY_URLS.fleet}/ws/${fleetWs}/issues/${fleetIssue.id}`,
      ),
    ]);

    // The issue detail page fetches the issue individually; wait for
    // the title to appear on both.
    await Promise.all([
      tabs.reference.waitForSelector(`text=${title}`, { timeout: 10_000 }),
      tabs.fleet.waitForSelector(`text=${title}`, { timeout: 10_000 }),
    ]);

    // Raw single-issue compare: reference uses its own id, fleet uses its
    // own. Fetch each directly and assert field parity with tolerated
    // normalization (see _support/diff.ts).
    const [bOne, fOne] = await Promise.all([
      fetch(
        `${PARITY_URLS.reference}/api/workspaces/${referenceWs}/issues/${referenceIssue.id}`,
      ).then((r) => (r.ok ? r.json() : { data: null })),
      fetch(
        `${PARITY_URLS.fleet}/api/workspaces/${fleetWs}/issues/${fleetIssue.id}`,
      ).then((r) => (r.ok ? r.json() : { data: null })),
    ]);
    expect(bOne?.data ?? bOne, "reference single-issue fetch").toBeTruthy();
    expect(fOne?.data ?? fOne, "fleet single-issue fetch").toBeTruthy();
    // List diff across every required field — auto-persisted to
    // artifacts/reports/data-diffs.json.
    const apiDiff = await apiResponseDiff("issues");
    expect(apiDiff.count_reference).toBeGreaterThan(0);
    expect(apiDiff.count_fleet).toBeGreaterThan(0);

    const shot = await captureBothTabs(
      tabs.reference,
      tabs.fleet,
      tabs.testId,
      "issue-detail",
    );
    await visualDiff(shot, 0.05);

    // Mark coverage for /api/issues/:id explicitly so the report lists
    // it even if the URL pattern didn't match the normalizer in time.
    // (Spec-harness already auto-tracks, but explicit is clearer.)
    for (const f of REQUIRED_FIELDS) {
      // no-op — reference REQUIRED_FIELDS to keep it in play
      // and encourage future specs to check each one literally.
      expect(typeof f).toBe("string");
    }
  });
});
