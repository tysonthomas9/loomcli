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

// Mock hooks used by EpicRow and its children.
vi.mock("@/hooks", () => ({
  useIssueDiffStat: vi.fn(() => ({
    data: null,
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  })),
}));

vi.mock("@/hooks/ui", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/ui")>("@/hooks/ui");
  return {
    ...actual,
    useToast: vi.fn(() => ({
      showToast: vi.fn(),
      dismissToast: vi.fn(),
      toasts: [],
    })),
  };
});

vi.mock("@/hooks/issues", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/issues")>("@/hooks/issues");
  return {
    ...actual,
    useInlineCreate: vi.fn(() => ({
      isAdding: false,
      isSubmitting: false,
      error: null,
      startAdding: vi.fn(),
      cancelAdding: vi.fn(),
      submitTitle: vi.fn(),
    })),
  };
});

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

  it("renders add task button when tasks array is empty and expanded", () => {
    render(
      <EpicRow epic={epic} tasks={[]} isCollapsed={false} onToggle={vi.fn()} />,
    );
    // Even with no tasks, the "+ Add task" button should be visible
    expect(screen.getByText("+ Add task")).toBeInTheDocument();
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
    // Task One's wrapper div should have the selected class
    const taskOneButton = screen.getByTitle("Task One");
    // The outer div is the parent of the button
    const taskOneWrapper = taskOneButton.parentElement;
    expect(taskOneWrapper?.className).toContain("taskRowSelected");

    // Task Two should not have the selected class
    const taskTwoButton = screen.getByTitle("Task Two");
    const taskTwoWrapper = taskTwoButton.parentElement;
    expect(taskTwoWrapper?.className).not.toContain("taskRowSelected");

    // Click Task Two should call onSelect
    fireEvent.click(taskTwoButton);
    expect(onSelect).toHaveBeenCalledWith("task-2");
  });

  // ── onTaskTerminalOpen pass-through (V7 Terminal View) ──────────────────

  describe("onTaskTerminalOpen", () => {
    it("passes onTaskTerminalOpen to child TaskRows via buildTaskProps", () => {
      const onTaskTerminalOpen = vi.fn();
      const tasksWithAssignee = [
        makeIssue({
          id: "task-a",
          title: "Assigned Task",
          issue_type: "task",
          status: "in_progress",
          parent: "epic-1",
          assignee: "agent-alpha",
        }),
      ];
      render(
        <EpicRow
          epic={epic}
          tasks={tasksWithAssignee}
          isCollapsed={false}
          onToggle={vi.fn()}
          onTaskTerminalOpen={onTaskTerminalOpen}
        />,
      );

      // Click on the assigned task — should trigger onTaskTerminalOpen
      fireEvent.click(screen.getByTitle("Assigned Task"));
      expect(onTaskTerminalOpen).toHaveBeenCalledWith("task-a", "agent-alpha");
    });

    it("falls back to onSelect when task has no assignee even with onTaskTerminalOpen", () => {
      const onTaskTerminalOpen = vi.fn();
      const onSelect = vi.fn();
      render(
        <EpicRow
          epic={epic}
          tasks={tasks}
          isCollapsed={false}
          onToggle={vi.fn()}
          onSelect={onSelect}
          onTaskTerminalOpen={onTaskTerminalOpen}
        />,
      );

      // Tasks in `tasks` have no assignee, so clicking should call onSelect
      fireEvent.click(screen.getByTitle("Task One"));
      expect(onSelect).toHaveBeenCalledWith("task-1");
      expect(onTaskTerminalOpen).not.toHaveBeenCalled();
    });
  });
});
