/** @vitest-environment jsdom */

import "@testing-library/jest-dom";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ReviewRevision } from "@/hooks/api";
import { ReviewBar, feedbackText } from "../ReviewBar";

vi.mock("@/api/git/revisions", () => ({
  applyRevision: vi.fn(),
  approveRevisionMerge: vi.fn(),
  cancelRevisionMerge: vi.fn(),
  getTaskRevisions: vi.fn(),
  submitRevisionVerdict: vi.fn(),
}));

const head = "b".repeat(40);
const fixup: ReviewRevision = {
  change_id: "C",
  repo: "repo",
  number: 2,
  head_sha: head,
  outcome: "completed",
  incomplete: false,
  applied: false,
  needs_working_area: false,
  superseded: false,
  no_changes: false,
  verdict: "feedback",
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
        { revision: 2, change: "C", repo: "repo", compare: "layer", files: [] },
      ]}
      onChanged={() => {}}
    />,
  );
}

describe("ReviewBar review fix-ups (D29 (6))", () => {
  it("says a pushed fix-up went to the PR with no Approve needed", () => {
    bar({ ...fixup, feedback_status: "pushed", pr_head: "c".repeat(40) });
    expect(screen.getByTestId("review-status-detail")).toHaveTextContent(
      "Pushed to PR #3 automatically",
    );
    expect(screen.queryByTestId("approve-create-pr")).toBeNull();
  });

  it("shows the cancelled merge-after of a held fix-up", () => {
    bar({
      ...fixup,
      pr_head: "c".repeat(40),
      feedback_status: "held",
      feedback_reason: "it conflicts with the stack; the lead was told (a.txt)",
      feedback_merge_cancelled: true,
      merge_status: "cancelled",
    });
    expect(screen.getByTestId("review-status-detail")).toHaveTextContent(
      "Auto-merge cancelled because the code changed. Approve again to merge.",
    );
    // The held version is not on the PR, so it offers no merge of it.
    expect(screen.queryByTestId("approve-merge")).toBeNull();
  });

  it("offers Approve code & merge again once the pushed fix-up is the PR head", () => {
    bar({
      ...fixup,
      applied: true,
      feedback_status: "pushed",
      feedback_merge_cancelled: true,
      merge_status: "cancelled",
    });
    expect(
      screen.getByRole("button", { name: "Approve code & merge" }),
    ).toBeEnabled();
  });

  it("words each state", () => {
    const text = (status: string, reason = "") =>
      feedbackText({
        ...fixup,
        feedback_status: status as never,
        feedback_reason: reason,
      });
    expect(text("pushing")).toBe(
      "Fixing review comments: pushing to PR #3 automatically",
    );
    expect(text("held", "x")).toBe("Held, not pushed to PR #3: x");
    expect(text("not_pushed", "it adds the secret-pattern path .env")).toBe(
      "Not pushed to PR #3: it adds the secret-pattern path .env",
    );
    expect(text("superseded")).toBe("Replaced by a newer fix-up");
  });
});
