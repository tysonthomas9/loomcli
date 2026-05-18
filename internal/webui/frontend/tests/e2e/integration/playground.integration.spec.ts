import { test, expect } from "@playwright/test";
import { BASE_URL, authHeaders } from "./helpers";

/**
 * Playground integration tests.
 *
 * Verifies the loom webui correctly renders the PLAYGROUND workspace
 * fixture created by `test/playground/setup.sh`.
 *
 * Prereqs:
 *   - `loom serve` running at http://localhost:8080
 *   - `test/playground/setup.sh` has been run (PLAYGROUND workspace exists
 *     with 3 seed tasks). Daemon may or may not be running.
 *   - RUN_INTEGRATION_TESTS=1
 *
 * Run with:
 *   RUN_INTEGRATION_TESTS=1 npx playwright test --project=integration \
 *     playground.integration.spec.ts
 *
 * These tests are non-destructive — they observe the playground but never
 * mutate it. Pair them with the Go scenario test or shell smoke test for
 * mutation coverage.
 */

const skipIntegration = !process.env.RUN_INTEGRATION_TESTS;
test.skip(
  skipIntegration,
  "Integration tests require RUN_INTEGRATION_TESTS=1",
);

const WORKSPACE_ID = "PLAYGROUND";

test.describe.configure({ mode: "serial" });

test.describe("Playground workspace UI", () => {
  test.beforeAll(async () => {
    // Verify the playground is set up; skip the suite cleanly if not.
    const resp = await fetch(
      `${BASE_URL}/api/workspaces/${WORKSPACE_ID}/issues`,
      { headers: authHeaders() },
    );
    if (!resp.ok) {
      test.skip(
        true,
        `PLAYGROUND workspace not found at ${BASE_URL} (HTTP ${resp.status}) — run test/playground/setup.sh first`,
      );
    }
  });

  test("kanban renders the 3 seed tasks", async ({ page }) => {
    await page.goto(`/ws/${WORKSPACE_ID}/kanban`);
    await page.waitForLoadState("domcontentloaded");

    // Wait for SSE to connect so the board hydrates.
    const connectionStatus = page.locator('[data-state="connected"]');
    await expect(connectionStatus).toBeVisible({ timeout: 10_000 });

    // The 3 seed task titles set by setup.sh.
    const titles = [
      "Seed task 1 (playground)",
      "Seed task 2 (playground)",
      "Seed task 3 (playground)",
    ];

    for (const title of titles) {
      await expect(async () => {
        const card = page.getByText(title, { exact: true });
        await expect(card).toBeVisible();
      }).toPass({ timeout: 10_000, intervals: [500, 1000, 2000] });
    }
  });

  test("agent definitions are visible", async ({ page }) => {
    await page.goto(`/ws/${WORKSPACE_ID}/`);
    await page.waitForLoadState("domcontentloaded");

    // Both agentdefs (planner + coder) should surface somewhere on the
    // workspace page. We're not asserting layout — just that the names
    // appear in the rendered DOM after hydration.
    for (const name of ["playground-planner", "playground-coder"]) {
      await expect(async () => {
        await expect(page.getByText(name, { exact: false }).first()).toBeVisible();
      }).toPass({ timeout: 10_000, intervals: [500, 1000, 2000] });
    }
  });
});
