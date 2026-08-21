/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type { OperatorQueueItem } from "@/hooks/issues";
import type { Issue, LoomAgentStatus } from "@/types";

import { OperatorQueueCard, pickDefaultAgentName } from "../OperatorQueueCard";

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

describe("pickDefaultAgentName", () => {
  it("prefers an idle implementation agent, then falls back to the first", () => {
    const agents = [
      agent({ name: "planner", role: "plan" }),
      agent({ name: "busy-dev", role: "dev", status: "working: TASK-2" }),
      agent({ name: "idle-coder", role: "coder", status: "idle" }),
    ];

    expect(pickDefaultAgentName(agents)).toBe("idle-coder");
    expect(pickDefaultAgentName(agents.slice(0, 2))).toBe("planner");
    expect(pickDefaultAgentName([])).toBeUndefined();
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
      "Approve — no agent to route to",
    );

    fireEvent.click(screen.getByTestId("queue-approve"));

    await waitFor(() =>
      expect(callbacks.onApprove).toHaveBeenCalledWith(
        expect.anything(),
        undefined,
      ),
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

    fireEvent.click(screen.getByTestId("queue-unblock"));
    await waitFor(() =>
      expect(callbacks.onUnblock).toHaveBeenCalledWith(
        expect.objectContaining({ id: "TASK-1" }),
      ),
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
});
