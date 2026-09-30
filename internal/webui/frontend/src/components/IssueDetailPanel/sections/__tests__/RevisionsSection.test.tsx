/** @vitest-environment jsdom */

import "@testing-library/jest-dom";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { RevisionsSection } from "../RevisionsSection";

const { getTaskRevisions, submitRevisionVerdict } = vi.hoisted(
  () => ({
    getTaskRevisions: vi.fn(),
    submitRevisionVerdict: vi.fn(),
  }),
);
vi.mock("@/api/git/revisions", () => ({
  getTaskRevisions,
  submitRevisionVerdict,
}));

const revision = {
  change_id: "C",
  number: 2,
  head_sha: "a".repeat(40),
  outcome: "completed",
  incomplete: false,
};

describe("RevisionsSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    getTaskRevisions.mockResolvedValue([revision]);
    submitRevisionVerdict.mockResolvedValue(undefined);
  });

  it("records approval for the displayed revision and exact head", async () => {
    render(<RevisionsSection workspaceId="W" taskId="T" />);
    fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
    await waitFor(() =>
      expect(submitRevisionVerdict).toHaveBeenCalledWith(
        "W",
        revision,
        "approve",
        "",
      ),
    );
  });

  it("requires a reason before recording a human override", async () => {
    render(<RevisionsSection workspaceId="W" taskId="T" />);
    fireEvent.click(await screen.findByRole("button", { name: "Override" }));
    expect(
      screen.getByRole("button", { name: "Record override" }),
    ).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Override reason"), {
      target: { value: "review exception" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Record override" }));
    await waitFor(() =>
      expect(submitRevisionVerdict).toHaveBeenCalledWith(
        "W",
        revision,
        "override",
        "review exception",
      ),
    );
  });

  it("records a rejection for the selected revision", async () => {
    render(<RevisionsSection workspaceId="W" taskId="T" />);
    fireEvent.click(await screen.findByRole("button", { name: "Reject" }));
    await waitFor(() =>
      expect(submitRevisionVerdict).toHaveBeenCalledWith(
        "W",
        revision,
        "reject",
        "",
      ),
    );
  });

  it("does not offer verdicts for an incomplete revision", async () => {
    getTaskRevisions.mockResolvedValue([{ ...revision, incomplete: true }]);
    render(<RevisionsSection workspaceId="W" taskId="T" />);
    expect(await screen.findByText("Incomplete capture")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Reject" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Override" })).toBeDisabled();
  });
});
