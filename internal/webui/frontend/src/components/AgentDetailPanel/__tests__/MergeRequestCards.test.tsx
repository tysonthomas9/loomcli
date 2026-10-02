/**
 * @vitest-environment jsdom
 */
import "@testing-library/jest-dom";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { MergeRequestCards } from "../MergeRequestCards";

const api = vi.hoisted(() => ({
  gitMergeRequests: vi.fn(),
  gitConfirmMergeRequest: vi.fn(),
}));

vi.mock("@/api/workspace/git", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/api/workspace/git")>()),
  ...api,
}));

const pending = {
  id: "R1",
  stack_id: "feature",
  target: "C",
  lead: "L",
  status: "pending",
  requested_kind: "lead",
  requested_by: "L",
  expires_at: "2026-10-01T12:30:00Z",
  layers: [
    {
      change: "A",
      head: "aaaa",
      pr_url: "https://github.test/pr/1",
      checks: "passing",
      review: "approved",
    },
    {
      change: "B",
      head: "bbbb",
      pr_url: "https://github.test/pr/2",
      checks: "failing",
      review: "review_required",
    },
  ],
};

describe("MergeRequestCards", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("lists each PR, head, checks and review, and confirms the request", async () => {
    api.gitMergeRequests.mockResolvedValue([pending]);
    api.gitConfirmMergeRequest.mockResolvedValue({ phase: "ready" });
    render(<MergeRequestCards workspaceId="W" agentName="L" />);
    expect(
      await screen.findByText(/lead L asks to merge feature up to C/),
    ).toBeInTheDocument();
    expect(screen.getByText("https://github.test/pr/2")).toBeInTheDocument();
    expect(screen.getByText("bbbb")).toBeInTheDocument();
    expect(screen.getByText("failing")).toBeInTheDocument();
    expect(screen.getByText("review_required")).toBeInTheDocument();
    expect(api.gitConfirmMergeRequest).not.toHaveBeenCalled();
    fireEvent.click(
      screen.getByRole("button", { name: "Confirm merge up to C" }),
    );
    await waitFor(() =>
      expect(api.gitConfirmMergeRequest).toHaveBeenCalledWith("W", "L", "R1"),
    );
  });

  it("shows why a stale request did not merge", async () => {
    api.gitMergeRequests.mockResolvedValue([pending]);
    api.gitConfirmMergeRequest.mockRejectedValue(
      new Error(
        "stale: stack head changed after the merge request; request the merge again",
      ),
    );
    render(<MergeRequestCards workspaceId="W" agentName="L" />);
    fireEvent.click(
      await screen.findByRole("button", { name: "Confirm merge up to C" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "request the merge again",
    );
  });

  it("renders nothing without pending requests", async () => {
    api.gitMergeRequests.mockResolvedValue([{ ...pending, status: "expired" }]);
    const { container } = render(
      <MergeRequestCards workspaceId="W" agentName="L" />,
    );
    await waitFor(() => expect(api.gitMergeRequests).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });
});
