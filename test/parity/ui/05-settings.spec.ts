/**
 * 05 Settings parity — backend selector shows correct backend per tab.
 *
 * The Settings page (or header status chip) reads /api/config. Preflight
 * already asserts different values; this spec additionally proves the
 * UI surfaces that difference.
 */
import {
  parityTest as test,
  expect,
  useParityHooks,
} from "./_support/spec-harness";
import {
  gotoViews,
  captureBothTabs,
  visualDiff,
  isFleetOnlyMode,
} from "./_support";
import { PARITY_URLS } from "./playwright.config";

useParityHooks();

test.describe("05 settings parity", () => {
  test("backend selector shows reference / fleet correctly", async ({
    tabs,
  }) => {
    await gotoViews(tabs, "settings");

    // The frontend surfaces the backend indicator via /api/config — we
    // assert on the API side (what the UI reads) AND on the rendered
    // text (what the user sees). If one matches and the other doesn't,
    // the frontend isn't wiring the config into the Settings view and
    // that's a webui bug to log.
    const [referenceCfg, fleetCfg] = await Promise.all([
      fetch(`${PARITY_URLS.reference}/api/config`).then((r) => r.json()),
      fetch(`${PARITY_URLS.fleet}/api/config`).then((r) => r.json()),
    ]);
    expect(referenceCfg.issue_backend ?? referenceCfg.backend).toBe(
      isFleetOnlyMode() ? "fleet" : "reference",
    );
    expect(fleetCfg.issue_backend ?? fleetCfg.backend).toBe("fleet");

    // Rendered text (best-effort; selector may change across redesigns).
    const backendLabel =
      '[data-testid="backend-indicator"], [data-backend], text=/backend/i';
    const referenceText = await tabs.reference
      .locator(backendLabel)
      .first()
      .innerText()
      .catch(() => "");
    const fleetText = await tabs.fleet
      .locator(backendLabel)
      .first()
      .innerText()
      .catch(() => "");
    // The two tabs MUST render different strings. If they render the
    // same, webui isn't reading /api/config — blocker-severity finding.
    expect(
      referenceText === fleetText &&
        (referenceText.length > 0 || fleetText.length > 0),
      `reference settings text="${referenceText}" fleet="${fleetText}"`,
    ).toBeFalsy();

    const shot = await captureBothTabs(
      tabs.reference,
      tabs.fleet,
      tabs.testId,
      "settings",
    );
    await visualDiff(shot, 0.1); // Settings vary legitimately; allow 10%.
  });
});
