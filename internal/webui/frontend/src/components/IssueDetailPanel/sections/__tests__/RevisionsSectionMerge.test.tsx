/** @vitest-environment jsdom */

import "@testing-library/jest-dom";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { RevisionsSection } from "../RevisionsSection";

const {
  applyRevision,
  approveRevisionMerge,
  cancelRevisionMerge,
  createRevisionPR,
  getTaskRevisions,
  submitRevisionVerdict,
} = vi.hoisted(() => ({
  applyRevision: vi.fn(),
  approveRevisionMerge: vi.fn(),
  cancelRevisionMerge: vi.fn(),
  createRevisionPR: vi.fn(),
  getTaskRevisions: vi.fn(),
  submitRevisionVerdict: vi.fn(),
}));
vi.mock("@/api/git/revisions", () => ({
  applyRevision,
  approveRevisionMerge,
  cancelRevisionMerge,
  createRevisionPR,
  getTaskRevisions,
  submitRevisionVerdict,
}));

const head = "a".repeat(40);
const open = {
  change_id: "C",
  number: 2,
  head_sha: head,
  outcome: "completed",
  incomplete: false,
  applied: true,
  needs_working_area: false,
  verdict: "approve",
  pr_number: 3,
  pr_url: "https://github.test/o/r/pull/3",
  pr_head: head,
};

describe("RevisionsSection Approve and merge (D29)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    approveRevisionMerge.mockResolvedValue(undefined);
    cancelRevisionMerge.mockResolvedValue(undefined);
  });

  it("merges the bottom PR with Approve and merge and offers no Approve only", async () => {
    getTaskRevisions
      .mockResolvedValueOnce([open])
      .mockResolvedValueOnce([{ ...open, merge_status: "merging" }]);
    render(<RevisionsSection workspaceId="W" taskId="T" lead="lead-a" />);
    fireEvent.click(
      await screen.findByRole("button", { name: "Approve and merge" }),
    );
    await waitFor(() =>
      expect(approveRevisionMerge).toHaveBeenCalledWith("W", open, "lead-a"),
    );
    expect(await screen.findByText("Merging…")).toBeInTheDocument();
    expect(screen.queryByTestId("approve-menu-toggle")).toBeNull();
    expect(screen.queryByTestId("approve-merge")).toBeNull();
  });

  it("names the PR below and shows Merges after with Cancel auto-merge", async () => {
    const above = { ...open, merge_after: [1, 2] };
    getTaskRevisions
      .mockResolvedValueOnce([above])
      .mockResolvedValueOnce([
        {
          ...above,
          merge_status: "waiting",
          merge_reason: "merges after #1, #2",
        },
      ])
      .mockResolvedValueOnce([
        { ...above, merge_status: "cancelled", merge_reason: "x" },
      ]);
    render(<RevisionsSection workspaceId="W" taskId="T" lead="lead-a" />);
    fireEvent.click(
      await screen.findByRole("button", { name: "Approve, merge after #2" }),
    );
    expect(
      await screen.findByText("Approved, merges after #1, #2"),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Cancel auto-merge" }));
    await waitFor(() =>
      expect(cancelRevisionMerge).toHaveBeenCalledWith("W", "C"),
    );
    expect(await screen.findByText("Auto-merge cancelled")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Approve, merge after #2" }),
    ).toBeInTheDocument();
  });

  it("shows a blocked merge with its reason and lets it be cancelled", async () => {
    getTaskRevisions.mockResolvedValue([
      {
        ...open,
        merge_status: "blocked",
        merge_reason: "required checks are failing",
      },
    ]);
    render(<RevisionsSection workspaceId="W" taskId="T" lead="lead-a" />);
    expect(
      await screen.findByText(
        /merge blocked: required checks are failing\. Retries when it passes\./,
      ),
    ).toBeInTheDocument();
    expect(screen.getByTestId("cancel-auto-merge")).toBeEnabled();
    expect(screen.queryByTestId("approve-merge")).toBeNull();
  });

  it("asks again after a rebuild that was not clean, approving and merging in one click", async () => {
    const rebuilt = {
      ...open,
      number: 3,
      head_sha: "b".repeat(40),
      verdict: undefined,
      merge_status: "reapproval_required",
      merge_reason:
        "the rebuild after the PRs below merged was not clean; approve again",
    };
    getTaskRevisions.mockResolvedValue([rebuilt]);
    submitRevisionVerdict.mockResolvedValue("applied");
    render(<RevisionsSection workspaceId="W" taskId="T" lead="lead-a" />);
    expect(
      await screen.findByText(
        "Not merged: the rebuild after the PRs below merged was not clean; approve again",
      ),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Approve and merge" }));
    await waitFor(() =>
      expect(submitRevisionVerdict).toHaveBeenCalledWith(
        "W",
        rebuilt,
        "approve",
        "",
        "lead-a",
        false,
        true,
      ),
    );
    expect(approveRevisionMerge).not.toHaveBeenCalled();
  });

  it("offers no second merge of the version someone else pushed over", async () => {
    getTaskRevisions.mockResolvedValue([
      {
        ...open,
        merge_status: "stale_subject",
        merge_reason: "someone else pushed to the PR after it was approved",
      },
    ]);
    render(<RevisionsSection workspaceId="W" taskId="T" lead="lead-a" />);
    expect(
      await screen.findByText(/Not merged: someone else pushed/),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("approve-merge")).toBeNull();
  });

  it("reports someone else's push and offers no merge of a version it has not reviewed", async () => {
    getTaskRevisions.mockResolvedValue([
      {
        ...open,
        pr_head: "c".repeat(40),
        merge_status: "stale_subject",
        merge_reason: "someone else pushed to the PR after it was approved",
      },
    ]);
    render(<RevisionsSection workspaceId="W" taskId="T" lead="lead-a" />);
    expect(
      await screen.findByText(
        /Not merged: someone else pushed to the PR after it was approved/,
      ),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("approve-merge")).toBeNull();
    expect(screen.queryByTestId("cancel-auto-merge")).toBeNull();
  });
});
