/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for CreateIssueModal component.
 */

import {
  render,
  screen,
  fireEvent,
  waitFor,
  act,
} from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { CreateIssueModal } from "../CreateIssueModal";

// ---------- Mocks ----------

vi.mock("@/hooks/api", () => ({
  createIssue: vi.fn(),
}));

vi.mock("@/hooks/ui", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/ui")>("@/hooks/ui");
  return {
    ...actual,
    useRegisterEscapeLayer: vi.fn(),
    LAYER_MODAL: "modal",
    useFocusTrap: vi.fn(),
    useFocusReturn: vi.fn(),
  };
});

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useWorkspaceContext: vi.fn(() => ({
      workspaceId: "test-ws-id",
      repos: [],
    })),
  };
});

import { createIssue } from "@/hooks/api";
import { useWorkspaceContext } from "@/hooks/workspace";
import type { Issue } from "@/types";

const mockCreateIssue = vi.mocked(createIssue);
const mockUseWorkspaceContext = vi.mocked(useWorkspaceContext);

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

const MOCK_CREATE_RESULT = {
  issue: MOCK_ISSUE,
  softDuplicate: false,
};

describe("CreateIssueModal", () => {
  let onClose: ReturnType<typeof vi.fn>;
  let onSuccess: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    onClose = vi.fn();
    onSuccess = vi.fn();
    mockCreateIssue.mockReset();
    mockUseWorkspaceContext.mockReturnValue({
      workspaceId: "test-ws-id",
      repos: [],
    } as ReturnType<typeof useWorkspaceContext>);
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
    it("issue_type defaults to 'task'", () => {
      render(
        <CreateIssueModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      expect(screen.getByTestId("create-issue-type")).toHaveValue("task");
    });

    it("prefills provided issue values", () => {
      render(
        <CreateIssueModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
          initialValues={{
            title: "Explore Hello-World onboarding",
            description: "Inspect the sample repo and write a plan.",
            issueType: "task",
            priority: 2,
          }}
        />,
      );

      expect(screen.getByTestId("create-issue-title")).toHaveValue(
        "Explore Hello-World onboarding",
      );
      expect(screen.getByTestId("create-issue-description")).toHaveValue(
        "Inspect the sample repo and write a plan.",
      );
      expect(screen.getByTestId("create-issue-submit")).toBeEnabled();
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
      let resolvePromise!: (value: typeof MOCK_CREATE_RESULT) => void;
      mockCreateIssue.mockImplementation(
        () =>
          new Promise<typeof MOCK_CREATE_RESULT>((resolve) => {
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
      await act(async () => {
        fireEvent.click(screen.getByTestId("create-issue-submit"));
      });

      expect(screen.getByTestId("create-issue-submit")).toHaveTextContent(
        "Creating...",
      );

      expect(screen.getByTestId("create-issue-submit")).toBeDisabled();

      // Clean up
      await act(async () => {
        resolvePromise(MOCK_CREATE_RESULT);
      });
    });
  });

  describe("form submission", () => {
    it("calls createIssue with correct data on submit", async () => {
      mockCreateIssue.mockResolvedValue(MOCK_CREATE_RESULT);

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
      fireEvent.change(screen.getByTestId("create-issue-description"), {
        target: { value: "Users cannot log in" },
      });
      fireEvent.click(screen.getByTestId("create-issue-submit"));

      await waitFor(() => {
        expect(mockCreateIssue).toHaveBeenCalledWith(
          "test-ws-id",
          {
            title: "Fix login bug",
            issue_type: "bug",
            priority: 2,
            description: "Users cannot log in",
          },
          { idempotencyKey: expect.stringMatching(/^loom-ui-/) },
        );
      });
    });

    it("calls onSuccess and onClose after successful creation", async () => {
      mockCreateIssue.mockResolvedValue(MOCK_CREATE_RESULT);

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

    it("waits for async onSuccess before closing", async () => {
      mockCreateIssue.mockResolvedValue(MOCK_CREATE_RESULT);
      let resolveSuccess!: () => void;
      onSuccess.mockImplementation(
        () =>
          new Promise<void>((resolve) => {
            resolveSuccess = resolve;
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
        target: { value: "Test issue" },
      });
      fireEvent.click(screen.getByTestId("create-issue-submit"));

      await waitFor(() => {
        expect(onSuccess).toHaveBeenCalledWith(MOCK_ISSUE);
      });
      expect(onClose).not.toHaveBeenCalled();

      await act(async () => {
        resolveSuccess();
      });

      await waitFor(() => {
        expect(onClose).toHaveBeenCalledTimes(1);
      });
    });

    it("surfaces async onSuccess failures without closing", async () => {
      mockCreateIssue.mockResolvedValue(MOCK_CREATE_RESULT);
      onSuccess.mockRejectedValue(new Error("Refresh failed"));

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
        expect(screen.getByTestId("create-issue-error")).toHaveTextContent(
          "Refresh failed",
        );
      });
      expect(onClose).not.toHaveBeenCalled();
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

    it("reuses the submit idempotency key after an ambiguous failure", async () => {
      mockCreateIssue
        .mockRejectedValueOnce(new Error("response lost"))
        .mockResolvedValueOnce(MOCK_CREATE_RESULT);

      render(
        <CreateIssueModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      fireEvent.change(screen.getByTestId("create-issue-title"), {
        target: { value: "Retry-safe issue" },
      });
      fireEvent.click(screen.getByTestId("create-issue-submit"));
      await screen.findByText("response lost");

      fireEvent.click(screen.getByTestId("create-issue-submit"));
      await waitFor(() => expect(onSuccess).toHaveBeenCalledWith(MOCK_ISSUE));

      const firstKey = mockCreateIssue.mock.calls[0]?.[2]?.idempotencyKey;
      const secondKey = mockCreateIssue.mock.calls[1]?.[2]?.idempotencyKey;
      expect(firstKey).toMatch(/^loom-ui-/);
      expect(secondKey).toBe(firstKey);
    });

    it("surfaces soft duplicate creates and retries with force when requested", async () => {
      const forcedIssue = { ...MOCK_ISSUE, id: "TST-002" };
      mockCreateIssue
        .mockResolvedValueOnce({ issue: MOCK_ISSUE, softDuplicate: true })
        .mockResolvedValueOnce({ issue: forcedIssue, softDuplicate: false });

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
        expect(screen.getByTestId("soft-duplicate-notice")).toHaveTextContent(
          "An identical issue was created moments ago",
        );
      });
      expect(onSuccess).not.toHaveBeenCalled();

      fireEvent.click(screen.getByTestId("soft-duplicate-create-anyway"));

      await waitFor(() => {
        expect(mockCreateIssue).toHaveBeenLastCalledWith(
          "test-ws-id",
          {
            title: "Test issue",
            issue_type: "task",
            priority: 2,
          },
          {
            force: true,
            idempotencyKey: expect.stringMatching(/^loom-ui-/),
          },
        );
      });
      await waitFor(() => {
        expect(onSuccess).toHaveBeenCalledWith(forcedIssue);
      });
    });

    it("description is optional: submitting without description works", async () => {
      mockCreateIssue.mockResolvedValue(MOCK_CREATE_RESULT);

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
        expect(mockCreateIssue).toHaveBeenCalledWith(
          "test-ws-id",
          {
            title: "No description issue",
            issue_type: "task",
            priority: 2,
          },
          { idempotencyKey: expect.stringMatching(/^loom-ui-/) },
        );
      });

      // description key should not be present in the request
      const callArgs = mockCreateIssue.mock.calls[0][1];
      expect(callArgs).not.toHaveProperty("description");
    });

    it("includes selected source_repo in multi-repo workspaces", async () => {
      mockCreateIssue.mockResolvedValue(MOCK_CREATE_RESULT);
      mockUseWorkspaceContext.mockReturnValue({
        workspaceId: "test-ws-id",
        repos: [
          { name: "e2e-app", source_repo_id: "e2e-app" },
          { name: "e2e-lib", source_repo_id: "e2e-lib" },
        ],
      } as ReturnType<typeof useWorkspaceContext>);

      render(
        <CreateIssueModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      fireEvent.change(screen.getByTestId("create-issue-title"), {
        target: { value: "Repo scoped issue" },
      });
      fireEvent.change(screen.getByTestId("create-issue-source-repo"), {
        target: { value: "e2e-lib" },
      });
      fireEvent.click(screen.getByTestId("create-issue-submit"));

      await waitFor(() => {
        expect(mockCreateIssue).toHaveBeenCalledWith(
          "test-ws-id",
          {
            title: "Repo scoped issue",
            issue_type: "task",
            priority: 2,
            source_repo: "e2e-lib",
          },
          { idempotencyKey: expect.stringMatching(/^loom-ui-/) },
        );
      });
    });

    it("defaults source_repo in single-repo workspaces", async () => {
      mockCreateIssue.mockResolvedValue(MOCK_CREATE_RESULT);
      mockUseWorkspaceContext.mockReturnValue({
        workspaceId: "test-ws-id",
        repos: [{ name: "hello-world", source_repo_id: "hello-world" }],
      } as ReturnType<typeof useWorkspaceContext>);

      render(
        <CreateIssueModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      expect(screen.getByTestId("create-issue-source-repo")).toBeDisabled();
      fireEvent.change(screen.getByTestId("create-issue-title"), {
        target: { value: "Single repo issue" },
      });
      fireEvent.click(screen.getByTestId("create-issue-submit"));

      await waitFor(() => {
        expect(mockCreateIssue).toHaveBeenCalledWith(
          "test-ws-id",
          {
            title: "Single repo issue",
            issue_type: "task",
            priority: 2,
            source_repo: "hello-world",
          },
          { idempotencyKey: expect.stringMatching(/^loom-ui-/) },
        );
      });
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
      expect(screen.getByTestId("create-issue-description")).toHaveValue("");
    });
  });
});
