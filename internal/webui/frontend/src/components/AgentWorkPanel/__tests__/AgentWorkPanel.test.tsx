// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";

import { StoreContext } from "@/hooks/common";
import { createAgentStore, createIssueStore } from "@/stores";
import type { Issue, LoomAgentStatus } from "@/types";

import { AgentWorkPanel } from "../AgentWorkPanel";

function issue(overrides: Partial<Issue> & Pick<Issue, "id">): Issue {
  return {
    id: overrides.id,
    title: overrides.title ?? `${overrides.id} title`,
    priority: overrides.priority ?? 2,
    created_at: "2026-01-01T00:00:00Z" as Issue["created_at"],
    updated_at: "2026-01-01T00:00:00Z" as Issue["updated_at"],
    ...overrides,
  } as Issue;
}

function agent(
  overrides: Partial<LoomAgentStatus> & { name: string },
): LoomAgentStatus {
  return {
    name: overrides.name,
    branch: overrides.branch ?? "main",
    status: overrides.status ?? "idle",
    ahead: overrides.ahead ?? 0,
    behind: overrides.behind ?? 0,
    workspace: overrides.workspace ?? "default",
    ...overrides,
  } as LoomAgentStatus;
}

function renderWithStores(
  children: ReactNode,
  {
    issues,
    agents,
  }: {
    issues: Issue[];
    agents: LoomAgentStatus[];
  },
) {
  const issueStore = createIssueStore();
  const agentStore = createAgentStore();
  issueStore.setState({
    issuesMap: new Map(issues.map((item) => [item.id, item])),
  });
  agentStore.setState({ agents });

  return render(
    <StoreContext.Provider value={{ issueStore, agentStore }}>
      {children}
    </StoreContext.Provider>,
  );
}

describe("AgentWorkPanel", () => {
  it("lets an assigned lead run its focused epic", () => {
    const onRunEpic = vi.fn();
    renderWithStores(
      <AgentWorkPanel agentName="lead-1" onRunEpic={onRunEpic} />,
      {
        agents: [agent({ name: "lead-1", role: "lead", parent: "EPIC-1" })],
        issues: [
          issue({
            id: "EPIC-1",
            title: "Assigned epic",
            issue_type: "epic",
            status: "open",
          }),
          issue({
            id: "TASK-1",
            title: "First task",
            parent: "EPIC-1",
            status: "open",
          }),
        ],
      },
    );

    const runButton = screen.getByRole("button", {
      name: "Run epic EPIC-1",
    });
    fireEvent.click(runButton);

    expect(onRunEpic).toHaveBeenCalledWith("EPIC-1");
  });
});
