/**
 * 19 First-run onboarding fleetdb-regression — browser journey guard for the
 * no-workspace wizard through agent run visibility.
 */
import {
  fleetdbTest as test,
  expect,
  useFleetDBHooks,
} from "./_support/spec-harness";
import { FLEETDB_URLS } from "./playwright.config";
// @ts-ignore The shared JS helper lives outside this TS project.
import firstRunOnboarding from "../../support/first-run-onboarding.mjs";

const { driveFirstRunOnboardingJourney, installFirstRunOnboardingMocks } =
  firstRunOnboarding;

useFleetDBHooks();

test.describe("19 first-run onboarding fleetdb-regression", () => {
  test("fleet UI keeps onboarding reachable through first run detection", async ({
    tabs,
  }) => {
    const harness = await installFirstRunOnboardingMocks(tabs.fleet);

    await driveFirstRunOnboardingJourney(
      tabs.fleet,
      expect,
      FLEETDB_URLS.fleet,
    );

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
