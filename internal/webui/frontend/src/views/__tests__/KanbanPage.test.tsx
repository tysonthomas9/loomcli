/**
 * @vitest-environment jsdom
 */

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import "@testing-library/jest-dom";

import type { Issue } from "@/types";
import { updateIssue } from "@/api";
import { NO_STORE_CONTEXT } from "@/hooks/common";
import {
  NO_WORKSPACE_VIEW_DATA,
  NO_WORKSPACE_VIEW_ACTIONS,
} from "@/contexts/WorkspaceViewContext";

const mockData = { ...NO_WORKSPACE_VIEW_DATA, activeView: "kanban" as const };
const mockActions = { ...NO_WORKSPACE_VIEW_ACTIONS };

vi.mock("@/contexts/WorkspaceViewContext", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@/contexts/WorkspaceViewContext")>();
  return {
    ...actual,
    useWorkspaceViewData: () => mockData,
    useWorkspaceViewActions: () => mockActions,
  };
});

// Mock child components to avoid deep rendering
vi.mock("@/components", () => ({
  ErrorBoundary: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="error-boundary">{children}</div>
  ),
  SwimLaneBoard: (props: Record<string, unknown>) => {
    const onDragEnd = props.onDragEnd as (
      issueId: string,
      newStatus: string,
      oldStatus: string,
    ) => Promise<void>;
    return (
      <div
        data-testid="swim-lane-board"
        data-issues={JSON.stringify(props.issues)}
      >
        <button
          data-testid="drag-open-to-progress"
          onClick={() => void onDragEnd("task-1", "in_progress", "open")}
        >
          Simulate drag
        </button>
      </div>
    );
  },
  AssigneePrompt: (props: Record<string, unknown>) => {
    if (!props.isOpen) return null;
    const onConfirm = props.onConfirm as (assignee: string) => Promise<void>;
    return (
      <button
        data-testid="confirm-assignee"
        onClick={() => void onConfirm("[H] Tyson")}
      >
        Confirm assignee
      </button>
    );
  },
}));

vi.mock("@/components/IssueViewGuard/IssueViewGuard", () => ({
  IssueViewGuard: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="issue-view-guard">{children}</div>
  ),
}));

vi.mock("@/hooks", () => ({
  useRecentAssignees: () => ({
    recentAssignees: [],
    addRecentAssignee: vi.fn(),
  }),
}));

vi.mock("@/api", () => ({
  updateIssue: vi.fn(),
}));

import { KanbanPage } from "../KanbanPage";

describe("KanbanPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    NO_STORE_CONTEXT.issueStore.getState().reset();
    mockData.filteredIssues = [];
    mockData.issues = [];
  });

  afterEach(() => {
    NO_STORE_CONTEXT.issueStore.getState().reset();
  });

  it("renders without crashing", () => {
    const { container } = render(<KanbanPage />);
    expect(container).toBeTruthy();
  });

  it("renders the SwimLaneBoard inside an ErrorBoundary", () => {
    render(<KanbanPage />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
    expect(screen.getByTestId("swim-lane-board")).toBeInTheDocument();
  });

  it("wraps content in a kanbanShell div", () => {
    const { container } = render(<KanbanPage />);
    const shell = container.querySelector("div > div > div > div");
    expect(shell).toBeTruthy();
  });

  it("passes issues to SwimLaneBoard", () => {
    const issues = [
      {
        id: "1",
        title: "Test",
        priority: "medium",
        created_at: "",
        updated_at: "",
      },
    ] as Issue[];
    mockData.filteredIssues = issues;
    render(<KanbanPage />);
    const board = screen.getByTestId("swim-lane-board");
    expect(board.getAttribute("data-issues")).toContain("Test");
    mockData.filteredIssues = [];
  });

  it("reconciles the confirmed Open to In Progress assignment into the kanban store", async () => {
    const issue = {
      id: "task-1",
      title: "Status proof",
      status: "open",
      priority: 2,
      created_at: "2026-08-03T00:00:00Z",
      updated_at: "2026-08-03T00:00:00Z",
    } satisfies Issue;
    const confirmed = {
      ...issue,
      status: "in_progress" as const,
      assignee: "[H] Tyson",
      updated_at: "2026-08-03T00:01:00Z",
    };
    mockData.filteredIssues = [issue];
    mockData.issues = [issue];
    NO_STORE_CONTEXT.issueStore.setState({
      issuesMap: new Map([[issue.id, issue]]),
    });
    vi.mocked(updateIssue).mockResolvedValueOnce(confirmed);

    render(<KanbanPage />);
    fireEvent.click(screen.getByTestId("drag-open-to-progress"));
    fireEvent.click(await screen.findByTestId("confirm-assignee"));

    await waitFor(() => {
      expect(
        NO_STORE_CONTEXT.issueStore.getState().issuesMap.get(issue.id),
      ).toMatchObject({ status: "in_progress", assignee: "[H] Tyson" });
    });
  });
});
