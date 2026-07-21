/**
 * @vitest-environment jsdom
 */
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

const mockAddWorkspaceRepos = vi.fn();
vi.mock("@/hooks/api", () => ({
  addWorkspaceRepos: (...args: unknown[]) => mockAddWorkspaceRepos(...args),
}));

import { AddRepoModal } from "../AddRepoModal";

const WS = "ws-1";

function renderModal(props: Partial<Parameters<typeof AddRepoModal>[0]> = {}) {
  const onClose = vi.fn();
  const onSuccess = vi.fn();
  render(
    <AddRepoModal
      isOpen
      workspaceId={WS}
      onClose={onClose}
      onSuccess={onSuccess}
      {...props}
    />,
  );
  return { onClose, onSuccess };
}

describe("AddRepoModal", () => {
  beforeEach(() => {
    mockAddWorkspaceRepos.mockReset();
    mockAddWorkspaceRepos.mockResolvedValue({});
  });

  it("renders nothing when closed", () => {
    const { container } = render(
      <AddRepoModal
        isOpen={false}
        workspaceId={WS}
        onClose={vi.fn()}
        onSuccess={vi.fn()}
      />,
    );
    expect(container).toBeEmptyDOMElement();
    expect(screen.queryByTestId("add-repo-overlay")).not.toBeInTheDocument();
  });

  it("renders the overlay on document.body so it covers the full app", () => {
    renderModal();
    const overlay = screen.getByTestId("add-repo-overlay");
    expect(overlay.parentElement).toBe(document.body);
  });

  it("seeds the URL field and leaves branch empty for remote HEAD detection", () => {
    renderModal({ initialUrl: "https://github.com/octocat/Hello-World" });
    expect(screen.getByLabelText("Repository URL")).toHaveValue(
      "https://github.com/octocat/Hello-World",
    );
    expect(screen.getByLabelText("Default branch")).toHaveValue("");
  });

  it("submits a clone URL with the chosen branch", async () => {
    const { onSuccess, onClose } = renderModal();
    fireEvent.change(screen.getByLabelText("Repository URL"), {
      target: { value: "https://github.com/org/repo" },
    });
    fireEvent.change(screen.getByLabelText("Default branch"), {
      target: { value: "develop" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Add Repository" }));

    await waitFor(() =>
      expect(mockAddWorkspaceRepos).toHaveBeenCalledWith(WS, {
        clone_urls: ["https://github.com/org/repo"],
        branch: "develop",
      }),
    );
    expect(onSuccess).toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
  });

  it("treats a non-URL value as a local repo path", async () => {
    renderModal();
    fireEvent.change(screen.getByLabelText("Repository URL"), {
      target: { value: "/repos/local" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Add Repository" }));

    await waitFor(() =>
      expect(mockAddWorkspaceRepos).toHaveBeenCalledWith(WS, {
        repos: ["/repos/local"],
      }),
    );
  });

  it("disables submit until a URL is entered", () => {
    renderModal();
    expect(
      screen.getByRole("button", { name: "Add Repository" }),
    ).toBeDisabled();
  });

  it("surfaces add-repo errors", async () => {
    mockAddWorkspaceRepos.mockRejectedValueOnce(new Error("clone failed"));
    renderModal();
    fireEvent.change(screen.getByLabelText("Repository URL"), {
      target: { value: "https://github.com/org/repo" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Add Repository" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("clone failed");
  });
});
