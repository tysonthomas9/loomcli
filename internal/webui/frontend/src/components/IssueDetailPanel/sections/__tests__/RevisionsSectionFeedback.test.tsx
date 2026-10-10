/** @vitest-environment jsdom */

import "@testing-library/jest-dom";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { RevisionsSection, feedbackText } from "../RevisionsSection";

const { getTaskRevisions } = vi.hoisted(() => ({
  getTaskRevisions: vi.fn(),
}));
vi.mock("@/api/git/revisions", () => ({
  applyRevision: vi.fn(),
  approveRevisionMerge: vi.fn(),
  cancelRevisionMerge: vi.fn(),
  createRevisionPR: vi.fn(),
  getTaskRevisions,
  submitRevisionVerdict: vi.fn(),
}));

const head = "b".repeat(40);
const fixup = {
  change_id: "C",
  number: 2,
  head_sha: head,
  outcome: "completed",
  incomplete: false,
  applied: false,
  needs_working_area: false,
  verdict: "feedback",
  pr_number: 3,
  pr_url: "https://github.test/o/r/pull/3",
  pr_head: head,
};

describe("RevisionsSection review fix-ups (D29 (6))", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("says a pushed fix-up went to the PR with no Approve and offers no Approve and create PR", async () => {
    getTaskRevisions.mockResolvedValueOnce([
      { ...fixup, feedback_status: "pushed" },
    ]);
    render(<RevisionsSection workspaceId="W" taskId="T" lead="lead-a" />);
    expect(await screen.findByTestId("feedback-status")).toHaveTextContent(
      "Pushed to PR #3 automatically",
    );
    expect(
      screen.getByText("Review fix-up (no Approve needed)"),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("approve-create-pr")).toBeNull();
    expect(screen.queryByTestId("approve-menu-toggle")).toBeNull();
  });

  it("shows a held fix-up and why, and the cancelled merge-after", async () => {
    getTaskRevisions.mockResolvedValueOnce([
      {
        ...fixup,
        pr_head: "c".repeat(40),
        feedback_status: "held",
        feedback_reason:
          "it conflicts with the stack; the lead was told (a.txt)",
        feedback_merge_cancelled: true,
        merge_status: "cancelled",
      },
    ]);
    render(<RevisionsSection workspaceId="W" taskId="T" lead="lead-a" />);
    expect(await screen.findByTestId("feedback-status")).toHaveTextContent(
      "Held, not pushed to PR #3: it conflicts with the stack; the lead was told (a.txt)",
    );
    expect(screen.getByTestId("feedback-merge-cancelled")).toHaveTextContent(
      "Auto-merge cancelled because the code changed. Approve again to merge.",
    );
    // The held version is not on the PR, so it offers no merge of it.
    expect(screen.queryByTestId("approve-merge")).toBeNull();
  });

  it("offers Approve and merge again once the pushed fix-up is the PR head", async () => {
    getTaskRevisions.mockResolvedValueOnce([
      {
        ...fixup,
        applied: true,
        feedback_status: "pushed",
        feedback_merge_cancelled: true,
        merge_status: "cancelled",
      },
    ]);
    render(<RevisionsSection workspaceId="W" taskId="T" lead="lead-a" />);
    expect(
      await screen.findByRole("button", { name: "Approve and merge" }),
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
    expect(text("not_pushed", "it adds the secret-pattern path .env")).toBe(
      "Not pushed to PR #3: it adds the secret-pattern path .env",
    );
    expect(text("superseded")).toBe("Replaced by a newer fix-up");
  });
});
