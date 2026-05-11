import { test, expect } from "@playwright/test";

import firstRunOnboarding from "../../../../../test/support/first-run-onboarding.mjs";

const { driveFirstRunOnboardingJourney, installFirstRunOnboardingMocks } =
  firstRunOnboarding;

test.describe("first-run onboarding journey", () => {
  test("creates workspace, agent, first task, and visible run state", async ({
    page,
  }) => {
    const harness = await installFirstRunOnboardingMocks(page);

    await driveFirstRunOnboardingJourney(page, expect);

    expect(harness.createWorkspaceRequests).toEqual([
      expect.objectContaining({
        name: "Hello-World",
        type: "clone",
        clone_urls: ["https://github.com/octocat/Hello-World"],
      }),
    ]);
    expect(harness.createAgentRequests).toEqual([
      expect.objectContaining({
        name: "planner",
        role_name: "plan",
        backend: "opencode",
        repos: ["Hello-World"],
      }),
    ]);
    expect(harness.firstTaskRequests).toEqual([
      expect.objectContaining({
        agent_name: "planner",
        title: "Explore Hello-World onboarding",
        source_repo: "Hello-World",
      }),
    ]);
  });
});
