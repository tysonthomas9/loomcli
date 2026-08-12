/**
 * Unit tests for epicRunnerRuntimePayload — the UI -> server runner mapping for
 * the epic runner. The contract is fail-closed and explicit:
 *  - "Locally" (agent_runtime.default !== "daytona") MUST pin
 *    {runner: "local-task-runner"} rather than returning {} and letting the
 *    request fall through to an unspecified server-side default.
 *  - "daytona" maps to the daytona runner with the resolved repo URL.
 */

import { describe, it, expect } from "vitest";

import type { LocalSettingsData } from "@/api/common";
import type { RepoInfo } from "@/api/workspace";

import { epicRunnerRuntimePayload } from "../epicRunnerPayload";

function makeLocalSettings(runtime: "local" | "daytona"): LocalSettingsData {
  return {
    version: 1,
    fleetdb_redis: { enabled: false },
    agent_runtime: { default: runtime },
    local_task_runner: {},
    runtime_credentials: {
      daytona: { configured: false },
      github: { configured: false },
    },
  } as unknown as LocalSettingsData;
}

function makeRepo(
  overrides: Partial<RepoInfo> = {},
): Pick<
  RepoInfo,
  "name" | "source_repo_id" | "remote" | "remote_url" | "default_branch"
> {
  return {
    name: "acme",
    source_repo_id: "acme",
    remote: "git@github.com:acme/widgets",
    remote_url: "https://github.com/acme/widgets.git",
    default_branch: "main",
    ...overrides,
  };
}

describe("epicRunnerRuntimePayload", () => {
  it('maps "Locally" runtime to local-task-runner PR delivery with a selected repo', () => {
    const payload = epicRunnerRuntimePayload({
      localSettings: makeLocalSettings("local"),
      repos: [makeRepo()],
      currentRepo: "acme",
    });
    expect(payload).toEqual({
      runner: "local-task-runner",
      deliveryMode: "pull-request",
      repoUrl: "https://github.com/acme/widgets.git",
      baseBranch: "main",
      openPullRequest: true,
    });
  });

  it("maps a null/undefined runtime to the explicit local-task-runner", () => {
    const payload = epicRunnerRuntimePayload({
      localSettings: null,
      repos: [],
      currentRepo: null,
    });
    expect(payload).toEqual({
      runner: "local-task-runner",
      deliveryMode: "patch-back",
    });
  });

  it("never returns an empty payload for the local path", () => {
    const payload = epicRunnerRuntimePayload({
      localSettings: makeLocalSettings("local"),
      repos: [makeRepo()],
      currentRepo: "acme",
    });
    expect(payload.runner).toBe("local-task-runner");
    expect(payload.openPullRequest).toBe(true);
    expect(Object.keys(payload)).not.toHaveLength(0);
  });

  it('maps "daytona" runtime to the daytona runner with a normalized repo URL', () => {
    const payload = epicRunnerRuntimePayload({
      localSettings: makeLocalSettings("daytona"),
      repos: [makeRepo()],
      currentRepo: "acme",
    });
    expect(payload).toEqual({
      runner: "daytona-task-runner",
      deliveryMode: "pull-request",
      repoUrl: "https://github.com/acme/widgets.git",
      baseBranch: "main",
      openPullRequest: true,
      stackedPullRequests: true,
    });
  });

  it("throws on the daytona path when no repo URL can be resolved", () => {
    expect(() =>
      epicRunnerRuntimePayload({
        localSettings: makeLocalSettings("daytona"),
        repos: [],
        currentRepo: null,
      }),
    ).toThrow(/Daytona runtime requires/);
  });
});
