/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for CreateWorkspaceModal component.
 */

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { CreateWorkspaceModal } from "../CreateWorkspaceModal";

// ---------- Mocks ----------

vi.mock("@/api/workspace", () => ({
  createWorkspace: vi.fn(),
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

import { createWorkspace } from "@/api/workspace";
import type { WorkspaceData } from "@/api/workspace";

const mockCreateWorkspace = vi.mocked(createWorkspace);

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
        screen.getByText("New git repository from scratch"),
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

    it("browse button is disabled with tooltip", () => {
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
        "Filesystem browsing is not available in the browser",
      );
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
      let resolvePromise!: (value: WorkspaceData) => void;
      mockCreateWorkspace.mockImplementation(
        () =>
          new Promise<WorkspaceData>((resolve) => {
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
      expect(
        screen.getByTestId("create-workspace-spinner"),
      ).toBeInTheDocument();

      // Clean up
      resolvePromise(MOCK_WORKSPACE_DATA);
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
      mockCreateWorkspace.mockResolvedValue(MOCK_WORKSPACE_DATA);

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
      mockCreateWorkspace.mockResolvedValue(MOCK_WORKSPACE_DATA);

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
        expect(onSuccess).toHaveBeenCalledWith(MOCK_WORKSPACE_DATA, "test-ws");
      });
      expect(onClose).toHaveBeenCalledTimes(1);

      expect(mockCreateWorkspace).toHaveBeenCalledWith({
        name: "test-ws",
        type: "empty",
      });
    });

    it("sends clone_urls when type is clone", async () => {
      mockCreateWorkspace.mockResolvedValue(MOCK_WORKSPACE_DATA);

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
      mockCreateWorkspace.mockResolvedValue(MOCK_WORKSPACE_DATA);

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
      mockCreateWorkspace.mockResolvedValue(MOCK_WORKSPACE_DATA);
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
});
