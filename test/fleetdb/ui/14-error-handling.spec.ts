/**
 * 14 Error-handling fleetdb-regression — 404, 422, 409 surface consistently on both.
 *
 * We deliberately hit missing IDs + malformed bodies + conflicting ops.
 * Both sides should return the same HTTP status class; payloads may differ
 * but the status code must match to keep the UI's error routing consistent.
 */
import {
  fleetdbTest as test,
  expect,
  useFleetDBHooks,
} from "./_support/spec-harness";
import { discoverWorkspaceId } from "./_support";
import { FLEETDB_URLS } from "./playwright.config";

useFleetDBHooks();

test.describe("14 error-handling fleetdb-regression", () => {
  test("404 on missing issue id on both backends", async () => {
    const [referenceWs, fleetWs] = await Promise.all([
      discoverWorkspaceId(FLEETDB_URLS.reference),
      discoverWorkspaceId(FLEETDB_URLS.fleet),
    ]);
    const [b, f] = await Promise.all([
      fetch(
        `${FLEETDB_URLS.reference}/api/workspaces/${referenceWs}/issues/no-such-id-xyz`,
      ),
      fetch(
        `${FLEETDB_URLS.fleet}/api/workspaces/${fleetWs}/issues/no-such-id-xyz`,
      ),
    ]);
    expect(b.status).toBe(404);
    expect(f.status).toBe(404);
  });

  test("422 on malformed create body on both backends", async () => {
    const [referenceWs, fleetWs] = await Promise.all([
      discoverWorkspaceId(FLEETDB_URLS.reference),
      discoverWorkspaceId(FLEETDB_URLS.fleet),
    ]);
    const body = JSON.stringify({ bogus: "no title or type" });
    const [b, f] = await Promise.all([
      fetch(`${FLEETDB_URLS.reference}/api/workspaces/${referenceWs}/issues`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body,
      }),
      fetch(`${FLEETDB_URLS.fleet}/api/workspaces/${fleetWs}/issues`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body,
      }),
    ]);
    // 400 or 422 is acceptable; both must be 4xx and MUST NOT be 5xx.
    expect(b.status).toBeGreaterThanOrEqual(400);
    expect(b.status).toBeLessThan(500);
    expect(f.status).toBeGreaterThanOrEqual(400);
    expect(f.status).toBeLessThan(500);
  });

  test("409 on closing an already-closed issue (fleetdb-regression of idempotency)", async () => {
    // Best-effort: close a seeded item twice on fleet; second close
    // should 409 or 200 idempotent but NOT 500.
    const fleetWs = await discoverWorkspaceId(FLEETDB_URLS.fleet);
    const j = await fetch(
      `${FLEETDB_URLS.fleet}/api/workspaces/${fleetWs}/issues`,
    ).then((r) => r.json());
    const first = (j.data ?? [])[0];
    if (!first) test.skip(true, "no seed issue available");
    const doClose = () =>
      fetch(
        `${FLEETDB_URLS.fleet}/api/workspaces/${fleetWs}/issues/${first.id}/close`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ close_reason: "fleetdb-regression-14-first" }),
        },
      );
    await doClose();
    const again = await doClose();
    expect(
      [200, 204, 409].includes(again.status),
      `second close status=${again.status}`,
    ).toBeTruthy();
  });
});
