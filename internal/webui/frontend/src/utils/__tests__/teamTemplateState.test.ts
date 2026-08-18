/**
 * @vitest-environment jsdom
 */

import { beforeEach, describe, expect, it } from "vitest";

import {
  deriveFirstIssueStepStatus,
  deriveTeamSetupStepStatus,
  readTeamTemplateBreadcrumb,
  writeTeamTemplateBreadcrumb,
} from "../teamTemplateState";

describe("deriveTeamSetupStepStatus", () => {
  it("prioritizes applying, then server-derived agent completion", () => {
    expect(
      deriveTeamSetupStepStatus({
        isApplying: true,
        hasWorkspaceAgent: true,
        hasWorkspaceRepo: true,
        isDefaultBackendReady: true,
      }),
    ).toBe("pending");
    expect(
      deriveTeamSetupStepStatus({
        isApplying: false,
        hasWorkspaceAgent: true,
        hasWorkspaceRepo: false,
        isDefaultBackendReady: false,
      }),
    ).toBe("complete");
  });

  it("is current only after repo and backend setup", () => {
    expect(
      deriveTeamSetupStepStatus({
        isApplying: false,
        hasWorkspaceAgent: false,
        hasWorkspaceRepo: true,
        isDefaultBackendReady: true,
      }),
    ).toBe("current");
    expect(
      deriveTeamSetupStepStatus({
        isApplying: false,
        hasWorkspaceAgent: false,
        hasWorkspaceRepo: true,
        isDefaultBackendReady: false,
      }),
    ).toBe("blocked");
  });
});

describe("deriveFirstIssueStepStatus", () => {
  it("unblocks for either a planner or a detected template architect", () => {
    expect(
      deriveFirstIssueStepStatus({
        isRunning: false,
        hasWorkspaceIssue: false,
        hasOnboardingPlanner: false,
        hasTemplateArchitect: true,
        isDefaultBackendReady: true,
      }),
    ).toBe("current");
    expect(
      deriveFirstIssueStepStatus({
        isRunning: false,
        hasWorkspaceIssue: false,
        hasOnboardingPlanner: true,
        hasTemplateArchitect: false,
        isDefaultBackendReady: true,
      }),
    ).toBe("current");
  });

  it("prioritizes running and completion and otherwise fails closed", () => {
    expect(
      deriveFirstIssueStepStatus({
        isRunning: true,
        hasWorkspaceIssue: true,
        hasOnboardingPlanner: true,
        hasTemplateArchitect: true,
        isDefaultBackendReady: true,
      }),
    ).toBe("pending");
    expect(
      deriveFirstIssueStepStatus({
        isRunning: false,
        hasWorkspaceIssue: true,
        hasOnboardingPlanner: false,
        hasTemplateArchitect: false,
        isDefaultBackendReady: false,
      }),
    ).toBe("complete");
    expect(
      deriveFirstIssueStepStatus({
        isRunning: false,
        hasWorkspaceIssue: false,
        hasOnboardingPlanner: false,
        hasTemplateArchitect: true,
        isDefaultBackendReady: false,
      }),
    ).toBe("blocked");
  });
});

describe("Team Template breadcrumb", () => {
  beforeEach(() => localStorage.clear());

  it("round-trips template identity, revision, timestamp and counts", () => {
    writeTeamTemplateBreadcrumb("ws-1", {
      templateId: "ai-agent",
      revision: 1,
      ts: 123,
      counts: { created: 7, skipped: 2, diverged: 1, failed: 0 },
    });

    expect(readTeamTemplateBreadcrumb("ws-1")).toEqual({
      templateId: "ai-agent",
      revision: 1,
      ts: 123,
      counts: { created: 7, skipped: 2, diverged: 1, failed: 0 },
    });
  });

  it("rejects malformed stored values", () => {
    localStorage.setItem("loom:ws-1:team-template-applied", "{bad json");
    expect(readTeamTemplateBreadcrumb("ws-1")).toBeNull();
  });
});
