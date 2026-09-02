/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for IssueDetailView component.
 * Focuses on Escape key handling and back button behavior.
 */

import {
  render,
  screen,
  fireEvent,
  act,
  waitFor,
} from "@testing-library/react";
import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { ApiError } from "@/types";
import type { Issue } from "@/types";
import { updateIssue } from "@/api";

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

vi.mock("@/api", () => ({
  updateIssue: vi.fn(),
}));

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useWorkspaceContext: () => ({ workspaceId: "test-ws-id" }),
  };
});

const mockUpdateIssue = vi.mocked(updateIssue);

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

  describe("HTML design rendering", () => {
    it("lets DesignPanel own presentation without description whitespace styles", () => {
      const issue = createTestIssue({
        design_format: "html",
        design:
          '<style>.card{display:grid}</style><div class="card">Plan</div>',
      });

      render(<IssueDetailView {...createDefaultProps({ issue })} />);

      const panel = screen.getByTestId("design-panel");
      expect(panel.className).not.toMatch(/description/);
      expect(screen.getAllByText("Design")).toHaveLength(1);
      expect(screen.getByTitle("HTML design artifact")).toBeInTheDocument();
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

  describe("StatusDropdown integration", () => {
    it("renders StatusDropdown with current issue status", () => {
      const issue = createTestIssue({ status: "in_progress" });
      render(<IssueDetailView {...createDefaultProps({ issue })} />);

      const dropdown = screen.getByTestId("status-dropdown");
      expect(dropdown).toBeInTheDocument();
      expect(dropdown).toHaveValue("in_progress");
    });

    it("defaults to open when issue has no status", () => {
      const issue = createTestIssue({ status: undefined });
      render(<IssueDetailView {...createDefaultProps({ issue })} />);

      const dropdown = screen.getByTestId("status-dropdown");
      expect(dropdown).toHaveValue("open");
    });

    it("calls updateIssue and onIssueUpdate on status change", async () => {
      const updatedIssue = createTestIssue({ status: "closed" });
      mockUpdateIssue.mockResolvedValue(updatedIssue);
      const onIssueUpdate = vi.fn();
      const issue = createTestIssue({ status: "open" });

      render(
        <IssueDetailView {...createDefaultProps({ issue, onIssueUpdate })} />,
      );

      const dropdown = screen.getByTestId("status-dropdown");
      await act(async () => {
        fireEvent.change(dropdown, { target: { value: "closed" } });
      });

      await waitFor(() => {
        expect(mockUpdateIssue).toHaveBeenCalledTimes(1);
        expect(mockUpdateIssue).toHaveBeenCalledWith(
          "test-ws-id",
          "test-issue-abc123",
          {
            status: "closed",
          },
        );
        expect(onIssueUpdate).toHaveBeenCalledTimes(1);
        expect(onIssueUpdate).toHaveBeenCalledWith(updatedIssue);
      });
    });

    it("shows saving state while API call is in flight", async () => {
      // Use a promise that we control to keep the API call in flight
      let resolveUpdate: (value: Issue) => void;
      const pendingPromise = new Promise<Issue>((resolve) => {
        resolveUpdate = resolve;
      });
      mockUpdateIssue.mockReturnValue(pendingPromise);

      const issue = createTestIssue({ status: "open" });
      render(<IssueDetailView {...createDefaultProps({ issue })} />);

      const dropdown = screen.getByTestId("status-dropdown");

      // Trigger the status change without awaiting resolution
      await act(async () => {
        fireEvent.change(dropdown, { target: { value: "in_progress" } });
      });

      // While the API call is in flight, the dropdown should be disabled (saving)
      expect(dropdown).toBeDisabled();
      expect(dropdown).toHaveAttribute("data-saving", "true");

      // Resolve the API call
      await act(async () => {
        resolveUpdate!(createTestIssue({ status: "in_progress" }));
      });

      // After resolution, the dropdown should be enabled again
      await waitFor(() => {
        expect(dropdown).not.toBeDisabled();
      });
    });

    it("handles API error gracefully and shows ErrorToast", async () => {
      mockUpdateIssue.mockRejectedValue(new Error("Network error"));
      const onIssueUpdate = vi.fn();
      const issue = createTestIssue({ status: "open" });

      render(
        <IssueDetailView {...createDefaultProps({ issue, onIssueUpdate })} />,
      );

      const dropdown = screen.getByTestId("status-dropdown");
      await act(async () => {
        fireEvent.change(dropdown, { target: { value: "blocked" } });
      });

      // ErrorToast should appear with the error message
      await waitFor(() => {
        expect(screen.getByTestId("status-error-toast")).toBeInTheDocument();
      });
      expect(screen.getByTestId("status-error-toast")).toHaveTextContent(
        "Network error",
      );

      // onIssueUpdate should NOT have been called on error
      expect(onIssueUpdate).not.toHaveBeenCalled();

      // Dropdown should be re-enabled after error
      expect(dropdown).not.toBeDisabled();
    });
  });

  // PUPPET-146. `onApprove` used to swallow its error, so none of this was
  // reachable: no message, and `isApproving` never cleared.
  describe("review action failures", () => {
    /** A review item: `blocked` + notes is the "help" review type. */
    function createReviewIssue(overrides: Partial<Issue> = {}): Issue {
      return createTestIssue({
        status: "blocked",
        notes: "I need help with this task",
        ...overrides,
      });
    }

    it("shows the server message verbatim when approve fails", async () => {
      const onApprove = vi
        .fn()
        .mockRejectedValue(
          new ApiError(409, "Conflict", { error: "issue is not claimable" }),
        );
      render(
        <IssueDetailView
          {...createDefaultProps({ issue: createReviewIssue(), onApprove })}
        />,
      );

      await act(async () => {
        fireEvent.click(screen.getByTestId("detail-approve-button"));
      });

      await waitFor(() => {
        expect(screen.getByTestId("action-error-toast")).toHaveTextContent(
          "issue is not claimable",
        );
      });
    });

    it("releases the approve spinner after a failure", async () => {
      const onApprove = vi.fn().mockRejectedValue(new Error("Network error"));
      render(
        <IssueDetailView
          {...createDefaultProps({ issue: createReviewIssue(), onApprove })}
        />,
      );

      const approve = screen.getByTestId("detail-approve-button");
      await act(async () => {
        fireEvent.click(approve);
      });

      await waitFor(() => {
        expect(screen.getByTestId("action-error-toast")).toBeInTheDocument();
      });
      expect(approve).not.toHaveTextContent("...");
    });

    it("disables only Approve, with the reason, after a 409", async () => {
      const onApprove = vi
        .fn()
        .mockRejectedValue(
          new ApiError(409, "Conflict", { error: "issue is not claimable" }),
        );
      render(
        <IssueDetailView
          {...createDefaultProps({ issue: createReviewIssue(), onApprove })}
        />,
      );

      await act(async () => {
        fireEvent.click(screen.getByTestId("detail-approve-button"));
      });

      await waitFor(() => {
        expect(
          screen.getByTestId("detail-approve-blocked-reason"),
        ).toHaveTextContent("issue is not claimable");
      });
      const approve = screen.getByTestId("detail-approve-button");
      expect(approve).toBeDisabled();
      expect(approve).toHaveAttribute("title", "issue is not claimable");
      // Reject is a different transition (PATCH status=open) that the claim
      // guard does not cover, so the 409 must not take it away.
      expect(screen.getByTestId("detail-reject-button")).not.toBeDisabled();
    });

    // A network error is not the server refusing: retrying is legitimate.
    it("leaves the buttons enabled after a non-409 failure", async () => {
      const onApprove = vi
        .fn()
        .mockRejectedValue(new ApiError(0, "Network error"));
      render(
        <IssueDetailView
          {...createDefaultProps({ issue: createReviewIssue(), onApprove })}
        />,
      );

      await act(async () => {
        fireEvent.click(screen.getByTestId("detail-approve-button"));
      });

      await waitFor(() => {
        expect(screen.getByTestId("action-error-toast")).toBeInTheDocument();
      });
      expect(screen.getByTestId("detail-approve-button")).not.toBeDisabled();
      expect(screen.getByTestId("detail-reject-button")).not.toBeDisabled();
      expect(
        screen.queryByTestId("detail-approve-blocked-reason"),
      ).not.toBeInTheDocument();
    });

    it("clears the error and the blocked latch when the issue changes", async () => {
      const onApprove = vi
        .fn()
        .mockRejectedValue(
          new ApiError(409, "Conflict", { error: "issue is not claimable" }),
        );
      const props = createDefaultProps({
        issue: createReviewIssue(),
        onApprove,
      });
      const { rerender } = render(<IssueDetailView {...props} />);

      await act(async () => {
        fireEvent.click(screen.getByTestId("detail-approve-button"));
      });
      await waitFor(() => {
        expect(
          screen.getByTestId("detail-approve-blocked-reason"),
        ).toBeInTheDocument();
      });

      rerender(
        <IssueDetailView
          {...props}
          issue={createReviewIssue({ id: "other-issue" })}
        />,
      );

      await waitFor(() => {
        expect(
          screen.queryByTestId("detail-approve-blocked-reason"),
        ).not.toBeInTheDocument();
      });
      expect(
        screen.queryByTestId("action-error-toast"),
      ).not.toBeInTheDocument();
      expect(screen.getByTestId("detail-approve-button")).not.toBeDisabled();
    });

    // An SSE update can make the same issue claimable again, so the latch has
    // to key on the record, not just on its identity.
    it("clears the blocked latch when the same issue is updated", async () => {
      const onApprove = vi
        .fn()
        .mockRejectedValue(
          new ApiError(409, "Conflict", { error: "issue is not claimable" }),
        );
      const props = createDefaultProps({
        issue: createReviewIssue({ updated_at: "2024-01-15T10:30:00Z" }),
        onApprove,
      });
      const { rerender } = render(<IssueDetailView {...props} />);

      await act(async () => {
        fireEvent.click(screen.getByTestId("detail-approve-button"));
      });
      await waitFor(() => {
        expect(screen.getByTestId("detail-approve-button")).toBeDisabled();
      });

      rerender(
        <IssueDetailView
          {...props}
          issue={createReviewIssue({ updated_at: "2024-01-15T10:35:00Z" })}
        />,
      );

      await waitFor(() => {
        expect(
          screen.queryByTestId("detail-approve-blocked-reason"),
        ).not.toBeInTheDocument();
      });
      expect(screen.getByTestId("detail-approve-button")).not.toBeDisabled();
    });

    it("shows the error and releases the spinner when reject fails", async () => {
      const onReject = vi
        .fn()
        .mockRejectedValue(new Error("Failed to add comment"));
      render(
        <IssueDetailView
          {...createDefaultProps({ issue: createReviewIssue(), onReject })}
        />,
      );

      fireEvent.click(screen.getByTestId("detail-reject-button"));
      fireEvent.change(screen.getByTestId("detail-reject-comment"), {
        target: { value: "not good enough" },
      });
      await act(async () => {
        fireEvent.click(screen.getByTestId("detail-reject-submit"));
      });

      await waitFor(() => {
        expect(screen.getByTestId("action-error-toast")).toHaveTextContent(
          "Failed to add comment",
        );
      });
    });
  });
});
