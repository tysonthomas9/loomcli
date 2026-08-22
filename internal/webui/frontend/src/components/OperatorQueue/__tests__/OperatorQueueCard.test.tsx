/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type { OperatorQueueItem } from "@/hooks/issues";
import type { Issue, LoomAgentStatus } from "@/types";

import { OperatorQueueCard, pickDefaultAgentName } from "../OperatorQueueCard";

const mockWorkspaceContext = vi.hoisted(() => ({
  repos: [] as {
    name: string;
    source_repo_id?: string;
    default_branch: string;
  }[],
}));

vi.mock("@/hooks/workspace", () => ({
  useWorkspaceContext: () => mockWorkspaceContext,
}));

function issue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "TASK-1",
    title: "Build the operator queue",
    priority: 2,
    issue_type: "task",
    status: "review",
    has_design: true,
    labels: ["needs-revision", "frontend"],
    assignee: "architect-1",
    source_repo: "source-repo",
    created_at: "2026-08-21T15:00:00.000Z",
    updated_at: "2026-08-21T15:48:02.000Z",
    ...overrides,
  } as Issue;
}

function item(
  kind: OperatorQueueItem["kind"],
  overrides: Partial<Issue> = {},
): OperatorQueueItem {
  const row = issue(overrides);
  return {
    issue: row,
    kind,
    waitingSince: Date.parse(row.updated_at),
  };
}

function agent(overrides: Partial<LoomAgentStatus>): LoomAgentStatus {
  return {
    name: "agent-1",
    branch: "agent-1",
    status: "idle",
    ahead: 0,
    behind: 0,
    workspace: "test-workspace",
    repo: "source-repo",
    ...overrides,
  };
}

function handlers() {
  return {
    onApprove: vi.fn().mockResolvedValue(undefined),
    onUnblock: vi.fn().mockResolvedValue(undefined),
    onOpenIssue: vi.fn(),
  };
}

beforeEach(() => {
  mockWorkspaceContext.repos = [
    {
      name: "source-repo",
      source_repo_id: "source-repo",
      default_branch: "main",
    },
  ];
});

describe("pickDefaultAgentName", () => {
  it("prefers an idle implementation agent, then falls back to the first", () => {
    const agents = [
      agent({ name: "planner", role: "plan" }),
      agent({ name: "busy-dev", role: "dev", status: "working: TASK-2" }),
      agent({ name: "idle-coder", role: "coder", status: "idle" }),
    ];

    expect(pickDefaultAgentName(agents, "source-repo")).toBe("idle-coder");
    expect(pickDefaultAgentName(agents.slice(0, 2), "source-repo")).toBe(
      "planner",
    );
    expect(pickDefaultAgentName([], "source-repo")).toBeUndefined();
    expect(pickDefaultAgentName(agents, "web")).toBeUndefined();
  });

  it("routes a repo-less task only to agents with no repo binding", () => {
    const agents = [
      agent({ name: "bound-coder", role: "coder", status: "idle" }),
      agent({ name: "researcher", role: "task", status: "idle", repo: "" }),
    ];

    expect(pickDefaultAgentName(agents, undefined)).toBe("researcher");
    expect(pickDefaultAgentName([agents[0]], undefined)).toBeUndefined();
  });
});

describe("OperatorQueueCard", () => {
  it("renders the design-gate anatomy and routes to the selected agent", async () => {
    const callbacks = handlers();
    const agents = [
      agent({ name: "architect-1", role: "plan" }),
      agent({ name: "agent-dev-1", role: "dev", status: "idle" }),
    ];
    render(
      <OperatorQueueCard
        item={item("design-gate")}
        agents={agents}
        {...callbacks}
      />,
    );

    const card = screen.getByTestId("queue-card");
    expect(card).toHaveAttribute("data-kind", "design-gate");
    expect(card).toHaveAttribute("data-issue-id", "TASK-1");
    expect(screen.getByText("Design gate")).toBeInTheDocument();
    expect(screen.getByTestId("queue-repo")).toHaveTextContent("source-repo");
    expect(card).toHaveTextContent("architect-1 attached a design");
    expect(screen.getByText(/design attached/)).toBeInTheDocument();
    expect(screen.getByText(/one atomic write/)).toHaveTextContent(
      "reopen · -label needs-revision · assignee = agent-dev-1",
    );
    expect(screen.getByTitle("coming later")).toBeDisabled();

    fireEvent.click(screen.getByTestId("queue-approve"));

    await waitFor(() => {
      expect(callbacks.onApprove).toHaveBeenCalledWith(
        expect.objectContaining({ id: "TASK-1" }),
        "agent-dev-1",
      );
    });
  });

  it("uses the picker to change the route without splitting approval", async () => {
    const callbacks = handlers();
    render(
      <OperatorQueueCard
        item={item("design-gate")}
        agents={[
          agent({ name: "agent-dev-1", role: "dev" }),
          agent({ name: "agent-dev-2", role: "dev" }),
        ]}
        {...callbacks}
      />,
    );

    fireEvent.change(screen.getByTestId("queue-agent-picker"), {
      target: { value: "agent-dev-2" },
    });
    expect(screen.getByTestId("queue-approve")).toHaveTextContent(
      "Approve → agent-dev-2",
    );

    fireEvent.click(screen.getByTestId("queue-approve"));

    await waitFor(() =>
      expect(callbacks.onApprove).toHaveBeenCalledWith(
        expect.anything(),
        "agent-dev-2",
      ),
    );
  });

  it("shows the repo name and only offers agents serving that repo", () => {
    const callbacks = handlers();
    mockWorkspaceContext.repos = [
      {
        name: "Source repository",
        source_repo_id: "source-repo",
        default_branch: "main",
      },
      {
        name: "Web application",
        source_repo_id: "web",
        default_branch: "develop",
      },
    ];
    render(
      <OperatorQueueCard
        item={item("design-gate")}
        agents={[
          agent({ name: "source-agent", role: "dev" }),
          agent({ name: "web-agent", role: "dev", repo: "web" }),
        ]}
        {...callbacks}
      />,
    );

    expect(screen.getByTestId("queue-repo")).toHaveTextContent(
      "Source repository",
    );
    expect(
      screen.getByRole("option", { name: "source-agent" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("option", { name: "web-agent" }),
    ).not.toBeInTheDocument();
  });

  it("honestly approves without assignment when the workspace has no agents", async () => {
    const callbacks = handlers();
    render(
      <OperatorQueueCard
        item={item("design-gate")}
        agents={[]}
        {...callbacks}
      />,
    );

    expect(screen.queryByTestId("queue-agent-picker")).not.toBeInTheDocument();
    expect(screen.getByTestId("queue-approve")).toHaveTextContent(
      "Approve without routing — no agent serves source-repo",
    );
    expect(screen.getByTestId("queue-no-agent-for-repo")).toHaveTextContent(
      "No agent serves source-repo, so this is not routed.",
    );
    // Not the primary call to action: it cannot route anywhere.
    expect(screen.getByTestId("queue-approve")).toHaveAttribute(
      "data-routed",
      "false",
    );

    fireEvent.click(screen.getByTestId("queue-approve"));

    await waitFor(() =>
      expect(callbacks.onApprove).toHaveBeenCalledWith(
        expect.anything(),
        undefined,
      ),
    );
  });

  it("does not route a source-repo task to an agent serving another repo", async () => {
    const callbacks = handlers();
    render(
      <OperatorQueueCard
        item={item("design-gate")}
        agents={[agent({ name: "web-agent", role: "dev", repo: "web" })]}
        {...callbacks}
      />,
    );

    expect(screen.queryByTestId("queue-agent-picker")).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId("queue-approve"));
    await waitFor(() =>
      expect(callbacks.onApprove).toHaveBeenCalledWith(
        expect.anything(),
        undefined,
      ),
    );
  });

  it("treats a repo-less task as legitimate: chip is neutral, picker offers repo-free agents", () => {
    const callbacks = handlers();
    render(
      <OperatorQueueCard
        item={item("design-gate", { source_repo: undefined })}
        agents={[
          agent({ name: "source-agent", role: "dev" }),
          agent({ name: "researcher", role: "task", repo: "" }),
        ]}
        {...callbacks}
      />,
    );

    expect(screen.getByTestId("queue-repo")).toHaveTextContent("no repo");
    expect(screen.getByTestId("queue-repo")).toHaveAttribute(
      "title",
      "Not tied to a repo. Claimable by agents without a repo binding.",
    );
    expect(screen.getByTestId("queue-approve")).toHaveTextContent(
      "Approve → researcher",
    );
    const options = screen
      .getAllByRole("option")
      .map((option) => option.textContent);
    expect(options).toEqual(["researcher"]);
  });

  it("says why a repo-less task cannot be routed when every agent is repo-bound", () => {
    const callbacks = handlers();
    render(
      <OperatorQueueCard
        item={item("design-gate", { source_repo: undefined })}
        agents={[agent({ name: "source-agent", role: "dev" })]}
        {...callbacks}
      />,
    );

    expect(screen.getByTestId("queue-approve")).toHaveTextContent(
      "Approve without routing — no repo-free agent is available",
    );
    expect(screen.getByTestId("queue-no-agent-for-repo")).toHaveTextContent(
      "No repo-free agent is available, so this is not routed.",
    );
  });

  it("quotes a blocked declaration without its BLOCKED prefix and can unblock", async () => {
    const callbacks = handlers();
    render(
      <OperatorQueueCard
        item={item("blocked", {
          status: "blocked",
          notes: "BLOCKED: no Go toolchain in the image",
          has_design: false,
          labels: [],
          assignee: "eval-engineer-1",
        })}
        agents={[]}
        {...callbacks}
      />,
    );

    expect(
      screen.getByText("“no Go toolchain in the image”"),
    ).toBeInTheDocument();
    expect(screen.queryByText(/BLOCKED:/)).not.toBeInTheDocument();
    expect(screen.getByText(/Unblock is one write/)).toHaveTextContent(
      "reopen · assignee = eval-engineer-1 — the agent resumes from the ready queue.",
    );

    fireEvent.click(screen.getByTestId("queue-unblock"));
    await waitFor(() =>
      expect(callbacks.onUnblock).toHaveBeenCalledWith(
        expect.objectContaining({ id: "TASK-1" }),
      ),
    );
  });

  it("explains when the carried blocked assignee serves another repo", () => {
    const callbacks = handlers();
    render(
      <OperatorQueueCard
        item={item("blocked", {
          status: "blocked",
          notes: "BLOCKED: waiting",
          has_design: false,
          labels: [],
          assignee: "web-agent",
        })}
        agents={[agent({ name: "web-agent", repo: "web" })]}
        {...callbacks}
      />,
    );

    expect(screen.getByText(/Unblock is one write/)).toHaveTextContent(
      "web-agent serves web, so it will not resume this task.",
    );
  });

  it("opens needs-revision tasks for operator review", () => {
    const callbacks = handlers();
    render(
      <OperatorQueueCard
        item={item("needs-revision", {
          status: "open",
          labels: ["needs-revision"],
          has_design: true,
        })}
        agents={[]}
        {...callbacks}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Review task" }));

    expect(callbacks.onOpenIssue).toHaveBeenCalledWith(
      expect.objectContaining({ id: "TASK-1" }),
    );
  });

  it("does not invent an actor for an unassigned revision bounce", () => {
    const callbacks = handlers();
    render(
      <OperatorQueueCard
        item={item("needs-revision", {
          status: "open",
          labels: ["needs-revision"],
          assignee: undefined,
        })}
        agents={[]}
        {...callbacks}
      />,
    );

    expect(screen.getByTestId("queue-card")).toHaveTextContent(
      /Sent back for revision at .* Waiting for operator arbitration\./,
    );
    expect(screen.queryByText(/An agent/)).not.toBeInTheDocument();
  });
});
