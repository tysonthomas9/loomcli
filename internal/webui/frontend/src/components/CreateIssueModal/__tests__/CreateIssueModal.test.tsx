/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for CreateIssueModal component.
 */

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { CreateIssueModal } from "../CreateIssueModal";

// ---------- Mocks ----------

vi.mock("@/api/issues", () => ({
  createIssue: vi.fn(),
}));

vi.mock("@/hooks", () => ({
  useRegisterEscapeLayer: vi.fn(),
  LAYER_MODAL: "modal",
}));

vi.mock("@/hooks/useFocusTrap", () => ({
  useFocusTrap: vi.fn(),
}));

vi.mock("@/hooks/useFocusReturn", () => ({
  useFocusReturn: vi.fn(),
}));

vi.mock("@/hooks/useWorkspaceContext", () => ({
  useWorkspaceContext: () => ({ workspaceId: "test-ws-id" }),
}));

import { createIssue } from "@/api/issues";
import type { Issue } from "@/types";

const mockCreateIssue = vi.mocked(createIssue);

const MOCK_ISSUE: Issue = {
  id: "TST-001",
  title: "Test issue",
  status: "open",
  priority: 2,
  issue_type: "task",
  labels: [],
  created_at: "2026-03-21T00:00:00Z",
  updated_at: "2026-03-21T00:00:00Z",
};

describe("CreateIssueModal", () => {
  let onClose: ReturnType<typeof vi.fn>;
  let onSuccess: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    onClose = vi.fn();
    onSuccess = vi.fn();
    mockCreateIssue.mockReset();
  });

  describe("rendering", () => {
    it("does not render when isOpen is false", () => {
      render(
        <CreateIssueModal
          isOpen={false}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      expect(
        screen.queryByTestId("create-issue-overlay"),
      ).not.toBeInTheDocument();
    });

    it("renders modal with all form fields when isOpen is true", () => {
      render(
        <CreateIssueModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      // Title heading
      expect(
        screen.getByRole("heading", { name: "New Issue" }),
      ).toBeInTheDocument();

      // Title input
      expect(screen.getByTestId("create-issue-title")).toBeInTheDocument();
      expect(screen.getByLabelText("Title")).toBeInTheDocument();

      // Type select
      expect(screen.getByTestId("create-issue-type")).toBeInTheDocument();
      expect(screen.getByLabelText("Type")).toBeInTheDocument();

      // Priority select
      expect(screen.getByTestId("create-issue-priority")).toBeInTheDocument();
      expect(screen.getByLabelText("Priority")).toBeInTheDocument();

      // Description textarea
      expect(
        screen.getByTestId("create-issue-description"),
      ).toBeInTheDocument();
      expect(screen.getByLabelText("Description")).toBeInTheDocument();

      // Buttons
      expect(screen.getByTestId("create-issue-cancel")).toBeInTheDocument();
      expect(screen.getByTestId("create-issue-submit")).toBeInTheDocument();

      // Dialog role
      const dialog = screen.getByRole("dialog");
      expect(dialog).toHaveAttribute("aria-modal", "true");
      expect(dialog).toHaveAttribute("aria-label", "Create Issue");
    });
  });

  describe("default values", () => {
    it("issue_type defaults to 'task' and priority defaults to 2", () => {
      render(
        <CreateIssueModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      expect(screen.getByTestId("create-issue-type")).toHaveValue("task");
      expect(screen.getByTestId("create-issue-priority")).toHaveValue("2");
    });
  });

  describe("submit button state", () => {
    it("submit button is disabled when title is empty", () => {
      render(
        <CreateIssueModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      expect(screen.getByTestId("create-issue-submit")).toBeDisabled();
    });

    it("submit button is enabled when title is non-empty", () => {
      render(
        <CreateIssueModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      fireEvent.change(screen.getByTestId("create-issue-title"), {
        target: { value: "My new issue" },
      });

      expect(screen.getByTestId("create-issue-submit")).toBeEnabled();
    });

    it("shows 'Creating...' text on submit button during submission", async () => {
      // Keep the promise pending so we can observe the submitting state
      let resolvePromise!: (value: Issue) => void;
      mockCreateIssue.mockImplementation(
        () =>
          new Promise<Issue>((resolve) => {
            resolvePromise = resolve;
          }),
      );

      render(
        <CreateIssueModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      fireEvent.change(screen.getByTestId("create-issue-title"), {
        target: { value: "My new issue" },
      });
      fireEvent.click(screen.getByTestId("create-issue-submit"));

      await waitFor(() => {
        expect(screen.getByTestId("create-issue-submit")).toHaveTextContent(
          "Creating...",
        );
      });

      expect(screen.getByTestId("create-issue-submit")).toBeDisabled();

      // Clean up
      resolvePromise(MOCK_ISSUE);
    });
  });

  describe("form submission", () => {
    it("calls createIssue with correct data on submit", async () => {
      mockCreateIssue.mockResolvedValue(MOCK_ISSUE);

      render(
        <CreateIssueModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      fireEvent.change(screen.getByTestId("create-issue-title"), {
        target: { value: "Fix login bug" },
      });
      fireEvent.change(screen.getByTestId("create-issue-type"), {
        target: { value: "bug" },
      });
      fireEvent.change(screen.getByTestId("create-issue-priority"), {
        target: { value: "1" },
      });
      fireEvent.change(screen.getByTestId("create-issue-description"), {
        target: { value: "Users cannot log in" },
      });
      fireEvent.click(screen.getByTestId("create-issue-submit"));

      await waitFor(() => {
        expect(mockCreateIssue).toHaveBeenCalledWith("test-ws-id", {
          title: "Fix login bug",
          issue_type: "bug",
          priority: 1,
          description: "Users cannot log in",
        });
      });
    });

    it("calls onSuccess and onClose after successful creation", async () => {
      mockCreateIssue.mockResolvedValue(MOCK_ISSUE);

      render(
        <CreateIssueModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      fireEvent.change(screen.getByTestId("create-issue-title"), {
        target: { value: "Test issue" },
      });
      fireEvent.click(screen.getByTestId("create-issue-submit"));

      await waitFor(() => {
        expect(onSuccess).toHaveBeenCalledWith(MOCK_ISSUE);
      });
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("displays error message when createIssue fails", async () => {
      mockCreateIssue.mockRejectedValue(new Error("Network error"));

      render(
        <CreateIssueModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      fireEvent.change(screen.getByTestId("create-issue-title"), {
        target: { value: "Fail issue" },
      });
      fireEvent.click(screen.getByTestId("create-issue-submit"));

      await waitFor(() => {
        expect(screen.getByTestId("create-issue-error")).toBeInTheDocument();
      });
      expect(screen.getByTestId("create-issue-error")).toHaveTextContent(
        "Network error",
      );
      expect(onSuccess).not.toHaveBeenCalled();
      expect(onClose).not.toHaveBeenCalled();
    });

    it("description is optional: submitting without description works", async () => {
      mockCreateIssue.mockResolvedValue(MOCK_ISSUE);

      render(
        <CreateIssueModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      fireEvent.change(screen.getByTestId("create-issue-title"), {
        target: { value: "No description issue" },
      });
      // Do not fill in description
      fireEvent.click(screen.getByTestId("create-issue-submit"));

      await waitFor(() => {
        expect(mockCreateIssue).toHaveBeenCalledWith("test-ws-id", {
          title: "No description issue",
          issue_type: "task",
          priority: 2,
        });
      });

      // description key should not be present in the request
      const callArgs = mockCreateIssue.mock.calls[0][1];
      expect(callArgs).not.toHaveProperty("description");
    });
  });

  describe("interactions", () => {
    it("calls onClose when Cancel button is clicked", () => {
      render(
        <CreateIssueModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      fireEvent.click(screen.getByTestId("create-issue-cancel"));
      expect(onClose).toHaveBeenCalledTimes(1);
    });
  });

  describe("form reset", () => {
    it("resets form when modal reopens", () => {
      const { rerender } = render(
        <CreateIssueModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      // Fill in form fields
      fireEvent.change(screen.getByTestId("create-issue-title"), {
        target: { value: "Dirty title" },
      });
      fireEvent.change(screen.getByTestId("create-issue-type"), {
        target: { value: "bug" },
      });
      fireEvent.change(screen.getByTestId("create-issue-priority"), {
        target: { value: "0" },
      });
      fireEvent.change(screen.getByTestId("create-issue-description"), {
        target: { value: "Some description" },
      });

      // Close the modal
      rerender(
        <CreateIssueModal
          isOpen={false}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      // Reopen the modal
      rerender(
        <CreateIssueModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      // All fields should be reset to defaults
      expect(screen.getByTestId("create-issue-title")).toHaveValue("");
      expect(screen.getByTestId("create-issue-type")).toHaveValue("task");
      expect(screen.getByTestId("create-issue-priority")).toHaveValue("2");
      expect(screen.getByTestId("create-issue-description")).toHaveValue("");
    });
  });
});
