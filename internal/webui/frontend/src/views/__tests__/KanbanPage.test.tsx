/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { Issue, Status } from "@/types";

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
}));

import { KanbanPage } from "../KanbanPage";

const baseProps = {
  filteredIssues: [] as Issue[],
  groupBy: "none" as const,
  onDragEnd: vi.fn() as (
    issueId: string,
    newStatus: Status,
    oldStatus: Status,
  ) => void,
  onIssueClick: vi.fn() as (issue: Issue) => void,
  isMultiRepo: false,
  activeView: "kanban" as const,
};

describe("KanbanPage", () => {
  it("renders without crashing", () => {
    const { container } = render(<KanbanPage {...baseProps} />);
    expect(container).toBeTruthy();
  });

  it("renders the SwimLaneBoard inside an ErrorBoundary", () => {
    render(<KanbanPage {...baseProps} />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
    expect(screen.getByTestId("swim-lane-board")).toBeInTheDocument();
  });

  it("wraps content in a kanbanShell div", () => {
    const { container } = render(<KanbanPage {...baseProps} />);
    // The kanbanShell div is inside the ErrorBoundary
    const shell = container.querySelector("div > div > div");
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
    render(<KanbanPage {...baseProps} filteredIssues={issues} />);
    const board = screen.getByTestId("swim-lane-board");
    expect(board.getAttribute("data-issues")).toContain("Test");
  });
});
