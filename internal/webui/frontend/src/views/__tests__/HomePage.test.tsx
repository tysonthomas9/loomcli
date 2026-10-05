/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type { OperatorQueueCardProps } from "@/components/OperatorQueue";
import {
  NO_WORKSPACE_VIEW_ACTIONS,
  NO_WORKSPACE_VIEW_DATA,
} from "@/contexts/WorkspaceViewContext";
import type { Agent } from "@/api/agentsv1";
import type { Issue, LoomAgentStatus } from "@/types";

const mockUpdateIssue = vi.hoisted(() => vi.fn());
const mockRoster = vi.hoisted(() => ({ current: new Map() }));

const mockData = {
  ...NO_WORKSPACE_VIEW_DATA,
  activeView: "home" as const,
  workspaceId: "workspace-1",
};
const mockActions = {
  ...NO_WORKSPACE_VIEW_ACTIONS,
  refetch: vi.fn(),
  handleIssueClick: vi.fn(),
  handleReject: vi.fn().mockResolvedValue(undefined),
  showToast: vi.fn(() => "toast-id"),
};

vi.mock("@/api", () => ({
  updateIssue: mockUpdateIssue,
}));

vi.mock("@/contexts/WorkspaceViewContext", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@/contexts/WorkspaceViewContext")>();
  return {
    ...actual,
    useWorkspaceViewData: () => mockData,
    useWorkspaceViewActions: () => mockActions,
  };
});

vi.mock("@/components", () => ({
  ErrorBoundary: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="error-boundary">{children}</div>
  ),
  EmptyState: ({ variant }: { variant: string }) => (
    <div data-testid="empty-state" data-variant={variant}>
      Queue clear
    </div>
  ),
  OperatorQueueCard: ({
    item,
    onApprove,
    onReject,
    onUnblock,
    onOpenIssue,
  }: OperatorQueueCardProps) => (
    <article
      data-testid="queue-card"
      data-kind={item.kind}
      data-issue-id={item.issue.id}
    >
      {item.issue.title}
      {item.kind === "design-gate" && (
        <>
          <button
            type="button"
            data-testid="queue-approve"
            onClick={() => void onApprove(item.issue, "agent-dev-1")}
          >
            Approve
          </button>
          <button
            type="button"
            data-testid="queue-reject"
            onClick={() => void onReject(item.issue, "Needs revision")}
          >
            Send back
          </button>
        </>
      )}
      {item.kind === "blocked" && (
        <button
          type="button"
          data-testid="queue-unblock"
          onClick={() => void onUnblock(item.issue)}
        >
          Unblock
        </button>
      )}
      <button type="button" onClick={() => onOpenIssue(item.issue)}>
        Open
      </button>
    </article>
  ),
  deriveThisWorkspaceCounts: (issues: Issue[]) => ({
    closed: issues.filter((issue) => issue.status === "closed").length,
  }),
  HomeRail: () => <aside data-testid="home-rail" />,
}));

vi.mock("@/hooks/workspace", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/hooks/workspace")>();
  return { ...actual, useRecentActivity: () => [] };
});

// The sidebar's shared Agent API roster (RAIL1).
vi.mock("@/hooks/agents/useAgentRoster", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@/hooks/agents/useAgentRoster")>();
  return { ...actual, useRoster: () => mockRoster.current };
});

vi.mock("@/components/IssueViewGuard", () => ({
  IssueViewGuard: ({
    isLoading,
    error,
    children,
  }: {
    isLoading: boolean;
    error: string | null;
    children: React.ReactNode;
  }) => {
    if (isLoading) return <div data-testid="loading-container" />;
    if (error) return <div role="alert">{error}</div>;
    return <>{children}</>;
  },
}));

import { HomePage } from "../HomePage";

function issue(overrides: Partial<Issue>): Issue {
  return {
    id: "TASK-1",
    title: "Queue task",
    priority: 2,
    issue_type: "task",
    status: "open",
    created_at: "2026-08-21T15:00:00.000Z",
    updated_at: "2026-08-21T15:00:00.000Z",
    ...overrides,
  } as Issue;
}

beforeEach(() => {
  vi.clearAllMocks();
  mockUpdateIssue.mockResolvedValue({});
  mockActions.refetch.mockResolvedValue(undefined);
  mockData.issues = [];
  mockData.agents = [];
  mockRoster.current = new Map();
  mockData.isLoading = false;
  mockData.error = null;
});

describe("HomePage", () => {
  it("renders the ranked operator queue and count", () => {
    mockData.issues = [
      issue({
        id: "NEWER",
        title: "Newer block",
        status: "blocked",
        notes: "BLOCKED: waiting",
        updated_at: "2026-08-21T15:20:00.000Z",
      }),
      issue({
        id: "OLDER",
        title: "Older design",
        status: "review",
        has_design: true,
        updated_at: "2026-08-21T15:10:00.000Z",
      }),
    ];

    render(<HomePage />);

    expect(screen.getByTestId("home-page")).toBeInTheDocument();
    expect(screen.getByTestId("home-rail")).toBeInTheDocument();
    expect(screen.getByTestId("operator-queue")).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Needs you" }),
    ).toBeInTheDocument();
    expect(screen.getByText("2")).toBeInTheDocument();
    expect(
      screen.getAllByTestId("queue-card").map((card) => card.dataset.issueId),
    ).toEqual(["OLDER", "NEWER"]);
  });

  it("sends approve, reopen, label removal, and assignment in one request", async () => {
    mockData.issues = [
      issue({ id: "DESIGN-1", status: "review", has_design: true }),
    ];
    render(<HomePage />);

    fireEvent.click(screen.getByTestId("queue-approve"));

    await waitFor(() => {
      expect(mockUpdateIssue).toHaveBeenCalledTimes(1);
      expect(mockUpdateIssue).toHaveBeenCalledWith("workspace-1", "DESIGN-1", {
        status: "open",
        remove_labels: ["needs-revision"],
        assignee: "agent-dev-1",
      });
      expect(mockActions.refetch).toHaveBeenCalledTimes(1);
    });
  });

  it("delegates send-back feedback to the canonical rejection action", async () => {
    mockData.issues = [
      issue({ id: "DESIGN-1", status: "review", has_design: true }),
    ];
    render(<HomePage />);

    fireEvent.click(screen.getByTestId("queue-reject"));

    await waitFor(() =>
      expect(mockActions.handleReject).toHaveBeenCalledWith(
        expect.objectContaining({ id: "DESIGN-1" }),
        "Needs revision",
      ),
    );
  });

  // PUPPET-146: `handleReject` re-throws, and the queue card only has a
  // try/finally around it. Without this catch the failure reaches nothing but
  // the global unhandled-rejection reporter.
  it("toasts when the canonical rejection action fails", async () => {
    mockData.issues = [
      issue({ id: "DESIGN-1", status: "review", has_design: true }),
    ];
    mockActions.handleReject.mockRejectedValueOnce(
      new Error("issue is not claimable"),
    );
    render(<HomePage />);

    fireEvent.click(screen.getByTestId("queue-reject"));

    await waitFor(() =>
      expect(mockActions.showToast).toHaveBeenCalledWith(
        "issue is not claimable",
        { type: "error" },
      ),
    );
  });

  it("reopens a blocked task and retains its assignee in the same write", async () => {
    mockData.issues = [
      issue({
        id: "BLOCKED-1",
        status: "blocked",
        notes: "BLOCKED: waiting for credentials",
        assignee: "agent-dev-1",
      }),
    ];
    render(<HomePage />);

    fireEvent.click(screen.getByTestId("queue-unblock"));

    await waitFor(() => {
      expect(mockUpdateIssue).toHaveBeenCalledWith("workspace-1", "BLOCKED-1", {
        status: "open",
        assignee: "agent-dev-1",
      });
      expect(mockActions.refetch).toHaveBeenCalledTimes(1);
      expect(mockActions.showToast).toHaveBeenCalledWith(
        "Unblocked BLOCKED-1",
        {
          type: "success",
        },
      );
      expect(screen.queryByTestId("queue-card")).not.toBeInTheDocument();
    });
  });

  it("surfaces an unblock failure", async () => {
    mockData.issues = [
      issue({
        id: "BLOCKED-1",
        status: "blocked",
        notes: "BLOCKED: waiting for credentials",
      }),
    ];
    mockUpdateIssue.mockRejectedValue(new Error("Could not reopen"));
    render(<HomePage />);

    fireEvent.click(screen.getByTestId("queue-unblock"));

    await waitFor(() =>
      expect(mockActions.showToast).toHaveBeenCalledWith("Could not reopen", {
        type: "error",
      }),
    );
    expect(screen.getByTestId("queue-card")).toBeInTheDocument();
  });

  it("reopens an unassigned blocked task without fabricating an assignee", async () => {
    mockData.issues = [
      issue({
        id: "BLOCKED-1",
        status: "blocked",
        notes: "BLOCKED: waiting for credentials",
      }),
    ];
    render(<HomePage />);

    fireEvent.click(screen.getByTestId("queue-unblock"));

    await waitFor(() =>
      expect(mockUpdateIssue).toHaveBeenCalledWith("workspace-1", "BLOCKED-1", {
        status: "open",
      }),
    );
  });

  it("opens issue details from a queue card", () => {
    mockData.issues = [
      issue({
        id: "REVISION-1",
        status: "open",
        labels: ["needs-revision"],
      }),
    ];
    render(<HomePage />);

    fireEvent.click(screen.getByRole("button", { name: "Open" }));

    expect(mockActions.handleIssueClick).toHaveBeenCalledWith(
      expect.objectContaining({ id: "REVISION-1" }),
    );
  });

  it("shows the honest queue-clear state when no issue needs the operator", () => {
    mockData.issues = [issue({ id: "RUNNING", status: "in_progress" })];

    render(<HomePage />);

    expect(screen.getByTestId("queue-empty")).toBeInTheDocument();
    expect(screen.getByTestId("empty-state")).toHaveAttribute(
      "data-variant",
      "queue-clear",
    );
    expect(screen.queryByTestId("operator-queue")).not.toBeInTheDocument();
  });

  it("counts the sidebar's idle Agent API agents with the fleet's in the idle stat", () => {
    const api = (id: string, over: Partial<Agent> = {}): Agent =>
      ({
        agent_id: id,
        name: id,
        harness: "opencode",
        preset: "lead",
        role_kind: "interactive",
        state: "idle",
        parent_agent_id: null,
        created_at: "2026-10-04T00:00:00Z",
        ...over,
      }) as Agent;
    mockData.agents = [
      { name: "local-coder", status: "idle" },
      { name: "local-planner", status: "idle" },
    ] as LoomAgentStatus[];
    // Two idle Leads and a finished Lead count; a working Lead, its working
    // child, a finished (hidden) child and an archived Lead do not.
    mockRoster.current = new Map(
      [
        api("l1"),
        api("l2", { state: "finished", outcome: "completed" }),
        api("l3", { state: "waiting" }),
        api("l4"),
        api("l5", { state: "active" }),
        api("k1", { parent_agent_id: "l5", state: "active", preset: "task" }),
        api("k2", { parent_agent_id: "l1", state: "finished", preset: "task" }),
        api("gone", { state: "archived" }),
      ].map((a) => [a.agent_id, a]),
    );

    render(<HomePage />);

    const idle = screen
      .getAllByTestId("queue-stat")
      .find((el) => el.dataset.stat === "idle");
    expect(idle).toHaveTextContent("5 agents");
  });

  it("uses the standard loading and error guard states", () => {
    mockData.isLoading = true;
    const { rerender } = render(<HomePage />);
    expect(screen.getByTestId("loading-container")).toBeInTheDocument();

    mockData.isLoading = false;
    mockData.error = "Could not load issues";
    rerender(<HomePage />);
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Could not load issues",
    );
  });
});
