/** @vitest-environment jsdom */

import "@testing-library/jest-dom";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/types";
import { RevisionsSection } from "../RevisionsSection";

const { applyRevision, getTaskRevisions, submitRevisionVerdict } = vi.hoisted(
  () => ({
    applyRevision: vi.fn(),
    getTaskRevisions: vi.fn(),
    submitRevisionVerdict: vi.fn(),
  }),
);
vi.mock("@/api/git/revisions", () => ({
  applyRevision,
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
    applyRevision.mockResolvedValue(undefined);
  });

  it("offers Apply when approval waits for the lead working area", async () => {
    submitRevisionVerdict.mockResolvedValue(
      "approved_waiting_for_working_area",
    );
    render(<RevisionsSection workspaceId="W" taskId="T" lead="lead-a" />);
    fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
    expect(
      await screen.findByText(
        "Approved: Apply to create the lead working area",
      ),
    ).toBeInTheDocument();
    expect(submitRevisionVerdict).toHaveBeenCalledWith(
      "W",
      revision,
      "approve",
      "",
      "lead-a",
    );
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(await screen.findByText("Applied")).toBeInTheDocument();
    expect(applyRevision).toHaveBeenCalledWith("W", revision, "lead-a");
    expect(
      screen.queryByText("Approved: Apply to create the lead working area"),
    ).not.toBeInTheDocument();
  });

  it("shows the missing-lead error and no Apply when the lead agent is absent", async () => {
    submitRevisionVerdict.mockResolvedValue(
      "approved_waiting_for_working_area",
    );
    applyRevision.mockRejectedValue(
      new ApiError(404, "Not Found", {
        error:
          'lead agent "lead-a" does not exist: create the lead agent first',
      }),
    );
    render(<RevisionsSection workspaceId="W" taskId="T" lead="lead-a" />);
    fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
    fireEvent.click(await screen.findByRole("button", { name: "Apply" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "create the lead agent first",
    );
    expect(
      screen.queryByRole("button", { name: "Apply" }),
    ).not.toBeInTheDocument();
  });

  it("does not Apply to a guessed lead when none is known", async () => {
    submitRevisionVerdict.mockResolvedValue(
      "approved_waiting_for_working_area",
    );
    render(<RevisionsSection workspaceId="W" taskId="T" />);
    fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
    expect(
      await screen.findByText("Approved: Apply needs a single workspace lead"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Apply" })).toBeDisabled();
    expect(applyRevision).not.toHaveBeenCalled();
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
        undefined,
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
        undefined,
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
        undefined,
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
