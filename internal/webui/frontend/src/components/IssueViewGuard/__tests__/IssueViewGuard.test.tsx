/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for IssueViewGuard component.
 *
 * Child components (LoadingSkeleton, ErrorDisplay, EmptyWorkspaceBoard) are
 * mocked to avoid deep rendering and isolate guard logic.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { Issue } from "@/types";

// ---------------------------------------------------------------------------
// Mocks — must be declared before importing the component under test
// ---------------------------------------------------------------------------

vi.mock("@/components", () => ({
  LoadingSkeleton: Object.assign(
    () => <div data-testid="loading-skeleton-base" />,
    {
      Column: () => <div data-testid="loading-skeleton-column" />,
      Table: () => <div data-testid="loading-skeleton-table" />,
    },
  ),
  ErrorDisplay: ({
    onRetry,
    error,
    variant,
    isRetrying,
    description,
  }: {
    onRetry?: () => void;
    error?: Error | null;
    variant?: string;
    isRetrying?: boolean;
    description?: string;
  }) => (
    <div
      data-testid="error-display"
      data-variant={variant}
      data-is-retrying={String(!!isRetrying)}
    >
      {error && <span data-testid="error-message">{error.message}</span>}
      {description && (
        <span data-testid="error-description">{description}</span>
      )}
      {onRetry && (
        <button data-testid="retry-button" onClick={onRetry}>
          Try again
        </button>
      )}
    </div>
  ),
  EmptyWorkspaceBoard: ({ isMultiRepo }: { isMultiRepo?: boolean }) => (
    <div
      data-testid="empty-workspace-board"
      data-multi-repo={String(!!isMultiRepo)}
    />
  ),
}));

import { IssueViewGuard } from "../IssueViewGuard";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const STUB_ISSUE: Issue = {
  id: "ISS-1",
  title: "Test issue",
  status: "open",
} as Issue;

function defaultProps(
  overrides: Partial<Parameters<typeof IssueViewGuard>[0]> = {},
) {
  return {
    issues: [STUB_ISSUE] as Issue[],
    isLoading: false,
    error: null as string | null,
    isMultiRepo: false,
    onRetry: vi.fn(),
    loadingVariant: "columns" as const,
    children: <div data-testid="children-content">Board content</div>,
    ...overrides,
  };
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("IssueViewGuard", () => {
  // =========================================================================
  // Loading state
  // =========================================================================
  describe("loading state", () => {
    it("renders 3 LoadingSkeleton.Column for columns variant", () => {
      render(
        <IssueViewGuard
          {...defaultProps({ isLoading: true, loadingVariant: "columns" })}
        />,
      );

      const columns = screen.getAllByTestId("loading-skeleton-column");
      expect(columns).toHaveLength(3);
      expect(screen.getByTestId("loading-container")).toBeInTheDocument();
    });

    it("renders LoadingSkeleton.Table for table variant", () => {
      render(
        <IssueViewGuard
          {...defaultProps({ isLoading: true, loadingVariant: "table" })}
        />,
      );

      expect(screen.getByTestId("loading-skeleton-table")).toBeInTheDocument();
      expect(screen.queryAllByTestId("loading-skeleton-column")).toHaveLength(
        0,
      );
    });

    it("does not render children when loading", () => {
      render(<IssueViewGuard {...defaultProps({ isLoading: true })} />);

      expect(screen.queryByTestId("children-content")).not.toBeInTheDocument();
    });
  });

  // =========================================================================
  // Error state
  // =========================================================================
  describe("error state", () => {
    it("renders ErrorDisplay with retry button", () => {
      const onRetry = vi.fn();
      render(
        <IssueViewGuard
          {...defaultProps({ error: "Something went wrong", onRetry })}
        />,
      );

      expect(screen.getByTestId("error-display")).toBeInTheDocument();
      expect(screen.getByTestId("retry-button")).toBeInTheDocument();
    });

    it("passes error message to ErrorDisplay", () => {
      render(
        <IssueViewGuard {...defaultProps({ error: "Network failure" })} />,
      );

      expect(screen.getByTestId("error-message")).toHaveTextContent(
        "Network failure",
      );
    });

    it("calls onRetry when retry button is clicked", () => {
      const onRetry = vi.fn();
      render(<IssueViewGuard {...defaultProps({ error: "fail", onRetry })} />);

      fireEvent.click(screen.getByTestId("retry-button"));
      expect(onRetry).toHaveBeenCalledTimes(1);
    });

    it("does not render children when error", () => {
      render(<IssueViewGuard {...defaultProps({ error: "fail" })} />);

      expect(screen.queryByTestId("children-content")).not.toBeInTheDocument();
    });

    it("passes isRetrying=true to ErrorDisplay when retryCount > 0", () => {
      render(
        <IssueViewGuard
          {...defaultProps({
            error: "transient failure",
            retryCount: 2,
            nextRetryAt: Date.now() + 3_000,
          })}
        />,
      );

      expect(screen.getByTestId("error-display")).toHaveAttribute(
        "data-is-retrying",
        "true",
      );
      // Description should mention auto-retry
      expect(screen.getByTestId("error-description").textContent).toMatch(
        /[Rr]etrying automatically/,
      );
    });

    it("does not mark error as retrying when retryCount is 0", () => {
      render(
        <IssueViewGuard {...defaultProps({ error: "fail", retryCount: 0 })} />,
      );

      expect(screen.getByTestId("error-display")).toHaveAttribute(
        "data-is-retrying",
        "false",
      );
      expect(screen.queryByTestId("error-description")).not.toBeInTheDocument();
    });

    it("does not mark as retrying once retry budget is exhausted (nextRetryAt=null)", () => {
      // After MAX_AUTO_RETRIES the store keeps retryCount > 0 but clears
      // nextRetryAt. The UI must stop showing the auto-retry indicator and
      // fall back to the default fetch-error state with a manual retry.
      render(
        <IssueViewGuard
          {...defaultProps({
            error: "persistent failure",
            retryCount: 5,
            nextRetryAt: null,
          })}
        />,
      );

      expect(screen.getByTestId("error-display")).toHaveAttribute(
        "data-is-retrying",
        "false",
      );
      expect(screen.queryByTestId("error-description")).not.toBeInTheDocument();
      // Manual "Try again" button still works
      expect(screen.getByTestId("retry-button")).toBeInTheDocument();
    });

    it("does not mark as retrying when the error is the workspace-loading variant", () => {
      render(
        <IssueViewGuard
          {...defaultProps({
            error: "workspace is loading",
            retryCount: 3,
            nextRetryAt: Date.now() + 1_000,
          })}
        />,
      );

      // The 'loading' variant has its own auto-retry UX; we don't override it
      const errorDisplay = screen.getByTestId("error-display");
      expect(errorDisplay).toHaveAttribute("data-variant", "loading");
      expect(errorDisplay).toHaveAttribute("data-is-retrying", "false");
    });
  });

  // =========================================================================
  // Empty state
  // =========================================================================
  describe("empty state", () => {
    it("renders EmptyWorkspaceBoard when issues is empty", () => {
      render(<IssueViewGuard {...defaultProps({ issues: [] })} />);

      expect(screen.getByTestId("empty-workspace-board")).toBeInTheDocument();
    });

    it("passes isMultiRepo to EmptyWorkspaceBoard", () => {
      render(
        <IssueViewGuard {...defaultProps({ issues: [], isMultiRepo: true })} />,
      );

      expect(screen.getByTestId("empty-workspace-board")).toHaveAttribute(
        "data-multi-repo",
        "true",
      );
    });

    it("does not render children when empty", () => {
      render(<IssueViewGuard {...defaultProps({ issues: [] })} />);

      expect(screen.queryByTestId("children-content")).not.toBeInTheDocument();
    });
  });

  // =========================================================================
  // Empty state suppressed (showEmptyState=false)
  // =========================================================================
  describe("empty state suppressed", () => {
    it("renders children when showEmptyState=false and issues is empty", () => {
      render(
        <IssueViewGuard
          {...defaultProps({ issues: [], showEmptyState: false })}
        />,
      );

      expect(screen.getByTestId("children-content")).toBeInTheDocument();
      expect(
        screen.queryByTestId("empty-workspace-board"),
      ).not.toBeInTheDocument();
    });
  });

  // =========================================================================
  // Normal state
  // =========================================================================
  describe("normal state", () => {
    it("renders children when issues exist, not loading, no error", () => {
      render(<IssueViewGuard {...defaultProps()} />);

      expect(screen.getByTestId("children-content")).toBeInTheDocument();
      expect(screen.getByText("Board content")).toBeInTheDocument();
    });

    it("does not render loading skeleton", () => {
      render(<IssueViewGuard {...defaultProps()} />);

      expect(screen.queryByTestId("loading-container")).not.toBeInTheDocument();
    });

    it("does not render error display", () => {
      render(<IssueViewGuard {...defaultProps()} />);

      expect(screen.queryByTestId("error-display")).not.toBeInTheDocument();
    });

    it("does not render empty board", () => {
      render(<IssueViewGuard {...defaultProps()} />);

      expect(
        screen.queryByTestId("empty-workspace-board"),
      ).not.toBeInTheDocument();
    });
  });

  // =========================================================================
  // Priority: loading overrides error; error overrides empty
  // =========================================================================
  describe("priority", () => {
    it("loading overrides error", () => {
      render(
        <IssueViewGuard
          {...defaultProps({ isLoading: true, error: "should not show" })}
        />,
      );

      expect(screen.getByTestId("loading-container")).toBeInTheDocument();
      expect(screen.queryByTestId("error-display")).not.toBeInTheDocument();
      expect(screen.queryByTestId("children-content")).not.toBeInTheDocument();
    });

    it("loading overrides empty", () => {
      render(
        <IssueViewGuard {...defaultProps({ isLoading: true, issues: [] })} />,
      );

      expect(screen.getByTestId("loading-container")).toBeInTheDocument();
      expect(
        screen.queryByTestId("empty-workspace-board"),
      ).not.toBeInTheDocument();
    });

    it("error overrides empty", () => {
      render(
        <IssueViewGuard
          {...defaultProps({ error: "error wins", issues: [] })}
        />,
      );

      expect(screen.getByTestId("error-display")).toBeInTheDocument();
      expect(
        screen.queryByTestId("empty-workspace-board"),
      ).not.toBeInTheDocument();
      expect(screen.queryByTestId("children-content")).not.toBeInTheDocument();
    });

    it("loading overrides both error and empty", () => {
      render(
        <IssueViewGuard
          {...defaultProps({ isLoading: true, error: "err", issues: [] })}
        />,
      );

      expect(screen.getByTestId("loading-container")).toBeInTheDocument();
      expect(screen.queryByTestId("error-display")).not.toBeInTheDocument();
      expect(
        screen.queryByTestId("empty-workspace-board"),
      ).not.toBeInTheDocument();
    });
  });
});
