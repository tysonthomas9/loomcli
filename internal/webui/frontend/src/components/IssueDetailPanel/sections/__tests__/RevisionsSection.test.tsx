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
  applied: false,
  needs_working_area: false,
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
    getTaskRevisions
      .mockResolvedValueOnce([revision])
      .mockResolvedValueOnce([{ ...revision, verdict: "approve" }])
      .mockResolvedValueOnce([
        { ...revision, verdict: "approve", applied: true },
      ]);
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
    expect(applyRevision).toHaveBeenCalledWith(
      "W",
      expect.objectContaining({
        change_id: revision.change_id,
        number: revision.number,
        head_sha: revision.head_sha,
      }),
      "lead-a",
    );
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

  it("offers Apply after a reload for an approved revision waiting for a working area", async () => {
    getTaskRevisions.mockResolvedValue([
      { ...revision, verdict: "approve", needs_working_area: true },
    ]);
    render(<RevisionsSection workspaceId="W" taskId="T" lead="lead-a" />);
    expect(
      await screen.findByText(
        "Approved: Apply to create the lead working area",
      ),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() =>
      expect(applyRevision).toHaveBeenCalledWith(
        "W",
        expect.objectContaining({ change_id: "C", number: 2 }),
        "lead-a",
      ),
    );
  });

  it("shows applied and Apply for the displayed lead only", async () => {
    getTaskRevisions.mockImplementation(
      async (_ws: string, _task: string, lead?: string) => [
        lead === "lead-a"
          ? { ...revision, verdict: "approve", applied: true }
          : { ...revision, verdict: "approve", needs_working_area: true },
      ],
    );
    const viewB = render(
      <RevisionsSection workspaceId="W" taskId="T" lead="lead-b" />,
    );
    expect(
      await screen.findByText(
        "Approved: Apply to create the lead working area",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText("Applied")).not.toBeInTheDocument();
    expect(getTaskRevisions).toHaveBeenCalledWith("W", "T", "lead-b");
    viewB.unmount();

    render(<RevisionsSection workspaceId="W" taskId="T" lead="lead-a" />);
    expect(await screen.findByText("Applied")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Apply" }),
    ).not.toBeInTheDocument();
    expect(getTaskRevisions).toHaveBeenCalledWith("W", "T", "lead-a");
  });

  it("shows Applied from the server after a reload and clears it after unapply", async () => {
    getTaskRevisions.mockResolvedValue([
      { ...revision, verdict: "approve", applied: true },
    ]);
    const first = render(<RevisionsSection workspaceId="W" taskId="T" />);
    expect(await screen.findByText("Applied")).toBeInTheDocument();
    first.unmount();

    // `loom unapply` on the CLI, then a page reload: the server says not applied.
    getTaskRevisions.mockResolvedValue([{ ...revision, verdict: "approve" }]);
    render(<RevisionsSection workspaceId="W" taskId="T" />);
    expect(await screen.findByText("approve")).toBeInTheDocument();
    expect(screen.queryByText("Applied")).not.toBeInTheDocument();
  });

  it("locks verdict buttons once recorded and offers them for a new derived revision", async () => {
    getTaskRevisions
      .mockResolvedValueOnce([revision])
      .mockResolvedValueOnce([{ ...revision, verdict: "reject" }]);
    const view = render(<RevisionsSection workspaceId="W" taskId="T" />);
    fireEvent.click(await screen.findByRole("button", { name: "Reject" }));
    expect(await screen.findByText("reject")).toBeInTheDocument();
    for (const name of ["Approve", "Reject", "Override"])
      expect(screen.getByRole("button", { name })).toBeDisabled();
    view.unmount();

    getTaskRevisions.mockResolvedValue([
      { ...revision, verdict: "reject" },
      { ...revision, number: 3, head_sha: "b".repeat(40) },
    ]);
    render(<RevisionsSection workspaceId="W" taskId="T" />);
    expect(await screen.findByText("Awaiting review")).toBeInTheDocument();
    // Only the newest revision is reviewable; the older one moves to History.
    expect(screen.queryByText("Revision 2")).not.toBeInTheDocument();
    for (const name of ["Approve", "Reject", "Override"])
      expect(screen.getByRole("button", { name })).toBeEnabled();
  });

  it("does not offer verdicts for an incomplete revision", async () => {
    getTaskRevisions.mockResolvedValue([{ ...revision, incomplete: true }]);
    render(<RevisionsSection workspaceId="W" taskId="T" />);
    expect(await screen.findByText("Incomplete capture")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Reject" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Override" })).toBeDisabled();
  });

  it("shows No changes and no verdict buttons for an empty attempt", async () => {
    getTaskRevisions.mockResolvedValue([{ ...revision, no_changes: true }]);
    render(<RevisionsSection workspaceId="W" taskId="T" lead="lead-a" />);
    expect(await screen.findByText("No changes")).toBeInTheDocument();
    expect(screen.getByTestId("revision-no-changes")).toHaveTextContent(
      "Revision 2",
    );
    expect(screen.queryByText("Awaiting review")).not.toBeInTheDocument();
    for (const name of ["Approve", "Reject", "Override", "Apply"])
      expect(screen.queryByRole("button", { name })).not.toBeInTheDocument();
    expect(submitRevisionVerdict).not.toHaveBeenCalled();
  });

  it("reviews a later attempt with changes normally after an empty one", async () => {
    getTaskRevisions.mockResolvedValue([
      { ...revision, number: 3, head_sha: "b".repeat(40) },
      { ...revision, no_changes: true },
    ]);
    render(<RevisionsSection workspaceId="W" taskId="T" />);
    expect(await screen.findByText("Awaiting review")).toBeInTheDocument();
    expect(screen.queryByText("No changes")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() =>
      expect(submitRevisionVerdict).toHaveBeenCalledWith(
        "W",
        expect.objectContaining({ number: 3 }),
        "approve",
        "",
        undefined,
      ),
    );
  });
});
