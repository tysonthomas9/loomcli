/**
 * @vitest-environment jsdom
 */

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { createWorkspace } from "@/api/workspace";

import { WorkspaceRepoWizard } from "../WorkspaceRepoWizard";

vi.mock("@/api/workspace", async (orig) => ({
  ...(await orig<typeof import("@/api/workspace")>()),
  createWorkspace: vi.fn(),
}));

vi.mock("@/hooks/agents/useJobPolling", () => ({
  useJobPolling: () => ({
    isPolling: false,
    startJob: vi.fn(),
    reset: vi.fn(),
  }),
}));

const mockCreate = vi.mocked(createWorkspace);

function renderWizard(props: Partial<Parameters<typeof WorkspaceRepoWizard>[0]> = {}) {
  return render(
    <WorkspaceRepoWizard
      isOpen
      onClose={vi.fn()}
      onSuccess={vi.fn()}
      {...props}
    />,
  );
}

describe("WorkspaceRepoWizard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders nothing when closed", () => {
    const { container } = render(
      <WorkspaceRepoWizard isOpen={false} onClose={vi.fn()} onSuccess={vi.fn()} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it("submits a local-path workspace as type=empty with repos", async () => {
    mockCreate.mockResolvedValueOnce({
      kind: "sync",
      data: {
        id: "ws-1",
        name: "my-ws",
        path: "/tmp",
        repos: [],
        groups: [],
        agents: [],
        workspaces: [],
        default_workspace: "",
      },
    });
    const onSuccess = vi.fn();
    const onClose = vi.fn();
    renderWizard({ onSuccess, onClose });

    fireEvent.change(screen.getByTestId("wizard-name"), {
      target: { value: "my-ws" },
    });
    fireEvent.change(screen.getByTestId("wizard-local-path"), {
      target: { value: "/Users/test/code/my-app" },
    });
    fireEvent.click(screen.getByTestId("wizard-submit"));

    await waitFor(() =>
      expect(mockCreate).toHaveBeenCalledWith({
        name: "my-ws",
        type: "empty",
        repos: ["/Users/test/code/my-app"],
      }),
    );
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(onSuccess).toHaveBeenCalled();
  });

  it("switches to clone fields when Git URL is selected", () => {
    renderWizard();
    fireEvent.click(screen.getByTestId("wizard-source-clone"));
    expect(screen.getByTestId("wizard-clone-url")).toBeInTheDocument();
    expect(screen.getByTestId("wizard-branch")).toBeInTheDocument();
    expect(screen.queryByTestId("wizard-local-path")).not.toBeInTheDocument();
  });

  it("submits a clone with optional branch", async () => {
    mockCreate.mockResolvedValueOnce({ kind: "async", jobId: "job-123" });
    renderWizard();

    fireEvent.change(screen.getByTestId("wizard-name"), {
      target: { value: "my-ws" },
    });
    fireEvent.click(screen.getByTestId("wizard-source-clone"));
    fireEvent.change(screen.getByTestId("wizard-clone-url"), {
      target: { value: "git@github.com:org/repo.git" },
    });
    fireEvent.change(screen.getByTestId("wizard-branch"), {
      target: { value: "develop" },
    });
    fireEvent.click(screen.getByTestId("wizard-submit"));

    await waitFor(() =>
      expect(mockCreate).toHaveBeenCalledWith({
        name: "my-ws",
        type: "clone",
        clone_urls: ["git@github.com:org/repo.git"],
        branch: "develop",
      }),
    );
  });

  it("disables submit until name and path are non-empty", () => {
    renderWizard();
    const submit = screen.getByTestId("wizard-submit");
    expect(submit).toBeDisabled();

    fireEvent.change(screen.getByTestId("wizard-name"), {
      target: { value: "ws" },
    });
    expect(submit).toBeDisabled(); // still missing path

    fireEvent.change(screen.getByTestId("wizard-local-path"), {
      target: { value: "/path" },
    });
    expect(submit).not.toBeDisabled();
  });

  it("surfaces API errors", async () => {
    mockCreate.mockRejectedValueOnce(new Error("path does not exist"));
    renderWizard();
    fireEvent.change(screen.getByTestId("wizard-name"), {
      target: { value: "ws" },
    });
    fireEvent.change(screen.getByTestId("wizard-local-path"), {
      target: { value: "/missing" },
    });
    fireEvent.click(screen.getByTestId("wizard-submit"));
    await waitFor(() =>
      expect(screen.getByTestId("wizard-error")).toHaveTextContent(
        /path does not exist/i,
      ),
    );
  });

  it("calls onClose when the backdrop is clicked", () => {
    const onClose = vi.fn();
    renderWizard({ onClose });
    fireEvent.click(screen.getByTestId("workspace-repo-wizard"));
    expect(onClose).toHaveBeenCalled();
  });
});
