/** @vitest-environment jsdom */

import "@testing-library/jest-dom";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/types";
import { TaskChangesTab } from "../TaskChangesTab";

const { getTaskDiff, getTaskRevisions, submitRevisionVerdict } = vi.hoisted(
  () => ({
    getTaskDiff: vi.fn(),
    getTaskRevisions: vi.fn(),
    submitRevisionVerdict: vi.fn(),
  }),
);
vi.mock("@/api/git/revisions", () => ({
  applyRevision: vi.fn(),
  approveRevisionMerge: vi.fn(),
  cancelRevisionMerge: vi.fn(),
  getTaskDiff,
  getTaskRevisions,
  submitRevisionVerdict,
}));

const base = {
  change_id: "C",
  repo: "source-repo",
  outcome: "completed",
  incomplete: false,
  applied: false,
  needs_working_area: false,
  no_changes: false,
  author: "coder",
};
const rev2 = {
  ...base,
  number: 2,
  head_sha: "b".repeat(40),
  superseded: false,
};
const rev1 = {
  ...base,
  number: 1,
  head_sha: "a".repeat(40),
  superseded: true,
  verdict: "reject",
  verdict_reason: "write it to the helper instead",
  date: "2026-10-02T23:14:24Z",
};
const patch = (line: string) =>
  `diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1 +1 @@\n-old\n+${line}\n`;
const taskDiff = {
  revision: 2,
  change: "C",
  repo: "source-repo",
  compare: "layer",
  files: [
    { path: "f", patchSize: 40, truncated: false, patch: patch("task") },
    { path: "big.bin", patchSize: 3_000_000, truncated: true },
  ],
};
const approve = () =>
  screen.getByRole("button", { name: "Approve code & create PR" });

describe("TaskChangesTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    getTaskRevisions.mockResolvedValue([rev2, rev1]);
    getTaskDiff.mockResolvedValue([taskDiff]);
    submitRevisionVerdict.mockResolvedValue("recorded");
  });

  it("loads the revisions before the task diff and shows the bar above one diff", async () => {
    let releaseRevisions: (value: unknown) => void = () => {};
    getTaskRevisions.mockReturnValueOnce(
      new Promise((resolve) => {
        releaseRevisions = resolve;
      }),
    );
    render(<TaskChangesTab workspaceId="W" taskId="T" lead="lead" />);
    expect(screen.getByText("Loading changes…")).toBeInTheDocument();
    expect(getTaskDiff).not.toHaveBeenCalled();
    releaseRevisions([rev2, rev1]);
    expect(await screen.findByText("+task")).toBeInTheDocument();
    expect(getTaskDiff).toHaveBeenCalledWith("W", "T", "lead");
    expect(getTaskDiff).toHaveBeenCalledTimes(1);
    // The trailing newline is not rendered as an empty line.
    expect(document.querySelectorAll('[data-type="context"]')).toHaveLength(0);
    const files = screen.getByRole("complementary", { name: "Changed files" });
    expect(within(files).getAllByRole("button")).toHaveLength(2);
    fireEvent.click(within(files).getByRole("button", { name: "big.bin" }));
    expect(
      screen.getByText(/3000000 bytes · too large to show/),
    ).toBeInTheDocument();
    // The bar comes before the diff.
    const bar = screen.getByTestId("revisions-section");
    expect(
      bar.compareDocumentPosition(files) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("has no attempts list, revision number or SHA (P2.23)", async () => {
    render(<TaskChangesTab workspaceId="W" taskId="T" lead="lead" />);
    await screen.findByText("+task");
    const tab = screen.getByTestId("task-changes-tab");
    expect(tab).not.toHaveTextContent(
      /History|Earlier attempts|Revision \d|bbbbbbbbbbbb/,
    );
    expect(screen.queryByRole("button", { name: /History/ })).toBeNull();
    // S7: no "Since your last review" toggle.
    expect(tab).not.toHaveTextContent("Since your last review");
  });

  it("shows the rejection reason above the full diff of the rerun", async () => {
    render(<TaskChangesTab workspaceId="W" taskId="T" lead="lead" />);
    expect(await screen.findByTestId("rejection-reason")).toHaveTextContent(
      "Retried after you rejected: write it to the helper instead",
    );
  });

  it("drops the rejection reason once the rerun is decided", async () => {
    getTaskRevisions.mockResolvedValue([{ ...rev2, verdict: "approve" }, rev1]);
    render(<TaskChangesTab workspaceId="W" taskId="T" lead="lead" />);
    await screen.findByText("+task");
    expect(screen.queryByTestId("rejection-reason")).toBeNull();
  });

  it("decides the newest revision only, and reloads the diff after", async () => {
    render(<TaskChangesTab workspaceId="W" taskId="T" lead="lead" />);
    await screen.findByText("+task");
    expect(
      screen.getAllByRole("button", { name: "Approve code & create PR" }),
    ).toHaveLength(1);
    fireEvent.click(approve());
    await vi.waitFor(() =>
      expect(submitRevisionVerdict).toHaveBeenCalledWith(
        "W",
        rev2,
        "approve",
        "",
        "lead",
      ),
    );
    await vi.waitFor(() => expect(getTaskDiff).toHaveBeenCalledTimes(2));
  });

  it("closes an empty attempt as No changes with no buttons", async () => {
    getTaskRevisions.mockResolvedValue([{ ...rev2, no_changes: true }]);
    getTaskDiff.mockResolvedValue([{ ...taskDiff, files: [] }]);
    render(<TaskChangesTab workspaceId="W" taskId="T" />);
    expect(await screen.findByTestId("revision-no-changes")).toHaveTextContent(
      "No changes",
    );
    expect(screen.queryByRole("button", { name: /Approve/ })).toBeNull();
  });

  it("shows the API error code when the diff fails", async () => {
    getTaskDiff.mockRejectedValue(
      new ApiError(409, "Conflict", { error: "base_ref_unresolvable" }),
    );
    render(<TaskChangesTab workspaceId="W" taskId="T" />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Could not load changes: base_ref_unresolvable",
    );
  });

  it("does not ask for a diff when the task has no code yet", async () => {
    getTaskRevisions.mockResolvedValue([]);
    render(<TaskChangesTab workspaceId="W" taskId="T" />);
    expect(await screen.findByText("No code changes yet.")).toBeInTheDocument();
    expect(getTaskDiff).not.toHaveBeenCalled();
  });

  it("keeps a newer revision's buttons disabled until its own diff has loaded", async () => {
    const first = { ...rev2, number: 1, verdict: undefined };
    const diffOf = (revision: number) => ({
      ...taskDiff,
      revision,
      files: [taskDiff.files[0]],
    });
    getTaskRevisions
      .mockResolvedValueOnce([first])
      .mockResolvedValue([rev2, first]);
    let releaseSecond: (value: unknown) => void = () => {};
    getTaskDiff.mockResolvedValueOnce([diffOf(1)]).mockReturnValueOnce(
      new Promise((resolve) => {
        releaseSecond = resolve;
      }),
    );
    render(<TaskChangesTab workspaceId="W" taskId="T" />);
    await screen.findByText("+task");
    fireEvent.click(approve());
    // The reload shows revision 2 while the diff on screen is still revision 1's.
    await vi.waitFor(() => expect(getTaskRevisions).toHaveBeenCalledTimes(2));
    await vi.waitFor(() => expect(approve()).toBeDisabled());
    releaseSecond([diffOf(2)]);
    await vi.waitFor(() => expect(approve()).toBeEnabled());
  });

  describe("a task with code in two repos", () => {
    const zeta = {
      ...base,
      change_id: "Z",
      repo: "zeta",
      number: 1,
      head_sha: "z".repeat(40),
      superseded: false,
    };
    const alpha = {
      ...base,
      change_id: "A",
      repo: "alpha",
      number: 1,
      head_sha: "c".repeat(40),
      superseded: false,
    };
    const diffFor = (change: string, repo: string, line: string) => ({
      revision: 1,
      change,
      repo,
      compare: "base",
      files: [
        {
          path: `${repo}.txt`,
          patchSize: 40,
          truncated: false,
          patch: patch(line),
        },
      ],
    });

    it("shows one diff section per repo, ordered by repo name, under one review bar", async () => {
      getTaskRevisions.mockResolvedValue([zeta, alpha]);
      getTaskDiff.mockResolvedValue([
        diffFor("A", "alpha", "a"),
        diffFor("Z", "zeta", "z"),
      ]);
      render(<TaskChangesTab workspaceId="W" taskId="T" lead="lead" />);
      await screen.findByText("+a");
      expect(screen.getAllByTestId("revisions-section")).toHaveLength(1);
      expect(
        screen.getAllByRole("button", { name: "Approve code & create PR" }),
      ).toHaveLength(1);
      const sections = screen.getAllByTestId("task-repo-changes");
      expect(sections.map((s) => s.getAttribute("aria-label"))).toEqual([
        "Repo alpha",
        "Repo zeta",
      ]);
      expect(within(sections[1]!).getByText("+z")).toBeInTheDocument();
    });

    it("enables the buttons only once every repo's diff has loaded", async () => {
      getTaskRevisions.mockResolvedValue([zeta, alpha]);
      getTaskDiff.mockResolvedValue([diffFor("A", "alpha", "a")]);
      render(<TaskChangesTab workspaceId="W" taskId="T" lead="lead" />);
      await screen.findByText("+a");
      expect(
        screen.getByText("No diff for this repo yet."),
      ).toBeInTheDocument();
      expect(approve()).toBeDisabled();
    });
  });
});
