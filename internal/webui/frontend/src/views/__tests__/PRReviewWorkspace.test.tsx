// @vitest-environment jsdom

import { describe, expect, it } from "vitest";

import type { Issue, LoomAgentStatus } from "@/types";

import {
  agentsLinkedToIssue,
  resolveDiffAgentForIssue,
} from "../PRReviewWorkspace";

function makeAgent(
  overrides: Partial<LoomAgentStatus> & Pick<LoomAgentStatus, "name">,
): LoomAgentStatus {
  return {
    status: "idle",
    desired_state: "stopped",
    mode: "persistent",
    ...overrides,
  } as LoomAgentStatus;
}

function makeIssue(overrides: Partial<Issue> & Pick<Issue, "id">): Issue {
  return {
    title: "Test issue",
    status: "review",
    ...overrides,
  } as Issue;
}

describe("resolveDiffAgentForIssue", () => {
  it("prefers the worker agent bound to the issue id", () => {
    const issue = makeIssue({ id: "LOCALMODE-1", assignee: "local-mode" });
    const worker = makeAgent({
      name: "local-coder",
      task_id: "LOCALMODE-1",
      mode: "ephemeral",
    });
    const agents = [worker, makeAgent({ name: "local-mode" })];

    expect(resolveDiffAgentForIssue(issue, agents)?.name).toBe("local-coder");
  });

  it("prefers a linked agent with commits ahead over a clean assignee", () => {
    const issue = makeIssue({ id: "LOCALMODE-1", assignee: "local-planner" });
    const agents = [
      makeAgent({ name: "local-planner", task_id: "LOCALMODE-1", ahead: 0 }),
      makeAgent({
        name: "local-coder",
        task_id: "LOCALMODE-1",
        ahead: 12,
        mode: "ephemeral",
      }),
    ];

    expect(resolveDiffAgentForIssue(issue, agents)?.name).toBe("local-coder");
  });

  it("collects all agents linked to an issue", () => {
    const issue = makeIssue({ id: "LOCALMODE-1", assignee: "local-planner" });
    const agents = [
      makeAgent({ name: "local-planner", task_id: "LOCALMODE-1" }),
      makeAgent({ name: "local-coder", task_id: "LOCALMODE-1" }),
    ];

    expect(
      agentsLinkedToIssue(issue, agents)
        .map((a) => a.name)
        .sort(),
    ).toEqual(["local-coder", "local-planner"]);
  });

  it("falls back to a direct agent assignee when no worker is bound", () => {
    const issue = makeIssue({ id: "TASK-9", assignee: "review-bot" });
    const agents = [makeAgent({ name: "review-bot" })];

    expect(resolveDiffAgentForIssue(issue, agents)?.name).toBe("review-bot");
  });

  it("returns undefined when assignee is not an agent and no worker exists", () => {
    const issue = makeIssue({ id: "TASK-9", assignee: "local-mode" });

    expect(resolveDiffAgentForIssue(issue, [])).toBeUndefined();
  });
});
