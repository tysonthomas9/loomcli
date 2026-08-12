import { describe, expect, it } from "vitest";

import { mergeAgentRoster } from "../agentRoster";

describe("mergeAgentRoster", () => {
  it("adds configured placeholders without replacing live monitor rows", () => {
    const roster = mergeAgentRoster(
      [
        {
          name: "live-coder",
          role: "task",
          status: "working",
          branch: "agent/live-coder",
          ahead: 1,
          behind: 0,
        },
      ],
      [
        {
          name: "live-coder",
          repos: ["loomcli"],
          repo_groups: [],
          cross_repo: false,
          role_name: "task",
        },
        {
          name: "configured-planner",
          repos: ["loomcli"],
          repo_groups: [],
          cross_repo: false,
          role_name: "plan",
        },
      ],
      "Workspace A",
    );

    expect(roster).toHaveLength(2);
    expect(roster.find((agent) => agent.name === "live-coder")).toMatchObject({
      status: "working",
      branch: "agent/live-coder",
    });
    expect(
      roster.find((agent) => agent.name === "configured-planner"),
    ).toMatchObject({
      status: "configured",
      role: "plan",
      repo: "loomcli",
      workspace: "Workspace A",
    });
  });
});
