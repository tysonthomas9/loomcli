/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { Issue } from "@/types";
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
  SwimLaneBoard: (props: Record<string, unknown>) => (
    <div
      data-testid="swim-lane-board"
      data-issues={JSON.stringify(props.issues)}
    />
  ),
  AssigneePrompt: () => <div data-testid="assignee-prompt" />,
}));

vi.mock("@/components/IssueViewGuard", () => ({
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

import { KanbanPage } from "../boards/KanbanPage";

describe("KanbanPage", () => {
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
});
