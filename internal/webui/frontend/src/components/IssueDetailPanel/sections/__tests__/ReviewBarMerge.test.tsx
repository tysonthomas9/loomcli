/** @vitest-environment jsdom */

import "@testing-library/jest-dom";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReviewRevision } from "@/hooks/api";
import { ReviewBar } from "../ReviewBar";

const { approveRevisionMerge, cancelRevisionMerge, submitRevisionVerdict } =
  vi.hoisted(() => ({
    approveRevisionMerge: vi.fn(),
    cancelRevisionMerge: vi.fn(),
    submitRevisionVerdict: vi.fn(),
  }));
vi.mock("@/api/git/revisions", () => ({
  applyRevision: vi.fn(),
  approveRevisionMerge,
  cancelRevisionMerge,
  getTaskRevisions: vi.fn(),
  submitRevisionVerdict,
}));

const head = "a".repeat(40);
const open: ReviewRevision = {
  change_id: "C",
  repo: "repo",
  number: 2,
  head_sha: head,
  outcome: "completed",
  incomplete: false,
  applied: true,
  needs_working_area: false,
  superseded: false,
  no_changes: false,
  verdict: "approve",
  pr_number: 3,
  pr_url: "https://github.test/o/r/pull/3",
  pr_head: head,
};

function bar(r: ReviewRevision) {
  return render(
    <ReviewBar
      workspaceId="W"
      taskId="T"
      lead="lead-a"
      current={[r]}
      diffs={[
        {
          revision: r.number,
          change: "C",
          repo: "repo",
          compare: "layer",
          files: [],
        },
      ]}
      onChanged={() => {}}
    />,
  );
}

describe("ReviewBar Approve and merge (D29)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    approveRevisionMerge.mockResolvedValue(undefined);
    cancelRevisionMerge.mockResolvedValue(undefined);
    submitRevisionVerdict.mockResolvedValue("applied");
  });

  it("an approved bottom PR's next step is Approve code & merge", async () => {
    bar(open);
    expect(screen.getByTestId("review-status")).toHaveTextContent(
      "✅ Approved · Applied to lead · PR #3 open",
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Approve code & merge" }),
    );
    await waitFor(() =>
      expect(approveRevisionMerge).toHaveBeenCalledWith("W", open, "lead-a"),
    );
    expect(screen.queryByTestId("approve-create-pr")).not.toBeInTheDocument();
  });

  it("names the PR below, then shows merges after with Cancel auto-merge", async () => {
    const above = { ...open, merge_after: [1, 2] };
    const { unmount } = bar(above);
    expect(
      screen.getByRole("button", { name: "Approve code, merge after #2" }),
    ).toBeEnabled();
    unmount();
    bar({
      ...above,
      merge_status: "waiting",
      merge_reason: "merges after #1, #2",
    });
    expect(screen.getByTestId("review-status")).toHaveTextContent(
      "PR #3 open · merges after #1, #2",
    );
    fireEvent.click(screen.getByRole("button", { name: "Cancel auto-merge" }));
    await waitFor(() =>
      expect(cancelRevisionMerge).toHaveBeenCalledWith("W", "C"),
    );
  });

  it("shows a blocked merge with its reason and lets it be cancelled", () => {
    bar({
      ...open,
      merge_status: "blocked",
      merge_reason: "required checks are failing",
    });
    expect(screen.getByTestId("review-status")).toHaveTextContent(
      "merge blocked: required checks are failing. Retries when it passes.",
    );
    expect(screen.getByTestId("cancel-auto-merge")).toBeEnabled();
    expect(screen.queryByTestId("approve-merge")).toBeNull();
  });

  it("asks again after a rebuild that was not clean, approving and merging in one click", async () => {
    const rebuilt = {
      ...open,
      number: 3,
      head_sha: "b".repeat(40),
      merge_status: "reapproval_required",
      merge_reason:
        "the rebuild after the PRs below merged was not clean; approve again",
    };
    delete (rebuilt as Partial<ReviewRevision>).verdict;
    bar(rebuilt);
    expect(screen.getByTestId("merge-status")).toHaveTextContent(
      "PR #3 open · not merged: the rebuild after the PRs below merged was not clean; approve again",
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Approve code & merge" }),
    );
    await waitFor(() =>
      expect(submitRevisionVerdict).toHaveBeenCalledWith(
        "W",
        rebuilt,
        "approve",
        "",
        "lead-a",
        true,
      ),
    );
    expect(approveRevisionMerge).not.toHaveBeenCalled();
  });

  it("offers no merge of a version someone else pushed over", () => {
    bar({
      ...open,
      pr_head: "c".repeat(40),
      merge_status: "stale_subject",
      merge_reason: "someone else pushed to the PR after it was approved",
    });
    expect(screen.getByTestId("review-status")).toHaveTextContent(
      "not merged: someone else pushed to the PR after it was approved",
    );
    expect(screen.queryByTestId("approve-merge")).toBeNull();
    expect(screen.queryByTestId("cancel-auto-merge")).toBeNull();
  });

  it("shows a merged PR as merged, with no open-PR actions", () => {
    bar({ ...open, pr_state: "merged", merge_status: "merged" });
    expect(screen.getByTestId("review-status")).toHaveTextContent(
      "✅ Merged · PR #3",
    );
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("shows a closed PR as closed and offers nothing to approve", () => {
    const closed = { ...open, pr_state: "closed", merge_after: [1] };
    delete (closed as Partial<ReviewRevision>).verdict;
    bar(closed);
    expect(screen.getByTestId("review-status")).toHaveTextContent(
      "PR #3 was closed",
    );
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
});
