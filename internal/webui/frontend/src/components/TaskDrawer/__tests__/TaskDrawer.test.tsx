/**
 * @vitest-environment jsdom
 */
import "@testing-library/jest-dom";
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";

import type { LoomTaskInfo } from "@/types";

import { TaskDrawer } from "../TaskDrawer";

// ---- Mocks ----

vi.mock("@/hooks", () => ({
  useFocusReturn: vi.fn(),
  useFocusTrap: vi.fn(),
  useRegisterEscapeLayer: vi.fn(),
  LAYER_TERMINAL_PANEL: 30,
}));

// ---- Helpers ----

function makeTask(overrides: Partial<LoomTaskInfo> = {}): LoomTaskInfo {
  return {
    id: "t-001",
    title: "Implement feature",
    priority: 2,
    status: "in_progress",
    ...overrides,
  };
}

// ---- Tests ----

describe("TaskDrawer", () => {
  const defaultProps = {
    isOpen: true,
    category: "impl" as const,
    title: "Implementation",
    tasks: [makeTask()],
    onClose: vi.fn(),
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    // Clean up body overflow style
    document.body.style.overflow = "";
  });

  it("renders null when isOpen is false", () => {
    const { container } = render(
      <TaskDrawer {...defaultProps} isOpen={false} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it("renders null when category is null", () => {
    const { container } = render(
      <TaskDrawer {...defaultProps} category={null} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it("renders drawer dialog when open with valid category", () => {
    render(<TaskDrawer {...defaultProps} />);
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("displays title in header", () => {
    render(<TaskDrawer {...defaultProps} title="Review Tasks" />);
    expect(screen.getByText("Review Tasks")).toBeInTheDocument();
  });

  it("displays task count in header", () => {
    render(
      <TaskDrawer
        {...defaultProps}
        tasks={[makeTask(), makeTask({ id: "t-002", title: "Second task" })]}
      />,
    );
    expect(screen.getByText("(2)")).toBeInTheDocument();
  });

  it("renders task items with id, title, and priority", () => {
    render(
      <TaskDrawer
        {...defaultProps}
        tasks={[makeTask({ id: "abc-123", title: "Fix bug", priority: 1 })]}
      />,
    );
    expect(screen.getByText("abc-123")).toBeInTheDocument();
    expect(screen.getByText("Fix bug")).toBeInTheDocument();
    expect(screen.getByText("P1")).toBeInTheDocument();
  });

  it("renders multiple tasks", () => {
    const tasks = [
      makeTask({ id: "t-001", title: "First" }),
      makeTask({ id: "t-002", title: "Second" }),
      makeTask({ id: "t-003", title: "Third" }),
    ];
    render(<TaskDrawer {...defaultProps} tasks={tasks} />);
    expect(screen.getByText("First")).toBeInTheDocument();
    expect(screen.getByText("Second")).toBeInTheDocument();
    expect(screen.getByText("Third")).toBeInTheDocument();
    expect(screen.getByText("(3)")).toBeInTheDocument();
  });

  it("shows empty state when tasks array is empty", () => {
    render(<TaskDrawer {...defaultProps} tasks={[]} />);
    expect(screen.getByText("No tasks in this category")).toBeInTheDocument();
  });

  it("calls onClose when close button is clicked", () => {
    const onClose = vi.fn();
    render(<TaskDrawer {...defaultProps} onClose={onClose} />);
    fireEvent.click(screen.getByLabelText("Close drawer"));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("calls onClose when overlay is clicked", () => {
    const onClose = vi.fn();
    const { container } = render(
      <TaskDrawer {...defaultProps} onClose={onClose} />,
    );
    // The overlay div has aria-hidden="true"
    const overlay = container.querySelector('[aria-hidden="true"]');
    expect(overlay).not.toBeNull();
    fireEvent.click(overlay!);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("sets body overflow to hidden when open", () => {
    render(<TaskDrawer {...defaultProps} />);
    expect(document.body.style.overflow).toBe("hidden");
  });

  it("restores body overflow when closed", () => {
    const { rerender } = render(<TaskDrawer {...defaultProps} />);
    expect(document.body.style.overflow).toBe("hidden");

    rerender(<TaskDrawer {...defaultProps} isOpen={false} />);
    expect(document.body.style.overflow).toBe("");
  });

  it("renders dialog with correct aria attributes", () => {
    render(<TaskDrawer {...defaultProps} />);
    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveAttribute("aria-modal", "true");
    expect(dialog).toHaveAttribute("aria-labelledby", "drawer-title");
  });

  it("renders priority badges with different colors", () => {
    const tasks = [
      makeTask({ id: "t-0", priority: 0 }),
      makeTask({ id: "t-1", priority: 1 }),
      makeTask({ id: "t-3", priority: 3 }),
      makeTask({ id: "t-4", priority: 4 }),
    ];
    render(<TaskDrawer {...defaultProps} tasks={tasks} />);
    expect(screen.getByText("P0")).toBeInTheDocument();
    expect(screen.getByText("P1")).toBeInTheDocument();
    expect(screen.getByText("P3")).toBeInTheDocument();
    expect(screen.getByText("P4")).toBeInTheDocument();
  });

  it("cleans up body overflow on unmount", () => {
    document.body.style.overflow = "";
    const { unmount } = render(<TaskDrawer {...defaultProps} />);
    expect(document.body.style.overflow).toBe("hidden");
    unmount();
    expect(document.body.style.overflow).toBe("");
  });
});
