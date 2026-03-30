/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for CreateWorkspaceModal component.
 */

import {
  render,
  screen,
  fireEvent,
  waitFor,
  act,
} from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import "@testing-library/jest-dom";

import { CreateWorkspaceModal } from "../CreateWorkspaceModal";

// ---------- Mocks ----------

vi.mock("@/api/workspace", () => ({
  createWorkspace: vi.fn(),
  pollWorkspaceJob: vi.fn(),
  refreshWorkspace: vi.fn(),
}));

vi.mock("@/hooks/useElapsedTime", () => ({
  useElapsedTime: vi.fn(() => "5s"),
}));

vi.mock("@/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/hooks")>();
  return {
    ...actual,
    useRegisterEscapeLayer: vi.fn(),
    LAYER_MODAL: "modal",
  };
});

vi.mock("@/hooks/useFocusTrap", () => ({
  useFocusTrap: vi.fn(),
}));

vi.mock("@/hooks/useFocusReturn", () => ({
  useFocusReturn: vi.fn(),
}));

import {
  createWorkspace,
  pollWorkspaceJob,
  refreshWorkspace,
} from "@/api/workspace";
import type {
  WorkspaceData,
  WorkspaceCreateResult,
  WorkspaceJobState,
} from "@/api/workspace";

const mockCreateWorkspace = vi.mocked(createWorkspace);
const mockPollWorkspaceJob = vi.mocked(pollWorkspaceJob);
const mockRefreshWorkspace = vi.mocked(refreshWorkspace);

const MOCK_WORKSPACE_DATA: WorkspaceData = {
  id: "ws-test-id",
  name: "test-ws",
  path: "/home/user/.loom/workspaces/test-ws",
  repos: [],
  groups: [],
  agents: [],
  workspaces: [],
  default_workspace: "test-ws",
};

/** Helper: return a sync WorkspaceCreateResult wrapping the given data. */
function syncResult(data: WorkspaceData): WorkspaceCreateResult {
  return { kind: "sync", data };
}

describe("CreateWorkspaceModal", () => {
  let onClose: ReturnType<typeof vi.fn>;
  let onSuccess: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    onClose = vi.fn();
    onSuccess = vi.fn();
    mockCreateWorkspace.mockReset();
    mockPollWorkspaceJob.mockReset();
    mockRefreshWorkspace.mockReset();
  });

  describe("rendering", () => {
    it("does not render when isOpen is false", () => {
      render(
        <CreateWorkspaceModal
          isOpen={false}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      expect(
        screen.queryByTestId("create-workspace-overlay"),
      ).not.toBeInTheDocument();
    });

    it("renders when isOpen is true with all form fields", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      // Title heading
      expect(
        screen.getByRole("heading", { name: "Create Workspace" }),
      ).toBeInTheDocument();

      // Name input
      expect(screen.getByTestId("create-workspace-name")).toBeInTheDocument();
      expect(screen.getByLabelText("Name")).toBeInTheDocument();

      // Location input
      expect(screen.getByTestId("create-workspace-path")).toBeInTheDocument();
      expect(screen.getByLabelText("Location")).toBeInTheDocument();

      // Type radios (Empty is now "Local Repos")
      expect(screen.getByLabelText("Local Repos")).toBeInTheDocument();
      expect(screen.getByLabelText("Clone")).toBeInTheDocument();
      expect(screen.getByLabelText("Template")).toBeInTheDocument();

      // Buttons
      expect(screen.getByTestId("create-workspace-cancel")).toBeInTheDocument();
      expect(screen.getByTestId("create-workspace-submit")).toBeInTheDocument();

      // Dialog role
      const dialog = screen.getByRole("dialog");
      expect(dialog).toHaveAttribute("aria-modal", "true");
      expect(dialog).toHaveAttribute("aria-label", "Create Workspace");
    });

    it("template type radio is disabled", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      const templateRadio = screen.getByTestId(
        "create-workspace-template-radio",
      );
      expect(templateRadio).toBeDisabled();
    });
  });

  describe("clone URL field", () => {
    it("does not show clone URL field when type is Local Repos", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      // Default type is "clone", switch to "Local Repos" (empty)
      fireEvent.click(screen.getByLabelText("Local Repos"));

      expect(
        screen.queryByTestId("create-workspace-clone-url"),
      ).not.toBeInTheDocument();
    });

    it("shows clone URL field when type is clone (default)", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      // Clone is the default type, so clone URL field should be visible
      expect(
        screen.getByTestId("create-workspace-clone-url"),
      ).toBeInTheDocument();
      expect(screen.getByLabelText("Repository URLs")).toBeInTheDocument();
    });
  });

  describe("submit button state", () => {
    it("submit button is disabled when name is empty", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      expect(screen.getByTestId("create-workspace-submit")).toBeDisabled();
    });

    it("submit button is enabled when name and clone URL are filled", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      // Default type is clone, so both name and URL are needed
      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: "my-workspace" },
      });
      fireEvent.change(screen.getByTestId("create-workspace-clone-url"), {
        target: { value: "https://github.com/example/repo" },
      });

      expect(screen.getByTestId("create-workspace-submit")).toBeEnabled();
    });

    it("submit button is disabled when type is clone and clone URL is empty", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: "my-workspace" },
      });
      fireEvent.click(screen.getByLabelText("Clone"));

      expect(screen.getByTestId("create-workspace-submit")).toBeDisabled();
    });

    it("submit button is enabled when type is clone and both name and URL are filled", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: "my-workspace" },
      });
      fireEvent.click(screen.getByLabelText("Clone"));
      fireEvent.change(screen.getByTestId("create-workspace-clone-url"), {
        target: { value: "https://github.com/example/repo" },
      });

      expect(screen.getByTestId("create-workspace-submit")).toBeEnabled();
    });

    it("shows 'Creating...' while submitting", async () => {
      // Keep the promise pending so we can observe the submitting state
      let resolvePromise!: (value: WorkspaceCreateResult) => void;
      mockCreateWorkspace.mockImplementation(
        () =>
          new Promise<WorkspaceCreateResult>((resolve) => {
            resolvePromise = resolve;
          }),
      );

      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      // Default type is clone, need name + URL to enable submit
      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: "my-workspace" },
      });
      fireEvent.change(screen.getByTestId("create-workspace-clone-url"), {
        target: { value: "https://github.com/example/repo" },
      });
      fireEvent.click(screen.getByTestId("create-workspace-submit"));

      await waitFor(() => {
        expect(screen.getByTestId("create-workspace-submit")).toHaveTextContent(
          "Creating...",
        );
      });

      expect(screen.getByTestId("create-workspace-submit")).toBeDisabled();

      // Clean up
      await act(async () => {
        resolvePromise(syncResult(MOCK_WORKSPACE_DATA));
      });
    });
  });

  describe("form submission", () => {
    it("calls onSuccess and onClose after successful sync API call", async () => {
      mockCreateWorkspace.mockResolvedValue(syncResult(MOCK_WORKSPACE_DATA));

      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      // Switch to "Local Repos" (empty) type so name alone is sufficient
      fireEvent.click(screen.getByLabelText("Local Repos"));
      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: "test-ws" },
      });
      // Need to add a repo path for empty type
      fireEvent.change(screen.getByTestId("create-workspace-repo-path"), {
        target: { value: "/path/to/repo" },
      });
      fireEvent.click(screen.getByTestId("create-workspace-submit"));

      await waitFor(() => {
        expect(onSuccess).toHaveBeenCalledWith(MOCK_WORKSPACE_DATA, "test-ws");
      });
      expect(onClose).toHaveBeenCalledTimes(1);

      expect(mockCreateWorkspace).toHaveBeenCalledWith({
        name: "test-ws",
        type: "empty",
        repos: ["/path/to/repo"],
      });
    });

    it("sends clone_urls when type is clone", async () => {
      mockCreateWorkspace.mockResolvedValue(syncResult(MOCK_WORKSPACE_DATA));

      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      // Clone is the default type
      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: "cloned-ws" },
      });
      fireEvent.change(screen.getByTestId("create-workspace-clone-url"), {
        target: { value: "https://github.com/example/repo.git" },
      });
      fireEvent.click(screen.getByTestId("create-workspace-submit"));

      await waitFor(() => {
        expect(mockCreateWorkspace).toHaveBeenCalledWith({
          name: "cloned-ws",
          type: "clone",
          clone_urls: ["https://github.com/example/repo.git"],
        });
      });
    });

    it("sends path when provided", async () => {
      mockCreateWorkspace.mockResolvedValue(syncResult(MOCK_WORKSPACE_DATA));

      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      // Clone is the default type, provide URL + path
      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: "my-ws" },
      });
      fireEvent.change(screen.getByTestId("create-workspace-clone-url"), {
        target: { value: "https://github.com/example/repo" },
      });
      fireEvent.change(screen.getByTestId("create-workspace-path"), {
        target: { value: "/custom/path" },
      });
      fireEvent.click(screen.getByTestId("create-workspace-submit"));

      await waitFor(() => {
        expect(mockCreateWorkspace).toHaveBeenCalledWith({
          name: "my-ws",
          type: "clone",
          clone_urls: ["https://github.com/example/repo"],
          path: "/custom/path",
        });
      });
    });

    it("displays error message on API failure", async () => {
      mockCreateWorkspace.mockRejectedValue(new Error("Network error"));

      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      // Default type is clone, provide URL
      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: "fail-ws" },
      });
      fireEvent.change(screen.getByTestId("create-workspace-clone-url"), {
        target: { value: "https://github.com/example/repo" },
      });
      fireEvent.click(screen.getByTestId("create-workspace-submit"));

      await waitFor(() => {
        expect(
          screen.getByTestId("create-workspace-error"),
        ).toBeInTheDocument();
      });
      expect(screen.getByTestId("create-workspace-error")).toHaveTextContent(
        "Network error",
      );
      expect(onSuccess).not.toHaveBeenCalled();
      expect(onClose).not.toHaveBeenCalled();
    });

    it("displays server error from API body", async () => {
      const apiError = Object.assign(new Error("api error"), {
        body: { error: "Workspace name already exists" },
      });
      mockCreateWorkspace.mockRejectedValue(apiError);

      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      // Default type is clone, provide URL
      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: "dup-ws" },
      });
      fireEvent.change(screen.getByTestId("create-workspace-clone-url"), {
        target: { value: "https://github.com/example/repo" },
      });
      fireEvent.click(screen.getByTestId("create-workspace-submit"));

      await waitFor(() => {
        expect(
          screen.getByTestId("create-workspace-error"),
        ).toBeInTheDocument();
      });
      expect(screen.getByTestId("create-workspace-error")).toHaveTextContent(
        "Workspace name already exists",
      );
    });
  });

  describe("async clone flow", () => {
    beforeEach(() => {
      vi.useFakeTimers();
    });

    afterEach(() => {
      vi.useRealTimers();
    });

    /** Submit the clone form and flush microtasks so the async result is processed. */
    async function submitCloneForm(name: string, url: string) {
      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: name },
      });
      fireEvent.change(screen.getByTestId("create-workspace-clone-url"), {
        target: { value: url },
      });

      // eslint-disable-next-line @typescript-eslint/require-await
      await act(async () => {
        fireEvent.click(screen.getByTestId("create-workspace-submit"));
      });
    }

    it("shows progress UI when createWorkspace returns async result", async () => {
      mockCreateWorkspace.mockResolvedValue({
        kind: "async",
        jobId: "test-job-123",
      });

      // Keep pollWorkspaceJob pending indefinitely for this test
      mockPollWorkspaceJob.mockImplementation(
        () => new Promise<WorkspaceJobState>(() => {}),
      );

      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      await submitCloneForm("clone-ws", "https://github.com/example/repo");

      // Progress UI should be visible
      expect(
        screen.getByTestId("create-workspace-progress"),
      ).toBeInTheDocument();

      // Title should change
      expect(
        screen.getByRole("heading", { name: "Creating Workspace" }),
      ).toBeInTheDocument();

      // Progress message and elapsed time should be visible
      expect(screen.getByText("Cloning repositories...")).toBeInTheDocument();
      expect(screen.getByText("5s")).toBeInTheDocument();
    });

    it("polls and navigates on job completion", async () => {
      mockCreateWorkspace.mockResolvedValue({
        kind: "async",
        jobId: "test-job-456",
      });

      // First poll: running with progress
      // Second poll: done with workspace_id
      let pollCount = 0;
      mockPollWorkspaceJob.mockImplementation(async () => {
        pollCount++;
        if (pollCount === 1) {
          return {
            status: "running" as const,
            progress: "Cloning repo 1/2...",
          };
        }
        return {
          status: "done" as const,
          workspace_id: "ws-new-id",
        };
      });

      mockRefreshWorkspace.mockResolvedValue(MOCK_WORKSPACE_DATA);

      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      await submitCloneForm("clone-ws", "https://github.com/example/repo");

      // Progress UI should be visible
      expect(
        screen.getByTestId("create-workspace-progress"),
      ).toBeInTheDocument();

      // Advance past the initial 1000ms delay to trigger first poll
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1000);
      });

      // First poll returns "running" — progress message should update
      expect(screen.getByText("Cloning repo 1/2...")).toBeInTheDocument();

      // Advance past the 2000ms poll interval to trigger second poll
      await act(async () => {
        await vi.advanceTimersByTimeAsync(2000);
      });

      // Second poll returns "done" — should call refreshWorkspace and then onSuccess
      expect(mockRefreshWorkspace).toHaveBeenCalledWith("ws-new-id");
      expect(onSuccess).toHaveBeenCalledWith(MOCK_WORKSPACE_DATA, "clone-ws");
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("shows error when async job fails", async () => {
      mockCreateWorkspace.mockResolvedValue({
        kind: "async",
        jobId: "test-job-fail",
      });

      mockPollWorkspaceJob.mockResolvedValue({
        status: "failed",
        error: "Clone failed: repository not found",
      });

      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      await submitCloneForm(
        "fail-clone-ws",
        "https://github.com/example/nonexistent",
      );

      // Progress UI should be visible
      expect(
        screen.getByTestId("create-workspace-progress"),
      ).toBeInTheDocument();

      // Advance past the initial 1000ms delay to trigger poll
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1000);
      });

      // Poll returns "failed" — should show error and go back to form
      expect(screen.getByTestId("create-workspace-error")).toBeInTheDocument();
      expect(screen.getByTestId("create-workspace-error")).toHaveTextContent(
        "Clone failed: repository not found",
      );
      expect(onSuccess).not.toHaveBeenCalled();
      expect(onClose).not.toHaveBeenCalled();
    });

    it("shows error when poll connection is lost", async () => {
      mockCreateWorkspace.mockResolvedValue({
        kind: "async",
        jobId: "test-job-disconnect",
      });

      mockPollWorkspaceJob.mockRejectedValue(new Error("fetch failed"));

      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      await submitCloneForm("lost-ws", "https://github.com/example/repo");

      // Progress UI should be visible
      expect(
        screen.getByTestId("create-workspace-progress"),
      ).toBeInTheDocument();

      // Advance past the initial 1000ms delay to trigger poll
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1000);
      });

      // Poll throws — should show connection-lost error
      expect(screen.getByTestId("create-workspace-error")).toBeInTheDocument();
      expect(screen.getByTestId("create-workspace-error")).toHaveTextContent(
        "Lost connection while creating workspace",
      );
    });

    it("overlay click is disabled during async clone progress", async () => {
      mockCreateWorkspace.mockResolvedValue({
        kind: "async",
        jobId: "test-job-overlay",
      });

      mockPollWorkspaceJob.mockImplementation(
        () => new Promise<WorkspaceJobState>(() => {}),
      );

      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      await submitCloneForm("overlay-ws", "https://github.com/example/repo");

      // Progress UI should be visible
      expect(
        screen.getByTestId("create-workspace-progress"),
      ).toBeInTheDocument();

      // Clicking overlay should NOT close the modal during progress
      fireEvent.click(screen.getByTestId("create-workspace-overlay"));
      expect(onClose).not.toHaveBeenCalled();
    });
  });

  describe("interactions", () => {
    it("cancel button calls onClose", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      fireEvent.click(screen.getByTestId("create-workspace-cancel"));
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("overlay click calls onClose", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      fireEvent.click(screen.getByTestId("create-workspace-overlay"));
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("dialog content click does not call onClose", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      const dialog = screen.getByRole("dialog");
      fireEvent.click(dialog);
      expect(onClose).not.toHaveBeenCalled();
    });
  });

  describe("form reset", () => {
    it("form resets when modal reopens", () => {
      const { rerender } = render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      // Fill in form fields (default type is clone)
      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: "dirty-ws" },
      });
      fireEvent.change(screen.getByTestId("create-workspace-path"), {
        target: { value: "/some/path" },
      });
      // Switch to Local Repos to test reset back to Clone
      fireEvent.click(screen.getByLabelText("Local Repos"));

      // Close the modal
      rerender(
        <CreateWorkspaceModal
          isOpen={false}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      // Reopen the modal
      rerender(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      // All fields should be reset
      expect(screen.getByTestId("create-workspace-name")).toHaveValue("");
      expect(screen.getByTestId("create-workspace-path")).toHaveValue("");
      // Default type resets to "clone"
      expect(screen.getByLabelText("Clone")).toBeChecked();
      expect(screen.getByLabelText("Local Repos")).not.toBeChecked();
      // Clone URL field should be visible since type reset to "clone"
      expect(
        screen.getByTestId("create-workspace-clone-url"),
      ).toBeInTheDocument();
    });
  });
});
