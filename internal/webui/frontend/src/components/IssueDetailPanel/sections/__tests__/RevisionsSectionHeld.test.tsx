/** @vitest-environment jsdom */

import "@testing-library/jest-dom";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/types";
import { RevisionsSection } from "../RevisionsSection";

const { getTaskRevisions, submitRevisionVerdict } = vi.hoisted(() => ({
  getTaskRevisions: vi.fn(),
  submitRevisionVerdict: vi.fn(),
}));
vi.mock("@/api/git/revisions", () => ({
  applyRevision: vi.fn(),
  createRevisionPR: vi.fn(),
  getTaskRevisions,
  submitRevisionVerdict,
}));

const revision = {
  change_id: "C",
  repo: "repo",
  number: 1,
  head_sha: "f".repeat(40),
  outcome: "completed",
  incomplete: false,
  applied: false,
  needs_working_area: false,
};
const held = {
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

const verdictButtons = () =>
  ["Approve and create PR", "More approve options", "Reject", "Override"].map(
    (name) => screen.getByRole("button", { name }),
  );

describe("RevisionsSection held approval (P2.21)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows the recorded approval, names the paths and locks the verdicts", async () => {
    getTaskRevisions
      .mockResolvedValueOnce([revision])
      .mockResolvedValueOnce([held]);
    submitRevisionVerdict.mockRejectedValue(heldError);
    render(<RevisionsSection workspaceId="W" taskId="T" lead="lead" />);
    fireEvent.click(
      await screen.findByRole("button", { name: "Approve and create PR" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("held.txt");
    expect(screen.getByRole("alert")).not.toHaveTextContent(/^apply_pending$/);
    await waitFor(() =>
      expect(screen.queryByText("Awaiting review")).not.toBeInTheDocument(),
    );
    expect(screen.getByText("approve")).toBeInTheDocument();
    expect(screen.getByTestId("revision-follow-held")).toHaveTextContent(
      "unsaved edits to the same files",
    );
    for (const button of verdictButtons()) expect(button).toBeDisabled();
    expect(screen.queryByTestId("revision-pr")).not.toBeInTheDocument();
    expect(screen.queryByTestId("create-pr")).not.toBeInTheDocument();
  });

  it("reloads the caller's snapshot after a held approval", async () => {
    getTaskRevisions.mockResolvedValue([held]);
    submitRevisionVerdict.mockRejectedValue(heldError);
    function Caller() {
      const [revisions, setRevisions] = useState([revision]);
      return (
        <RevisionsSection
          workspaceId="W"
          taskId="T"
          lead="lead"
          revisions={revisions}
          verdictsFor={1}
          onChanged={() => {
            void getTaskRevisions("W", "T", "lead").then(setRevisions);
          }}
        />
      );
    }
    render(<Caller />);
    fireEvent.click(
      await screen.findByRole("button", { name: "Approve and create PR" }),
    );
    expect(await screen.findByText("approve")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("held.txt");
    for (const button of verdictButtons()) expect(button).toBeDisabled();
  });

  it("keeps the held approval locked after a reload", async () => {
    getTaskRevisions.mockResolvedValue([held]);
    render(<RevisionsSection workspaceId="W" taskId="T" lead="lead" />);
    expect(await screen.findByText("approve")).toBeInTheDocument();
    expect(screen.getByTestId("revision-follow-held")).toBeInTheDocument();
    for (const button of verdictButtons()) expect(button).toBeDisabled();
  });

  it("offers the verdicts again for a new derived revision", async () => {
    getTaskRevisions.mockResolvedValue([
      held,
      { ...revision, number: 2, head_sha: "e".repeat(40) },
    ]);
    render(<RevisionsSection workspaceId="W" taskId="T" lead="lead" />);
    expect(await screen.findByText("Awaiting review")).toBeInTheDocument();
    expect(
      screen.queryByTestId("revision-follow-held"),
    ).not.toBeInTheDocument();
    for (const button of verdictButtons()) expect(button).toBeEnabled();
  });
});
