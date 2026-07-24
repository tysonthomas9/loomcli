/**
 * @vitest-environment jsdom
 */
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

const mockAddWorkspaceRepos = vi.fn();
const mockPollWorkspaceJob = vi.fn();
const mockFetchWorkspaceApi = vi.fn();
vi.mock("@/hooks/api", () => ({
  addWorkspaceRepos: (...args: unknown[]) => mockAddWorkspaceRepos(...args),
}));
vi.mock("@/api/workspace", () => ({
  pollWorkspaceJob: (...args: unknown[]) => mockPollWorkspaceJob(...args),
  fetchWorkspaceApi: (...args: unknown[]) => mockFetchWorkspaceApi(...args),
}));

import { AddRepoModal } from "../AddRepoModal";

const WS = "ws-1";

function renderModal(props: Partial<Parameters<typeof AddRepoModal>[0]> = {}) {
  const onClose = vi.fn();
  const onSuccess = vi.fn();
  const view = render(
    <AddRepoModal
      isOpen
      workspaceId={WS}
      onClose={onClose}
      onSuccess={onSuccess}
      {...props}
    />,
  );
  return { ...view, onClose, onSuccess };
}

describe("AddRepoModal", () => {
  beforeEach(() => {
    mockAddWorkspaceRepos.mockReset();
    mockPollWorkspaceJob.mockReset();
    mockFetchWorkspaceApi.mockReset();
    mockAddWorkspaceRepos.mockResolvedValue({ kind: "sync", data: {} });
    mockFetchWorkspaceApi.mockResolvedValue({ id: WS });
  });

  afterEach(() => {
    vi.useRealTimers();
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

  it("polls an accepted remote clone before reporting success", async () => {
    vi.useFakeTimers();
    mockAddWorkspaceRepos.mockResolvedValueOnce({
      kind: "async",
      jobId: "add-repos-job-123",
    });
    mockPollWorkspaceJob.mockResolvedValueOnce({
      status: "done",
      workspace_id: WS,
    });
    const { onSuccess, onClose } = renderModal();

    fireEvent.change(screen.getByLabelText("Repository URL"), {
      target: { value: "https://github.com/org/slow-repo" },
    });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Add Repository" }));
    });

    expect(screen.getByTestId("add-repo-progress")).toBeInTheDocument();
    expect(screen.getByText("Cloning repository...")).toBeInTheDocument();
    expect(onSuccess).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });

    expect(mockPollWorkspaceJob).toHaveBeenCalledWith("add-repos-job-123");
    expect(mockFetchWorkspaceApi).toHaveBeenCalledWith(WS);
    expect(onSuccess).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("keeps polling when refreshed workspace data changes the initial URL", async () => {
    vi.useFakeTimers();
    mockAddWorkspaceRepos.mockResolvedValueOnce({
      kind: "async",
      jobId: "add-repos-job-rerender",
    });
    mockPollWorkspaceJob.mockResolvedValueOnce({
      status: "done",
      workspace_id: WS,
    });
    const { onSuccess, onClose, rerender } = renderModal({
      initialUrl: "https://github.com/org/slow-repo",
    });

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Add Repository" }));
    });
    expect(screen.getByTestId("add-repo-progress")).toBeInTheDocument();

    rerender(
      <AddRepoModal
        isOpen
        workspaceId={WS}
        initialUrl=""
        onClose={onClose}
        onSuccess={onSuccess}
      />,
    );

    expect(screen.getByTestId("add-repo-progress")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Add Repository" }),
    ).not.toBeInTheDocument();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });

    expect(mockPollWorkspaceJob).toHaveBeenCalledWith("add-repos-job-rerender");
    expect(onSuccess).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("surfaces the async job's terminal failure without closing", async () => {
    vi.useFakeTimers();
    mockAddWorkspaceRepos.mockResolvedValueOnce({
      kind: "async",
      jobId: "add-repos-job-failed",
    });
    mockPollWorkspaceJob.mockResolvedValueOnce({
      status: "failed",
      error: "repository not found",
    });
    const { onSuccess, onClose } = renderModal();

    fireEvent.change(screen.getByLabelText("Repository URL"), {
      target: { value: "https://github.com/org/missing" },
    });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Add Repository" }));
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });

    expect(screen.getByRole("alert")).toHaveTextContent("repository not found");
    expect(onSuccess).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
  });
});
