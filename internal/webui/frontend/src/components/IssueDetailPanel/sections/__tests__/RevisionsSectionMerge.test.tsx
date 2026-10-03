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

  it("asks again after a rebuild that was not clean, offering merge only on the new version", async () => {
    const reason =
      "the rebuild after the PRs below merged was not clean; approve again";
    getTaskRevisions.mockResolvedValue([
      {
        ...open,
        number: 3,
        head_sha: "d".repeat(40),
        verdict: undefined,
        merge_status: "reapproval_required",
        merge_reason: reason,
      },
      {
        ...open,
        number: 2,
        verdict: "carried",
        merge_status: "reapproval_required",
        merge_reason: reason,
      },
    ]);
    render(<RevisionsSection workspaceId="W" taskId="T" lead="lead-a" />);
    await waitFor(() =>
      expect(screen.getAllByTestId("merge-status")).toHaveLength(2),
    );
    for (const status of screen.getAllByTestId("merge-status")) {
      expect(status).toHaveTextContent(`Not merged: ${reason}`);
    }
    // Only the rebuilt version, not yet reviewed, can be approved to merge.
    expect(screen.getAllByTestId("approve-merge")).toHaveLength(1);
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
  it("shows a merged PR as merged on every revision, with no open-PR actions", async () => {
    getTaskRevisions.mockResolvedValue([
      { ...open, number: 2, pr_state: "merged", merge_status: "merged" },
      {
        ...open,
        number: 1,
        head_sha: "b".repeat(40),
        verdict: "carried",
        pr_state: "merged",
        merge_status: "merged",
      },
    ]);
    render(<RevisionsSection workspaceId="W" taskId="T" lead="lead-a" />);
    await waitFor(() =>
      expect(screen.getAllByTestId("revision-pr")).toHaveLength(2),
    );
    for (const pr of screen.getAllByTestId("revision-pr")) {
      expect(pr).toHaveTextContent("PR #3 was merged");
      expect(pr).not.toHaveTextContent("is open");
    }
    expect(screen.queryByTestId("approve-merge")).not.toBeInTheDocument();
    expect(screen.queryByTestId("approve-create-pr")).not.toBeInTheDocument();
    expect(screen.queryByTestId("approve-menu-toggle")).not.toBeInTheDocument();
    expect(screen.queryByTestId("create-pr")).not.toBeInTheDocument();
    expect(screen.queryByTestId("cancel-auto-merge")).not.toBeInTheDocument();
  });

  it("shows a closed PR as closed and offers no merge or Cancel auto-merge", async () => {
    getTaskRevisions.mockResolvedValue([
      {
        ...open,
        verdict: undefined,
        pr_state: "closed",
        merge_status: "waiting",
        merge_reason: "merges after #1",
        merge_after: [1],
      },
    ]);
    render(<RevisionsSection workspaceId="W" taskId="T" lead="lead-a" />);
    expect(await screen.findByTestId("revision-pr")).toHaveTextContent(
      "PR #3 was closed",
    );
    expect(screen.queryByTestId("approve-merge")).not.toBeInTheDocument();
    expect(screen.queryByTestId("approve-create-pr")).not.toBeInTheDocument();
    expect(screen.queryByTestId("cancel-auto-merge")).not.toBeInTheDocument();
  });
});
