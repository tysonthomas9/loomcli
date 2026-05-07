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
  fetchWorkspaceApi: vi.fn(),
}));

vi.mock("@/hooks/common", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/common")>("@/hooks/common");
  return { ...actual, useElapsedTime: vi.fn(() => "5s") };
});

vi.mock("@/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/hooks")>();
  return {
    ...actual,
    useRegisterEscapeLayer: vi.fn(),
    LAYER_MODAL: "modal",
  };
});

vi.mock("@/hooks/ui", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/ui")>("@/hooks/ui");
  return {
    ...actual,
    useFocusTrap: vi.fn(),
    useFocusReturn: vi.fn(),
  };
});

import {
  createWorkspace,
  pollWorkspaceJob,
  fetchWorkspaceApi,
} from "@/api/workspace";
import type {
  WorkspaceData,
  WorkspaceCreateResult,
  WorkspaceJobState,
} from "@/api/workspace";

const mockCreateWorkspace = vi.mocked(createWorkspace);
const mockPollWorkspaceJob = vi.mocked(pollWorkspaceJob);
const mockFetchWorkspaceApi = vi.mocked(fetchWorkspaceApi);

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

const MOCK_CREATE_RESULT: WorkspaceCreateResult =
  syncResult(MOCK_WORKSPACE_DATA);

const MOCK_CREATE_RESULT_WITH_WARNINGS: WorkspaceCreateResult = {
  kind: "sync",
  data: MOCK_WORKSPACE_DATA,
  warnings: ["Daemon startup failed: timeout after 30s"],
};

/** Helper to select a type card by clicking it */
function selectTypeCard(type: "empty" | "clone" | "template") {
  fireEvent.click(screen.getByTestId(`create-workspace-type-${type}`));
}

describe("CreateWorkspaceModal", () => {
  let onClose: ReturnType<typeof vi.fn>;
  let onSuccess: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    onClose = vi.fn();
    onSuccess = vi.fn();
    mockCreateWorkspace.mockReset();
    mockPollWorkspaceJob.mockReset();
    mockFetchWorkspaceApi.mockReset();
    Reflect.deleteProperty(window, "__TAURI__");
    Reflect.deleteProperty(window, "__TAURI_INTERNALS__");
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
        screen.getByRole("heading", { name: "New Workspace" }),
      ).toBeInTheDocument();

      // Close button
      expect(screen.getByTestId("create-workspace-close")).toBeInTheDocument();

      // Name input
      expect(screen.getByTestId("create-workspace-name")).toBeInTheDocument();
      expect(screen.getByLabelText("Name")).toBeInTheDocument();

      // Location input
      expect(screen.getByTestId("create-workspace-path")).toBeInTheDocument();
      expect(screen.getByLabelText("Location")).toBeInTheDocument();

      // Browse button
      expect(screen.getByTestId("create-workspace-browse")).toBeInTheDocument();

      // Type cards (role="radio")
      const radioCards = screen.getAllByRole("radio");
      expect(radioCards).toHaveLength(3);
      expect(
        screen.getByTestId("create-workspace-type-empty"),
      ).toBeInTheDocument();
      expect(
        screen.getByTestId("create-workspace-type-clone"),
      ).toBeInTheDocument();
      expect(
        screen.getByTestId("create-workspace-type-template"),
      ).toBeInTheDocument();

      // Card descriptions
      expect(
        screen.getByText("Attach local repos now or later"),
      ).toBeInTheDocument();
      expect(screen.getByText("Clone from a remote URL")).toBeInTheDocument();
      expect(
        screen.getByText("Start from a project template"),
      ).toBeInTheDocument();

      // Buttons
      expect(screen.getByTestId("create-workspace-cancel")).toBeInTheDocument();
      expect(screen.getByTestId("create-workspace-submit")).toBeInTheDocument();

      // Dialog role
      const dialog = screen.getByRole("dialog");
      expect(dialog).toHaveAttribute("aria-modal", "true");
      expect(dialog).toHaveAttribute("aria-label", "New Workspace");

      // Radiogroup
      expect(screen.getByRole("radiogroup")).toHaveAttribute(
        "aria-label",
        "Workspace type",
      );
    });

    it("clone card is selected by default", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      const cloneCard = screen.getByTestId("create-workspace-type-clone");
      expect(cloneCard).toHaveAttribute("aria-checked", "true");

      const emptyCard = screen.getByTestId("create-workspace-type-empty");
      expect(emptyCard).toHaveAttribute("aria-checked", "false");

      const templateCard = screen.getByTestId("create-workspace-type-template");
      expect(templateCard).toHaveAttribute("aria-checked", "false");
    });

    it("browse button is disabled outside the desktop app", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      const browseBtn = screen.getByTestId("create-workspace-browse");
      expect(browseBtn).toBeDisabled();
      expect(browseBtn).toHaveAttribute(
        "title",
        "Filesystem browsing is only available in the desktop app",
      );
    });

    it("fills the workspace location from the desktop folder picker", async () => {
      const invoke = vi.fn().mockResolvedValue("/Users/test/workspaces/demo");
      window.__TAURI__ = { core: { invoke } };

      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      fireEvent.click(screen.getByTestId("create-workspace-browse"));

      await waitFor(() => {
        expect(screen.getByTestId("create-workspace-path")).toHaveValue(
          "/Users/test/workspaces/demo",
        );
      });
      expect(invoke).toHaveBeenCalledWith("pick_folder");
    });

    it("fills an empty workspace repository path from the desktop folder picker", async () => {
      const invoke = vi.fn().mockResolvedValue("/Users/test/repos/api");
      window.__TAURI__ = { core: { invoke } };

      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      selectTypeCard("empty");
      fireEvent.click(screen.getByTestId("create-workspace-repo-browse"));

      await waitFor(() => {
        expect(screen.getByTestId("create-workspace-repo-path")).toHaveValue(
          "/Users/test/repos/api",
        );
      });
      expect(invoke).toHaveBeenCalledWith("pick_folder");
    });

    it("template card is selectable and shows coming-soon placeholder", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      selectTypeCard("template");

      const templateCard = screen.getByTestId("create-workspace-type-template");
      expect(templateCard).toHaveAttribute("aria-checked", "true");

      expect(
        screen.getByTestId("create-workspace-template-placeholder"),
      ).toBeInTheDocument();
      expect(
        screen.getByText(
          "Coming soon — template registry is not yet available",
        ),
      ).toBeInTheDocument();
    });
  });

  describe("type selector", () => {
    it("clicking Empty card selects it and hides clone URL field", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      // Clone is default — clone URL field visible
      expect(
        screen.getByTestId("create-workspace-clone-url"),
      ).toBeInTheDocument();

      selectTypeCard("empty");

      expect(screen.getByTestId("create-workspace-type-empty")).toHaveAttribute(
        "aria-checked",
        "true",
      );
      expect(
        screen.queryByTestId("create-workspace-clone-url"),
      ).not.toBeInTheDocument();
    });

    it("clicking Clone card after switching shows clone URL field", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      selectTypeCard("empty");
      expect(
        screen.queryByTestId("create-workspace-clone-url"),
      ).not.toBeInTheDocument();

      selectTypeCard("clone");
      expect(
        screen.getByTestId("create-workspace-clone-url"),
      ).toBeInTheDocument();
    });

    it("clicking Template hides clone URL field and shows placeholder", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      selectTypeCard("template");

      expect(
        screen.queryByTestId("create-workspace-clone-url"),
      ).not.toBeInTheDocument();
      expect(
        screen.getByTestId("create-workspace-template-placeholder"),
      ).toBeInTheDocument();
    });
  });

  describe("clone URL field", () => {
    it("does not show clone URL field when type is Empty", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      selectTypeCard("empty");

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
      expect(screen.getByLabelText("Repository URL")).toBeInTheDocument();
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
      fireEvent.change(screen.getByTestId("create-workspace-clone-url"), {
        target: { value: "https://github.com/example/repo" },
      });

      expect(screen.getByTestId("create-workspace-submit")).toBeEnabled();
    });

    it("submit button is enabled when type is empty and name is filled", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      selectTypeCard("empty");
      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: "my-workspace" },
      });

      expect(screen.getByTestId("create-workspace-submit")).toBeEnabled();
    });

    it("submit button is disabled when template is selected regardless of name", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      selectTypeCard("template");
      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: "my-workspace" },
      });

      expect(screen.getByTestId("create-workspace-submit")).toBeDisabled();
    });

    it("shows 'Creating...' and spinner while submitting", async () => {
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
      await act(async () => {
        fireEvent.click(screen.getByTestId("create-workspace-submit"));
      });

      expect(screen.getByTestId("create-workspace-submit")).toHaveTextContent(
        "Creating...",
      );

      expect(screen.getByTestId("create-workspace-submit")).toBeDisabled();
      expect(
        screen.getByTestId("create-workspace-spinner"),
      ).toBeInTheDocument();

      // Clean up
      await act(async () => {
        resolvePromise(MOCK_CREATE_RESULT);
      });
    });

    it("does not show spinner before submission", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      expect(
        screen.queryByTestId("create-workspace-spinner"),
      ).not.toBeInTheDocument();
    });

    it("removes spinner after successful submission", async () => {
      mockCreateWorkspace.mockResolvedValue(MOCK_CREATE_RESULT);

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
        expect(onSuccess).toHaveBeenCalled();
      });

      expect(
        screen.queryByTestId("create-workspace-spinner"),
      ).not.toBeInTheDocument();
    });

    it("removes spinner after failed submission", async () => {
      mockCreateWorkspace.mockRejectedValue(new Error("Network error"));

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
        expect(
          screen.getByTestId("create-workspace-error"),
        ).toBeInTheDocument();
      });

      expect(
        screen.queryByTestId("create-workspace-spinner"),
      ).not.toBeInTheDocument();
    });
  });

  describe("form submission", () => {
    it("calls onSuccess and onClose after successful empty type submission", async () => {
      mockCreateWorkspace.mockResolvedValue(MOCK_CREATE_RESULT);

      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      selectTypeCard("empty");
      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: "test-ws" },
      });
      fireEvent.click(screen.getByTestId("create-workspace-submit"));

      await waitFor(() => {
        expect(onSuccess).toHaveBeenCalledWith(
          MOCK_WORKSPACE_DATA,
          "test-ws",
          undefined,
        );
      });
      expect(onClose).toHaveBeenCalledTimes(1);

      expect(mockCreateWorkspace).toHaveBeenCalledWith({
        name: "test-ws",
        type: "empty",
      });
    });

    it("sends pending repo path when empty workspace is submitted", async () => {
      mockCreateWorkspace.mockResolvedValue(MOCK_CREATE_RESULT);

      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      selectTypeCard("empty");
      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: "test-ws" },
      });
      fireEvent.change(screen.getByTestId("create-workspace-repo-path"), {
        target: { value: "/workspace/api" },
      });
      fireEvent.click(screen.getByTestId("create-workspace-submit"));

      await waitFor(() => {
        expect(mockCreateWorkspace).toHaveBeenCalledWith({
          name: "test-ws",
          type: "empty",
          repos: ["/workspace/api"],
        });
      });
    });

    it("splits multiline pending repo paths when empty workspace is submitted", async () => {
      mockCreateWorkspace.mockResolvedValue(MOCK_CREATE_RESULT);

      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      selectTypeCard("empty");
      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: "test-ws" },
      });
      fireEvent.change(screen.getByTestId("create-workspace-repo-path"), {
        target: { value: "/workspace/api\n/workspace/web" },
      });
      fireEvent.click(screen.getByTestId("create-workspace-submit"));

      await waitFor(() => {
        expect(mockCreateWorkspace).toHaveBeenCalledWith({
          name: "test-ws",
          type: "empty",
          repos: ["/workspace/api", "/workspace/web"],
        });
      });
    });

    it("sends clone_urls when type is clone", async () => {
      mockCreateWorkspace.mockResolvedValue(MOCK_CREATE_RESULT);

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

    it("splits multiline clone URLs when clone workspace is submitted", async () => {
      mockCreateWorkspace.mockResolvedValue(MOCK_CREATE_RESULT);

      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: "cloned-ws" },
      });
      fireEvent.change(screen.getByTestId("create-workspace-clone-url"), {
        target: {
          value:
            "https://github.com/example/api.git\nhttps://github.com/example/web.git",
        },
      });
      fireEvent.click(screen.getByTestId("create-workspace-submit"));

      await waitFor(() => {
        expect(mockCreateWorkspace).toHaveBeenCalledWith({
          name: "cloned-ws",
          type: "clone",
          clone_urls: [
            "https://github.com/example/api.git",
            "https://github.com/example/web.git",
          ],
        });
      });
    });

    it("sends path when provided", async () => {
      mockCreateWorkspace.mockResolvedValue(MOCK_CREATE_RESULT);

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

    it("template type cannot submit", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      selectTypeCard("template");
      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: "template-ws" },
      });

      expect(screen.getByTestId("create-workspace-submit")).toBeDisabled();
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

  describe("modal close on success", () => {
    it("closes modal when onSuccess throws", async () => {
      mockCreateWorkspace.mockResolvedValue(MOCK_CREATE_RESULT);
      onSuccess.mockImplementation(() => {
        throw new Error("navigation failed");
      });

      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      selectTypeCard("empty");
      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: "test-ws" },
      });
      fireEvent.click(screen.getByTestId("create-workspace-submit"));

      await waitFor(() => {
        expect(onClose).toHaveBeenCalledTimes(1);
      });
      expect(onSuccess).toHaveBeenCalled();
    });

    it("does not close modal on API failure", async () => {
      mockCreateWorkspace.mockRejectedValue(new Error("Network error"));

      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      selectTypeCard("empty");
      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: "test-ws" },
      });
      fireEvent.click(screen.getByTestId("create-workspace-submit"));

      await waitFor(() => {
        expect(
          screen.getByTestId("create-workspace-error"),
        ).toBeInTheDocument();
      });
      expect(onClose).not.toHaveBeenCalled();
      expect(onSuccess).not.toHaveBeenCalled();
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

      mockFetchWorkspaceApi.mockResolvedValue(MOCK_WORKSPACE_DATA);

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

      // Second poll returns "done" — should call fetchWorkspaceApi and then onSuccess
      expect(mockFetchWorkspaceApi).toHaveBeenCalledWith("ws-new-id");
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

    it("close button calls onClose", () => {
      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      fireEvent.click(screen.getByTestId("create-workspace-close"));
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
      // Switch to Empty to test reset back to Clone
      selectTypeCard("empty");

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
      expect(screen.getByTestId("create-workspace-type-clone")).toHaveAttribute(
        "aria-checked",
        "true",
      );
      expect(screen.getByTestId("create-workspace-type-empty")).toHaveAttribute(
        "aria-checked",
        "false",
      );
      // Clone URL field should be visible since type reset to "clone"
      expect(
        screen.getByTestId("create-workspace-clone-url"),
      ).toBeInTheDocument();
    });
  });

  describe("warnings passthrough", () => {
    it("passes warnings to onSuccess when present in API response", async () => {
      mockCreateWorkspace.mockResolvedValue(MOCK_CREATE_RESULT_WITH_WARNINGS);

      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      selectTypeCard("empty");
      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: "warn-ws" },
      });
      fireEvent.click(screen.getByTestId("create-workspace-submit"));

      await waitFor(() => {
        expect(onSuccess).toHaveBeenCalledWith(
          MOCK_WORKSPACE_DATA,
          "warn-ws",
          MOCK_CREATE_RESULT_WITH_WARNINGS.warnings,
        );
      });
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("passes undefined warnings to onSuccess when no warnings in API response", async () => {
      mockCreateWorkspace.mockResolvedValue(MOCK_CREATE_RESULT);

      render(
        <CreateWorkspaceModal
          isOpen={true}
          onClose={onClose}
          onSuccess={onSuccess}
        />,
      );

      selectTypeCard("empty");
      fireEvent.change(screen.getByTestId("create-workspace-name"), {
        target: { value: "no-warn-ws" },
      });
      fireEvent.click(screen.getByTestId("create-workspace-submit"));

      await waitFor(() => {
        expect(onSuccess).toHaveBeenCalledWith(
          MOCK_WORKSPACE_DATA,
          "no-warn-ws",
          undefined,
        );
      });
      expect(onClose).toHaveBeenCalledTimes(1);
    });
  });
});
