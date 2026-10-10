/** @vitest-environment jsdom */

import "@testing-library/jest-dom";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReviewRevision } from "@/hooks/api";
import { ApiError } from "@/types";
import { ReviewBar } from "../ReviewBar";

const { submitRevisionVerdict } = vi.hoisted(() => ({
  submitRevisionVerdict: vi.fn(),
}));
vi.mock("@/api/git/revisions", () => ({
  applyRevision: vi.fn(),
  approveRevisionMerge: vi.fn(),
  cancelRevisionMerge: vi.fn(),
  getTaskRevisions: vi.fn(),
  submitRevisionVerdict,
}));

const revision: ReviewRevision = {
  change_id: "C",
  repo: "repo",
  number: 1,
  head_sha: "f".repeat(40),
  outcome: "completed",
  incomplete: false,
  applied: false,
  needs_working_area: false,
  superseded: false,
  no_changes: false,
};
const held: ReviewRevision = {
  ...revision,
  verdict: "approve",
  follow_status: "apply_pending",
  publish_status: "pending",
};
// The 409 the server sends when the approval is recorded but its apply is held.
const heldError = new ApiError(409, "Conflict", {
  success: false,
  error: "apply_pending",
  status: "recorded",
  paths: ["held.txt"],
  message:
    "Approved, not applied yet: the lead's working area has unsaved edits to the same files (held.txt). No PR until it applies.",
});

const diffs = (n: number) => [
  {
    revision: n,
    change: "C",
    repo: "repo",
    compare: "layer" as const,
    files: [],
  },
];

describe("ReviewBar held approval (P2.21)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    submitRevisionVerdict.mockRejectedValue(heldError);
  });

  it("replaces the buttons at once, before the reload arrives, and names the paths", async () => {
    render(
      <ReviewBar
        workspaceId="W"
        taskId="T"
        lead="lead"
        current={[revision]}
        diffs={diffs(1)}
        onChanged={() => {}}
      />,
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Approve code & create PR" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("held.txt");
    expect(screen.getByTestId("review-status")).toHaveTextContent(/^Approved/);
    expect(
      screen.queryByRole("button", { name: "Approve code & create PR" }),
    ).toBeNull();
    expect(screen.queryByRole("button", { name: "Reject" })).toBeNull();
  });

  it("shows the held reason from the server after the reload", async () => {
    function Caller() {
      const [current, setCurrent] = useState([revision]);
      return (
        <ReviewBar
          workspaceId="W"
          taskId="T"
          lead="lead"
          current={current}
          diffs={diffs(1)}
          onChanged={() => setCurrent([held])}
        />
      );
    }
    render(<Caller />);
    fireEvent.click(
      screen.getByRole("button", { name: "Approve code & create PR" }),
    );
    await waitFor(() =>
      expect(screen.getByTestId("review-status")).toHaveTextContent(
        "Not applied yet: the lead's working area has unsaved edits to the same files",
      ),
    );
    expect(screen.queryByTestId("review-next-action")).toBeNull();
  });

  it("offers the buttons again for a new derived revision", () => {
    const next = { ...revision, number: 2, head_sha: "e".repeat(40) };
    render(
      <ReviewBar
        workspaceId="W"
        taskId="T"
        lead="lead"
        current={[next]}
        diffs={diffs(2)}
        onChanged={() => {}}
      />,
    );
    expect(
      screen.getByRole("button", { name: "Approve code & create PR" }),
    ).toBeEnabled();
    expect(screen.queryByTestId("review-status")).toBeNull();
  });
});
