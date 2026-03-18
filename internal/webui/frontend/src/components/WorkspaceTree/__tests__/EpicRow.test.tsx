/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for EpicRow component.
 * Covers title and status dot rendering, collapse toggle,
 * and nested TaskRow children rendering.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { Issue } from "@/types";

import { EpicRow } from "../EpicRow";

// Mock useIssueDiffStat since TaskRow (rendered as child) uses it.
vi.mock("@/hooks", () => ({
  useIssueDiffStat: vi.fn(() => ({
    data: null,
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  })),
}));

/** Helper to create a test Issue. */
function makeIssue(
  overrides: Partial<Issue> & { id: string; title: string },
): Issue {
  return {
    priority: 2,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

describe("EpicRow", () => {
  const epic = makeIssue({
    id: "epic-1",
    title: "My Epic",
    issue_type: "epic",
    status: "in_progress",
  });
  const tasks = [
    makeIssue({
      id: "task-1",
      title: "Task One",
      issue_type: "task",
      status: "open",
      parent: "epic-1",
    }),
    makeIssue({
      id: "task-2",
      title: "Task Two",
      issue_type: "task",
      status: "review",
      parent: "epic-1",
    }),
  ];

  it("renders epic title", () => {
    render(
      <EpicRow
        epic={epic}
        tasks={tasks}
        isCollapsed={false}
        onToggle={vi.fn()}
      />,
    );
    expect(screen.getByText("My Epic")).toBeInTheDocument();
  });

  it("renders status dot with correct data-status", () => {
    const { container } = render(
      <EpicRow
        epic={epic}
        tasks={tasks}
        isCollapsed={false}
        onToggle={vi.fn()}
      />,
    );
    const statusDot = container.querySelector("[data-status='in_progress']");
    expect(statusDot).toBeInTheDocument();
  });

  it("renders status dot for open epic", () => {
    const openEpic = makeIssue({
      id: "epic-open",
      title: "Open Epic",
      issue_type: "epic",
      status: "open",
    });
    const { container } = render(
      <EpicRow
        epic={openEpic}
        tasks={[]}
        isCollapsed={false}
        onToggle={vi.fn()}
      />,
    );
    const statusDot = container.querySelector("[data-status='open']");
    expect(statusDot).toBeInTheDocument();
  });

  it("renders status dot for closed epic", () => {
    const closedEpic = makeIssue({
      id: "epic-closed",
      title: "Closed Epic",
      issue_type: "epic",
      status: "closed",
    });
    const { container } = render(
      <EpicRow
        epic={closedEpic}
        tasks={[]}
        isCollapsed={false}
        onToggle={vi.fn()}
      />,
    );
    const statusDot = container.querySelector("[data-status='closed']");
    expect(statusDot).toBeInTheDocument();
  });

  it("shows task children when expanded (isCollapsed=false)", () => {
    render(
      <EpicRow
        epic={epic}
        tasks={tasks}
        isCollapsed={false}
        onToggle={vi.fn()}
      />,
    );
    expect(screen.getByText("Task One")).toBeInTheDocument();
    expect(screen.getByText("Task Two")).toBeInTheDocument();
  });

  it("hides task children when collapsed (isCollapsed=true)", () => {
    render(
      <EpicRow
        epic={epic}
        tasks={tasks}
        isCollapsed={true}
        onToggle={vi.fn()}
      />,
    );
    expect(screen.queryByText("Task One")).not.toBeInTheDocument();
    expect(screen.queryByText("Task Two")).not.toBeInTheDocument();
  });

  it("calls onToggle when epic row is clicked", () => {
    const onToggle = vi.fn();
    render(
      <EpicRow
        epic={epic}
        tasks={tasks}
        isCollapsed={false}
        onToggle={onToggle}
      />,
    );
    fireEvent.click(screen.getByText("My Epic"));
    expect(onToggle).toHaveBeenCalledTimes(1);
  });

  it("shows collapse chevron with correct aria-label when expanded", () => {
    render(
      <EpicRow
        epic={epic}
        tasks={tasks}
        isCollapsed={false}
        onToggle={vi.fn()}
      />,
    );
    expect(screen.getByLabelText("Collapse epic")).toBeInTheDocument();
  });

  it("shows collapse chevron with correct aria-label when collapsed", () => {
    render(
      <EpicRow
        epic={epic}
        tasks={tasks}
        isCollapsed={true}
        onToggle={vi.fn()}
      />,
    );
    expect(screen.getByLabelText("Expand epic")).toBeInTheDocument();
  });

  it("does not render children section when tasks array is empty", () => {
    const { container } = render(
      <EpicRow epic={epic} tasks={[]} isCollapsed={false} onToggle={vi.fn()} />,
    );
    // epicChildren div should not be present when no tasks
    const childrenDiv = container.querySelector("[class*='epicChildren']");
    expect(childrenDiv).not.toBeInTheDocument();
  });

  it("sets button title to epic title", () => {
    render(
      <EpicRow
        epic={epic}
        tasks={tasks}
        isCollapsed={false}
        onToggle={vi.fn()}
      />,
    );
    expect(screen.getByTitle("My Epic")).toBeInTheDocument();
  });

  it("passes selectedId and onSelect to child TaskRows", () => {
    const onSelect = vi.fn();
    render(
      <EpicRow
        epic={epic}
        tasks={tasks}
        isCollapsed={false}
        onToggle={vi.fn()}
        selectedId="task-1"
        onSelect={onSelect}
      />,
    );
    // Task One should have the selected class
    const taskOneButton = screen.getByTitle("Task One");
    expect(taskOneButton.className).toContain("taskRowSelected");

    // Task Two should not have the selected class
    const taskTwoButton = screen.getByTitle("Task Two");
    expect(taskTwoButton.className).not.toContain("taskRowSelected");

    // Click Task Two should call onSelect
    fireEvent.click(taskTwoButton);
    expect(onSelect).toHaveBeenCalledWith("task-2");
  });
});
