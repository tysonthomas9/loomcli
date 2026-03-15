/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for IssueDetailView component.
 * Focuses on Escape key handling and back button behavior.
 */

import { render, screen, fireEvent, act } from "@testing-library/react";
import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { Issue } from "@/types";

import { IssueDetailView } from "../IssueDetailView";
import type { IssueDetailViewProps } from "../IssueDetailView";

// Hoist mock for useRegisterEscapeLayer so we can inspect and invoke the handler
const { mockUseRegisterEscapeLayer } = vi.hoisted(() => ({
  mockUseRegisterEscapeLayer: vi.fn(),
}));

vi.mock("@/hooks", async (importOriginal) => {
  const orig = await importOriginal<typeof import("@/hooks")>();
  return {
    ...orig,
    useRegisterEscapeLayer: mockUseRegisterEscapeLayer,
  };
});

/**
 * Create a minimal test issue with required fields.
 */
function createTestIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "test-issue-abc123",
    title: "Test Issue Title",
    priority: 2,
    created_at: "2024-01-15T10:30:00Z",
    updated_at: "2024-01-15T10:30:00Z",
    ...overrides,
  };
}

/**
 * Create default props for IssueDetailView.
 */
function createDefaultProps(
  overrides: Partial<IssueDetailViewProps> = {},
): IssueDetailViewProps {
  return {
    issue: createTestIssue(),
    isLoading: false,
    error: null,
    previousView: "kanban",
    onBack: vi.fn(),
    onApprove: vi.fn(),
    onReject: vi.fn(),
    ...overrides,
  };
}

/**
 * Helper: get the most recent escape layer handler registered via the mock.
 * The component calls useRegisterEscapeLayer(priority, handler, active).
 */
function getEscapeHandler(): (() => void) | null {
  const calls = mockUseRegisterEscapeLayer.mock.calls;
  // Find the last call where active=true
  for (let i = calls.length - 1; i >= 0; i--) {
    if (calls[i][2] === true) {
      return calls[i][1] as () => void;
    }
  }
  return null;
}

describe("IssueDetailView", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("back button", () => {
    it("calls onBack when back button is clicked", () => {
      const onBack = vi.fn();
      render(<IssueDetailView {...createDefaultProps({ onBack })} />);

      fireEvent.click(screen.getByTestId("detail-back-button"));

      expect(onBack).toHaveBeenCalledTimes(1);
    });
  });

  describe("Escape key handling", () => {
    it("registers escape layer and handler calls onBack", () => {
      const onBack = vi.fn();
      render(<IssueDetailView {...createDefaultProps({ onBack })} />);

      // Verify useRegisterEscapeLayer was called with active=true
      const handler = getEscapeHandler();
      expect(handler).not.toBeNull();

      // Invoke the handler to simulate Escape key via the layer system
      handler!();

      expect(onBack).toHaveBeenCalledTimes(1);
    });

    it("closes reject form instead of calling onBack when reject form is open", () => {
      const onBack = vi.fn();
      // Make the issue a review item so the reject button appears
      const issue = createTestIssue({ status: "review" });
      render(<IssueDetailView {...createDefaultProps({ onBack, issue })} />);

      // Open the reject form by clicking the Reject button
      fireEvent.click(screen.getByTestId("detail-reject-button"));

      // Verify reject form is visible
      expect(screen.getByTestId("detail-reject-comment")).toBeInTheDocument();

      // Get the latest handler (re-rendered with showRejectForm=true)
      const handler = getEscapeHandler();
      expect(handler).not.toBeNull();
      act(() => {
        handler!();
      });

      expect(onBack).not.toHaveBeenCalled();
      // Reject form should be closed now
      expect(
        screen.queryByTestId("detail-reject-comment"),
      ).not.toBeInTheDocument();
    });

    it("calls onBack after reject form is closed by Escape and Escape is pressed again", () => {
      const onBack = vi.fn();
      const issue = createTestIssue({ status: "review" });
      render(<IssueDetailView {...createDefaultProps({ onBack, issue })} />);

      // Open the reject form
      fireEvent.click(screen.getByTestId("detail-reject-button"));
      expect(screen.getByTestId("detail-reject-comment")).toBeInTheDocument();

      // First Escape closes the form
      let handler = getEscapeHandler();
      expect(handler).not.toBeNull();
      act(() => {
        handler!();
      });
      expect(onBack).not.toHaveBeenCalled();
      expect(
        screen.queryByTestId("detail-reject-comment"),
      ).not.toBeInTheDocument();

      // Second Escape navigates back
      handler = getEscapeHandler();
      expect(handler).not.toBeNull();
      act(() => {
        handler!();
      });
      expect(onBack).toHaveBeenCalledTimes(1);
    });

    it("registers escape layer with LAYER_ISSUE_PANEL priority", () => {
      render(<IssueDetailView {...createDefaultProps()} />);

      // The component calls useRegisterEscapeLayer(LAYER_ISSUE_PANEL, handler, true)
      // LAYER_ISSUE_PANEL = 10
      const call = mockUseRegisterEscapeLayer.mock.calls.find(
        (c: unknown[]) => c[2] === true,
      );
      expect(call).toBeDefined();
      expect(call![0]).toBe(10); // LAYER_ISSUE_PANEL
    });
  });
});
