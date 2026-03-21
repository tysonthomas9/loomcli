/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for TaskRow component.
 * Covers title rendering, status dot, selected state,
 * onSelect callback, assignee chip, and diff stats display.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { Issue } from "@/types";

import { TaskRow } from "../TaskRow";

vi.mock("@/hooks", () => ({
  useIssueDiffStat: vi.fn(() => ({
    data: null,
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  })),
}));

import { useIssueDiffStat } from "@/hooks";

const mockUseIssueDiffStat = vi.mocked(useIssueDiffStat);

/** Helper to create a test Issue. */
function makeTask(overrides?: Partial<Issue>): Issue {
  return {
    id: "task-1",
    title: "Test Task",
    priority: 2,
    issue_type: "task",
    status: "open",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

describe("TaskRow", () => {
  it("renders task title", () => {
    render(
      <TaskRow task={makeTask({ title: "Fix the bug" })} isSelected={false} />,
    );
    expect(screen.getByText("Fix the bug")).toBeInTheDocument();
  });

  it("renders status dot with correct data-status attribute", () => {
    const { container } = render(
      <TaskRow task={makeTask({ status: "in_progress" })} isSelected={false} />,
    );
    const statusDot = container.querySelector("[data-status='in_progress']");
    expect(statusDot).toBeInTheDocument();
  });

  it("renders status dot for review status", () => {
    const { container } = render(
      <TaskRow task={makeTask({ status: "review" })} isSelected={false} />,
    );
    const statusDot = container.querySelector("[data-status='review']");
    expect(statusDot).toBeInTheDocument();
  });

  it("renders status dot for blocked status", () => {
    const { container } = render(
      <TaskRow task={makeTask({ status: "blocked" })} isSelected={false} />,
    );
    const statusDot = container.querySelector("[data-status='blocked']");
    expect(statusDot).toBeInTheDocument();
  });

  it("applies selected class when isSelected is true", () => {
    const { container } = render(
      <TaskRow task={makeTask()} isSelected={true} />,
    );
    // taskRowSelected is on the outer div wrapper
    const wrapper = container.firstElementChild;
    expect(wrapper?.className).toContain("taskRowSelected");
  });

  it("does not apply selected class when isSelected is false", () => {
    const { container } = render(
      <TaskRow task={makeTask()} isSelected={false} />,
    );
    const wrapper = container.firstElementChild;
    expect(wrapper?.className).not.toContain("taskRowSelected");
  });

  it("calls onSelect with task id on click", () => {
    const onSelect = vi.fn();
    render(
      <TaskRow
        task={makeTask({ id: "task-42" })}
        isSelected={false}
        onSelect={onSelect}
      />,
    );
    fireEvent.click(screen.getByText("Test Task"));
    expect(onSelect).toHaveBeenCalledWith("task-42");
  });

  it("does not throw when onSelect is not provided and button is clicked", () => {
    render(<TaskRow task={makeTask()} isSelected={false} />);
    expect(() => {
      fireEvent.click(screen.getByText("Test Task"));
    }).not.toThrow();
  });

  it("shows assignee chip when assignee exists", () => {
    render(
      <TaskRow
        task={makeTask({ assignee: "agent-alpha" })}
        isSelected={false}
      />,
    );
    expect(screen.getByText("agent-alpha")).toBeInTheDocument();
  });

  it("does not show assignee chip when assignee is absent", () => {
    render(<TaskRow task={makeTask()} isSelected={false} />);
    expect(screen.queryByText("agent-alpha")).not.toBeInTheDocument();
  });

  it("shows diff stats when available", () => {
    mockUseIssueDiffStat.mockReturnValue({
      data: { branch: "feat-x", added: 15, removed: 3 },
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });

    render(
      <TaskRow task={makeTask({ assignee: "agent-1" })} isSelected={false} />,
    );
    expect(screen.getByText("+15")).toBeInTheDocument();
    expect(screen.getByText("-3")).toBeInTheDocument();
  });

  it("hides diff stats when data is null", () => {
    mockUseIssueDiffStat.mockReturnValue({
      data: null,
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });

    render(
      <TaskRow task={makeTask({ assignee: "agent-1" })} isSelected={false} />,
    );
    expect(screen.queryByText(/^\+\d+$/)).not.toBeInTheDocument();
    expect(screen.queryByText(/^-\d+$/)).not.toBeInTheDocument();
  });

  it("hides diff stats when added and removed are both zero", () => {
    mockUseIssueDiffStat.mockReturnValue({
      data: { branch: "feat-x", added: 0, removed: 0 },
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });

    render(
      <TaskRow task={makeTask({ assignee: "agent-1" })} isSelected={false} />,
    );
    expect(screen.queryByText("+0")).not.toBeInTheDocument();
    expect(screen.queryByText("-0")).not.toBeInTheDocument();
  });

  it("sets button title to task title", () => {
    render(
      <TaskRow
        task={makeTask({ title: "My Important Task" })}
        isSelected={false}
      />,
    );
    expect(screen.getByTitle("My Important Task")).toBeInTheDocument();
  });
});
