/**
 * @vitest-environment jsdom
 */

/**
 * Drag-overlay coverage for the tab marker slot.
 *
 * Lives in its own file because it mocks @dnd-kit/core: a real pointer drag is
 * not reproducible in jsdom, so DndContext is stubbed to start a drag on mount
 * and DragOverlay to render its children unconditionally.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { useEffect } from "react";

import "@testing-library/jest-dom";

vi.mock("@dnd-kit/core", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@dnd-kit/core")>();
  return {
    ...actual,
    DndContext: ({
      children,
      onDragStart,
    }: {
      children: React.ReactNode;
      onDragStart?: (e: { active: { id: string } }) => void;
    }) => {
      useEffect(() => {
        onDragStart?.({ active: { id: "tab-1" } });
      }, [onDragStart]);
      return <div data-testid="mock-dnd-context">{children}</div>;
    },
    DragOverlay: ({ children }: { children: React.ReactNode }) => (
      <div data-testid="mock-drag-overlay">{children}</div>
    ),
  };
});

const { TerminalTabBar } = await import("../TerminalTabBar");
type TerminalTab = import("../TerminalTabBar").TerminalTab;

class MockResizeObserver {
  observe = vi.fn();
  unobserve = vi.fn();
  disconnect = vi.fn();
}
globalThis.ResizeObserver =
  MockResizeObserver as unknown as typeof ResizeObserver;

const tabs: TerminalTab[] = [
  {
    id: "tab-1",
    label: "Terminal 1",
    connectionState: "connected",
    replacedAt: "2026-08-15T10:00:00Z",
  },
  { id: "tab-2", label: "Terminal 2", connectionState: "connected" },
];

const defaultProps = {
  tabs,
  activeTabId: "tab-1",
  onTabChange: vi.fn(),
  onTabClose: vi.fn(),
  onNewTab: vi.fn(),
};

describe("TerminalTabBar drag overlay markers", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders the restart marker in the drag overlay", () => {
    render(<TerminalTabBar {...defaultProps} />);

    const overlay = screen.getByTestId("mock-drag-overlay");
    expect(overlay).toHaveTextContent("Terminal 1");
    expect(
      screen.getByTestId("tab-marker-restart-tab-1-drag"),
    ).toBeInTheDocument();
    // …and the dragged tab in the strip still carries its own marker.
    expect(screen.getByTestId("tab-marker-restart-tab-1")).toBeInTheDocument();
  });

  it("omits the overlay marker for a tab that was never replaced", () => {
    render(<TerminalTabBar {...defaultProps} activeTabId="tab-2" />);

    expect(screen.queryByTestId("tab-marker-restart-tab-2-drag")).toBeNull();
  });
});
