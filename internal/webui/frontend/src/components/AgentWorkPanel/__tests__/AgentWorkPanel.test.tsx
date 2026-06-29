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

  it("opens the epic when its title is clicked", () => {
    const onTaskClick = vi.fn();
    const epic = issue({
      id: "EPIC-1",
      title: "Assigned epic",
      issue_type: "epic",
      status: "open",
    });
    renderWithStores(
      <AgentWorkPanel
        agentName="lead-1"
        onTaskClick={onTaskClick}
        onRunEpic={vi.fn()}
      />,
      {
        agents: [agent({ name: "lead-1", role: "lead", parent: "EPIC-1" })],
        issues: [
          epic,
          issue({
            id: "TASK-1",
            title: "First task",
            parent: "EPIC-1",
            status: "open",
          }),
        ],
      },
    );

    fireEvent.click(
      screen.getByRole("button", { name: "Open epic: Assigned epic" }),
    );

    expect(onTaskClick).toHaveBeenCalledWith(epic);
  });

  it("filters tasks by id and title and hides empty epic groups", () => {
    renderWithStores(<AgentWorkPanel agentName="worker-1" />, {
      agents: [agent({ name: "worker-1", role: "task" })],
      issues: [
        issue({
          id: "EPIC-1",
          title: "Build the Hello World web app",
          issue_type: "epic",
          status: "open",
        }),
        issue({
          id: "HELLO-WORLD-1",
          title: "Scaffold project",
          parent: "EPIC-1",
          assignee: "worker-1",
          status: "open",
        }),
        issue({
          id: "HELLO-WORLD-2",
          title: "Add landing page",
          parent: "EPIC-1",
          assignee: "worker-1",
          status: "open",
        }),
        issue({
          id: "EPIC-2",
          title: "Other epic",
          issue_type: "epic",
          status: "open",
        }),
        issue({
          id: "OTHER-1",
          title: "Unrelated task",
          parent: "EPIC-2",
          assignee: "worker-1",
          status: "open",
        }),
      ],
    });

    expect(screen.getByText("HELLO-WORLD-1")).toBeTruthy();
    expect(screen.getByText("HELLO-WORLD-2")).toBeTruthy();
    expect(screen.getByText("OTHER-1")).toBeTruthy();

    fireEvent.change(screen.getByRole("searchbox", { name: /search tasks/i }), {
      target: { value: "HELLO-WORLD-1" },
    });

    expect(screen.getByText("HELLO-WORLD-1")).toBeTruthy();
    expect(screen.queryByText("HELLO-WORLD-2")).toBeNull();
    expect(screen.queryByText("OTHER-1")).toBeNull();
    expect(screen.queryByText("Other epic")).toBeNull();
  });

  it("shows a search-specific empty state when nothing matches", () => {
    renderWithStores(<AgentWorkPanel agentName="worker-1" />, {
      agents: [agent({ name: "worker-1", role: "task" })],
      issues: [
        issue({
          id: "TASK-1",
          title: "First task",
          assignee: "worker-1",
          status: "open",
        }),
      ],
    });

    fireEvent.change(screen.getByRole("searchbox", { name: /search tasks/i }), {
      target: { value: "no-such-task" },
    });

    expect(screen.getByText("No tasks match your search.")).toBeTruthy();
  });

  it("clears task search when the selected agent changes", () => {
    const issueStore = createIssueStore();
    const agentStore = createAgentStore();
    issueStore.setState({
      issuesMap: new Map(
        [
          issue({
            id: "TASK-1",
            title: "First task",
            assignee: "worker-1",
            status: "open",
          }),
          issue({
            id: "TASK-2",
            title: "Second task",
            assignee: "worker-2",
            status: "open",
          }),
        ].map((item) => [item.id, item]),
      ),
    });
    agentStore.setState({
      agents: [
        agent({ name: "worker-1", role: "task" }),
        agent({ name: "worker-2", role: "task" }),
      ],
    });

    const { rerender } = render(
      <StoreContext.Provider value={{ issueStore, agentStore }}>
        <AgentWorkPanel agentName="worker-1" />
      </StoreContext.Provider>,
    );

    const searchbox = screen.getByRole("searchbox", {
      name: /search tasks/i,
    }) as HTMLInputElement;
    fireEvent.change(searchbox, { target: { value: "First" } });
    expect(searchbox.value).toBe("First");
    expect(screen.queryByText("TASK-2")).toBeNull();

    rerender(
      <StoreContext.Provider value={{ issueStore, agentStore }}>
        <AgentWorkPanel agentName="worker-2" />
      </StoreContext.Provider>,
    );

    expect(searchbox.value).toBe("");
    expect(screen.getByText("TASK-2")).toBeTruthy();
  });
});
