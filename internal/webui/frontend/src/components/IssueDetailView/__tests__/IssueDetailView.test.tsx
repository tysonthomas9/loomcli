/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for IssueDetailView component.
 * Focuses on Escape key handling and back button behavior.
 */

import { render, screen, fireEvent, act } from "@testing-library/react";
import { describe, it, expect, vi, afterEach } from "vitest";
import "@testing-library/jest-dom";

import type { Issue } from "@/types";

import { IssueDetailView } from "../IssueDetailView";
import type { IssueDetailViewProps } from "../IssueDetailView";

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

describe("IssueDetailView", () => {
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
    it("calls onBack when Escape is pressed", () => {
      const onBack = vi.fn();
      render(<IssueDetailView {...createDefaultProps({ onBack })} />);

      fireEvent.keyDown(document, { key: "Escape" });

      expect(onBack).toHaveBeenCalledTimes(1);
    });

    it("does NOT call onBack when input is focused", () => {
      const onBack = vi.fn();
      const { container } = render(
        <IssueDetailView {...createDefaultProps({ onBack })} />,
      );

      // Add an input element to the rendered container to simulate an inner input
      const input = document.createElement("input");
      container.appendChild(input);
      input.focus();

      fireEvent.keyDown(input, { key: "Escape" });

      expect(onBack).not.toHaveBeenCalled();
    });

    it("does NOT call onBack when textarea is focused", () => {
      const onBack = vi.fn();
      const { container } = render(
        <IssueDetailView {...createDefaultProps({ onBack })} />,
      );

      // Add a textarea element to simulate an inner textarea
      const textarea = document.createElement("textarea");
      container.appendChild(textarea);
      textarea.focus();

      fireEvent.keyDown(textarea, { key: "Escape" });

      expect(onBack).not.toHaveBeenCalled();
    });

    it("does NOT call onBack when contentEditable element is focused", () => {
      const onBack = vi.fn();
      const { container } = render(
        <IssueDetailView {...createDefaultProps({ onBack })} />,
      );

      // Add a contentEditable div to simulate an inline editor
      const editable = document.createElement("div");
      editable.contentEditable = "true";
      container.appendChild(editable);
      editable.focus();

      // Dispatch a native KeyboardEvent with target set to the contentEditable element
      const event = new KeyboardEvent("keydown", {
        key: "Escape",
        bubbles: true,
      });
      // jsdom may not set isContentEditable from contentEditable attribute,
      // so ensure it's defined on the element
      Object.defineProperty(editable, "isContentEditable", {
        value: true,
        configurable: true,
      });
      editable.dispatchEvent(event);

      expect(onBack).not.toHaveBeenCalled();
    });

    it('does NOT call onBack when a [role="dialog"] is in the DOM', () => {
      const onBack = vi.fn();
      render(<IssueDetailView {...createDefaultProps({ onBack })} />);

      // Add a dialog overlay to the document body
      const dialog = document.createElement("div");
      dialog.setAttribute("role", "dialog");
      document.body.appendChild(dialog);

      try {
        fireEvent.keyDown(document, { key: "Escape" });

        expect(onBack).not.toHaveBeenCalled();
      } finally {
        document.body.removeChild(dialog);
      }
    });

    it('does NOT call onBack when a [role="listbox"] is in the DOM', () => {
      const onBack = vi.fn();
      render(<IssueDetailView {...createDefaultProps({ onBack })} />);

      // Add a listbox overlay to the document body (e.g., a dropdown)
      const listbox = document.createElement("div");
      listbox.setAttribute("role", "listbox");
      document.body.appendChild(listbox);

      try {
        fireEvent.keyDown(document, { key: "Escape" });

        expect(onBack).not.toHaveBeenCalled();
      } finally {
        document.body.removeChild(listbox);
      }
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

      // Press Escape — should close the form, not call onBack
      fireEvent.keyDown(document, { key: "Escape" });

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
      fireEvent.keyDown(document, { key: "Escape" });
      expect(onBack).not.toHaveBeenCalled();
      expect(
        screen.queryByTestId("detail-reject-comment"),
      ).not.toBeInTheDocument();

      // Second Escape navigates back
      fireEvent.keyDown(document, { key: "Escape" });
      expect(onBack).toHaveBeenCalledTimes(1);
    });

    it("does not call onBack for non-Escape keys", () => {
      const onBack = vi.fn();
      render(<IssueDetailView {...createDefaultProps({ onBack })} />);

      fireEvent.keyDown(document, { key: "Enter" });
      fireEvent.keyDown(document, { key: "a" });
      fireEvent.keyDown(document, { key: "Tab" });

      expect(onBack).not.toHaveBeenCalled();
    });

    it("cleans up keydown listener on unmount", () => {
      const removeEventListenerSpy = vi.spyOn(document, "removeEventListener");
      const onBack = vi.fn();

      const { unmount } = render(
        <IssueDetailView {...createDefaultProps({ onBack })} />,
      );

      unmount();

      expect(removeEventListenerSpy).toHaveBeenCalledWith(
        "keydown",
        expect.any(Function),
      );
    });
  });
});
