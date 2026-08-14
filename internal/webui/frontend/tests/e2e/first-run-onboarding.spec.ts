import { test, expect } from "@playwright/test";

test.describe("first-run onboarding journey", () => {
  test.skip("applies a Team Template and hands the first issue to its architect", async ({
    page,
  }) => {
    // TODO(team-templates): replace the shared first-run harness, which is
    // outside frontend ownership and still hard-codes the removed planner
    // creation step, with a fresh-serve-compatible fixture that can model the
    // catalog, synchronous apply report, and workspace refetch.
    //
    // Scenario outline:
    // 1. Create the Hello-World workspace and reach "Set up your team".
    // 2. Open the picker; assert the 2x2 cards, grouped agent-role chips, and
    //    interactive code-reviewer tooltip.
    // 3. Apply Full-Stack App Development; assert the exact POST path, locked
    //    progress state, warnings-before-rows, and itemized report.
    // 4. Retry the same bundle after one failed row; assert the same POST and
    //    collapsed already-set-up rows.
    // 5. Assert the step completes from the refreshed workspace agents.
    // 6. Create the first issue; assert labels:["architect"], pin_agent:false,
    //    agent_name:"app-architect-1", and the architect-poll success copy.
    await page.goto("/");
    await expect(page).toHaveTitle(/Loom/);
  });
});
