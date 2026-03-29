/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for WorkQueueSection workspace-switch re-read bug fix.
 *
 * The bug: when navigating between workspaces in the SPA, components that
 * read UI preferences from scoped localStorage (keyed by workspaceId) would
 * keep showing stale state from the previous workspace because the initial
 * useState(() => wsGet(...)) initializer only runs on mount, not on
 * workspaceId changes.
 *
 * The fix: a useEffect that re-reads the correct workspace's preferences
 * from localStorage whenever workspaceId changes.
 *
 * This test verifies the fix by:
 * 1. Rendering with workspace A (collapsed) and confirming A's state loads
 * 2. Switching to workspace B (expanded) and confirming B's state loads
 * 3. Verifying workspace A's stored value was not overwritten by the switch
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { WorkQueueCounts } from "../WorkQueueSection";
import { WorkQueueSection } from "../WorkQueueSection";

// --- Dynamic workspace ID mock ---
// We need to change the returned workspaceId between renders to simulate
// SPA navigation between workspaces.
let mockWorkspaceId = "ws-aaa-1111";

vi.mock("@/hooks/useWorkspaceContext", () => ({
  useWorkspaceContext: () => ({ workspaceId: mockWorkspaceId }),
}));

// --- Helpers ---

const WS_A = "ws-aaa-1111";
const WS_B = "ws-bbb-2222";

/** Scoped localStorage key for the work-queue-expanded preference. */
function storageKey(wsId: string): string {
  return `loom:${wsId}:work-queue-expanded`;
}

function makeCounts(overrides: Partial<WorkQueueCounts> = {}): WorkQueueCounts {
  return {
    backlog: 0,
    open: 0,
    blocked: 0,
    inProgress: 0,
    needsReview: 0,
    done: 0,
    ...overrides,
  };
}

describe("WorkQueueSection workspace-switch re-read", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    // Reset to workspace A
    mockWorkspaceId = WS_A;
  });

  it("loads correct scoped state for workspace A on initial render", () => {
    // Workspace A is collapsed, workspace B is expanded
    localStorage.setItem(storageKey(WS_A), "false");
    localStorage.setItem(storageKey(WS_B), "true");

    mockWorkspaceId = WS_A;
    render(<WorkQueueSection counts={makeCounts({ backlog: 3 })} />);

    // Collapsed: queue content should NOT be visible
    expect(screen.queryByText("Backlog")).not.toBeInTheDocument();
    expect(screen.getByText("Work Queue")).toBeInTheDocument();
  });

  it("re-reads scoped state when workspaceId changes to workspace B", () => {
    // Workspace A is collapsed, workspace B is expanded
    localStorage.setItem(storageKey(WS_A), "false");
    localStorage.setItem(storageKey(WS_B), "true");

    // Render with workspace A (collapsed)
    mockWorkspaceId = WS_A;
    const { rerender } = render(
      <WorkQueueSection counts={makeCounts({ backlog: 3, open: 1 })} />,
    );

    // Verify collapsed (A's preference)
    expect(screen.queryByText("Backlog")).not.toBeInTheDocument();

    // Switch to workspace B
    mockWorkspaceId = WS_B;
    rerender(<WorkQueueSection counts={makeCounts({ backlog: 7, open: 2 })} />);

    // Should now be expanded (B's preference) — queue content is visible
    expect(screen.getByText("Backlog")).toBeInTheDocument();
    expect(screen.getByText("Open")).toBeInTheDocument();
    expect(screen.getByText("7")).toBeInTheDocument();
    expect(screen.getByText("2")).toBeInTheDocument();
  });

  it("does not overwrite workspace A's stored value when switching to B", () => {
    // Workspace A is collapsed, workspace B is expanded
    localStorage.setItem(storageKey(WS_A), "false");
    localStorage.setItem(storageKey(WS_B), "true");

    // Render with workspace A
    mockWorkspaceId = WS_A;
    const { rerender } = render(
      <WorkQueueSection counts={makeCounts({ backlog: 1 })} />,
    );

    // Verify A's state loaded (collapsed)
    expect(screen.queryByText("Backlog")).not.toBeInTheDocument();

    // Switch to workspace B
    mockWorkspaceId = WS_B;
    rerender(<WorkQueueSection counts={makeCounts({ backlog: 2 })} />);

    // B is expanded
    expect(screen.getByText("Backlog")).toBeInTheDocument();

    // Workspace A's stored value must still be "false" (not overwritten)
    expect(localStorage.getItem(storageKey(WS_A))).toBe("false");
    // Workspace B's stored value must still be "true"
    expect(localStorage.getItem(storageKey(WS_B))).toBe("true");
  });

  it("switches from expanded workspace to collapsed workspace", () => {
    // Opposite direction: A is expanded, B is collapsed
    localStorage.setItem(storageKey(WS_A), "true");
    localStorage.setItem(storageKey(WS_B), "false");

    // Render with workspace A (expanded)
    mockWorkspaceId = WS_A;
    const { rerender } = render(
      <WorkQueueSection counts={makeCounts({ inProgress: 5 })} />,
    );

    // Expanded — content visible
    expect(screen.getByText("In Progress")).toBeInTheDocument();
    expect(screen.getByText("5")).toBeInTheDocument();

    // Switch to workspace B (collapsed)
    mockWorkspaceId = WS_B;
    rerender(<WorkQueueSection counts={makeCounts({ inProgress: 10 })} />);

    // Collapsed — content NOT visible
    expect(screen.queryByText("In Progress")).not.toBeInTheDocument();

    // Neither workspace's stored value was corrupted
    expect(localStorage.getItem(storageKey(WS_A))).toBe("true");
    expect(localStorage.getItem(storageKey(WS_B))).toBe("false");
  });

  it("defaults to expanded when switching to a workspace with no stored value", () => {
    // Only workspace A has a stored value (collapsed)
    localStorage.setItem(storageKey(WS_A), "false");
    // Workspace B has NO stored value

    // Render with workspace A (collapsed)
    mockWorkspaceId = WS_A;
    const { rerender } = render(
      <WorkQueueSection counts={makeCounts({ done: 4 })} />,
    );
    expect(screen.queryByText("Done")).not.toBeInTheDocument();

    // Switch to workspace B (no stored value => defaults to expanded)
    mockWorkspaceId = WS_B;
    rerender(<WorkQueueSection counts={makeCounts({ done: 8 })} />);

    expect(screen.getByText("Done")).toBeInTheDocument();
    expect(screen.getByText("8")).toBeInTheDocument();
  });

  it("preserves both workspaces after round-trip switch A -> B -> A", () => {
    localStorage.setItem(storageKey(WS_A), "false");
    localStorage.setItem(storageKey(WS_B), "true");

    // Start on workspace A (collapsed)
    mockWorkspaceId = WS_A;
    const { rerender } = render(
      <WorkQueueSection counts={makeCounts({ open: 1 })} />,
    );
    expect(screen.queryByText("Open")).not.toBeInTheDocument();

    // Switch to workspace B (expanded)
    mockWorkspaceId = WS_B;
    rerender(<WorkQueueSection counts={makeCounts({ open: 2 })} />);
    expect(screen.getByText("Open")).toBeInTheDocument();

    // Switch back to workspace A (should be collapsed again)
    mockWorkspaceId = WS_A;
    rerender(<WorkQueueSection counts={makeCounts({ open: 3 })} />);
    expect(screen.queryByText("Open")).not.toBeInTheDocument();

    // Verify storage integrity
    expect(localStorage.getItem(storageKey(WS_A))).toBe("false");
    expect(localStorage.getItem(storageKey(WS_B))).toBe("true");
  });
});
