/** @vitest-environment jsdom */

import "@testing-library/jest-dom";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/types";
import { TaskChangesTab } from "../TaskChangesTab";

const {
  applyRevision,
  getRevisionDiff,
  getTaskDiff,
  getTaskRevisions,
  submitRevisionVerdict,
} = vi.hoisted(() => ({
  applyRevision: vi.fn(),
  getRevisionDiff: vi.fn(),
  getTaskDiff: vi.fn(),
  getTaskRevisions: vi.fn(),
  submitRevisionVerdict: vi.fn(),
}));
vi.mock("@/api/git/revisions", () => ({
  applyRevision,
  getRevisionDiff,
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

describe("TaskChangesTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    getTaskRevisions.mockResolvedValue([rev2, rev1]);
    getTaskDiff.mockResolvedValue([taskDiff]);
    getRevisionDiff.mockResolvedValue({
      revision: 1,
      files: [
        { path: "f", patchSize: 40, truncated: false, patch: patch("old-try") },
      ],
    });
    submitRevisionVerdict.mockResolvedValue("recorded");
  });

  it("loads the revisions before the task diff and shows one diff", async () => {
    let releaseRevisions: (value: unknown) => void = () => {};
    getTaskRevisions.mockReturnValueOnce(
      new Promise((resolve) => {
        releaseRevisions = resolve;
      }),
    );
    render(<TaskChangesTab workspaceId="W" taskId="T" lead="lead" />);
    expect(screen.getByText("Loading revisions…")).toBeInTheDocument();
    expect(getTaskDiff).not.toHaveBeenCalled();
    releaseRevisions([rev2, rev1]);
    expect(
      await screen.findByText(
        "Revision 2 against the layer below it in the stack",
      ),
    ).toBeInTheDocument();
    expect(getTaskDiff).toHaveBeenCalledWith("W", "T", "lead");
    expect(getTaskDiff).toHaveBeenCalledTimes(1);
    expect(getRevisionDiff).not.toHaveBeenCalled();
    expect(screen.getByText("+task")).toBeInTheDocument();
    // The patch's trailing newline is not rendered as an empty line.
    expect(document.querySelectorAll('[data-type="context"]')).toHaveLength(0);
    const files = screen.getByRole("complementary", { name: "Changed files" });
    expect(within(files).getAllByRole("button")).toHaveLength(2);
    fireEvent.click(within(files).getByRole("button", { name: "big.bin" }));
    expect(
      screen.getByText(/3000000 bytes · too large to show/),
    ).toBeInTheDocument();
  });

  it("puts verdict buttons on the newest revision only", async () => {
    render(<TaskChangesTab workspaceId="W" taskId="T" lead="lead" />);
    fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
    expect(screen.getAllByRole("button", { name: "Approve" })).toHaveLength(1);
    await vi.waitFor(() =>
      expect(submitRevisionVerdict).toHaveBeenCalledWith(
        "W",
        rev2,
        "approve",
        "",
        "lead",
      ),
    );
  });

  it("reloads the task diff after a verdict applies the revision", async () => {
    getTaskDiff
      .mockResolvedValueOnce([{ ...taskDiff, compare: "base" }])
      .mockResolvedValue([{ ...taskDiff, compare: "trunk" }]);
    render(<TaskChangesTab workspaceId="W" taskId="T" />);
    expect(
      await screen.findByText("Revision 2 against its base (not applied yet)"),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    expect(
      await screen.findByText("Revision 2 against trunk"),
    ).toBeInTheDocument();
    expect(getTaskDiff).toHaveBeenCalledTimes(2);
  });

  it("lists older revisions read-only under History with their own diffs", async () => {
    render(<TaskChangesTab workspaceId="W" taskId="T" />);
    const history = await screen.findByRole("button", { name: "History (1)" });
    expect(screen.queryByRole("list", { name: "Revision history" })).toBeNull();
    fireEvent.click(history);
    const list = screen.getByRole("list", { name: "Revision history" });
    expect(within(list).getByText("aaaaaaaaaaaa")).toBeInTheDocument();
    expect(within(list).getByText(/completed/)).toBeInTheDocument();
    expect(
      within(list).getByText(/replaced by a newer revision/),
    ).toBeInTheDocument();
    expect(within(list).queryByRole("button", { name: "Approve" })).toBeNull();
    fireEvent.click(within(list).getByRole("button", { name: "Revision 1" }));
    expect(await screen.findByText("+old-try")).toBeInTheDocument();
    expect(getRevisionDiff).toHaveBeenCalledWith("W", rev1);
    expect(
      screen.getByText("Revision 1 (read-only history), against its base"),
    ).toBeInTheDocument();
    // Still one set of verdict buttons: the task's, never the old revision's.
    expect(screen.getAllByRole("button", { name: "Approve" })).toHaveLength(1);
    fireEvent.click(
      screen.getByRole("button", { name: "Back to the task diff" }),
    );
    expect(screen.getByText("+task")).toBeInTheDocument();
  });

  it("says No changes for an empty newest revision", async () => {
    getTaskRevisions.mockResolvedValue([rev2]);
    getTaskDiff.mockResolvedValue([
      { ...taskDiff, compare: "base", files: [] },
    ]);
    render(<TaskChangesTab workspaceId="W" taskId="T" />);
    expect(await screen.findByText("No changes")).toBeInTheDocument();
    expect(
      screen.getByText("Revision 2 against its base (not applied yet)"),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /History/ })).toBeNull();
  });

  it("closes an empty attempt as No changes with no verdict buttons", async () => {
    getTaskRevisions.mockResolvedValue([{ ...rev2, no_changes: true }]);
    getTaskDiff.mockResolvedValue([
      { ...taskDiff, compare: "base", files: [] },
    ]);
    render(<TaskChangesTab workspaceId="W" taskId="T" />);
    expect(
      await screen.findByTestId("revision-no-changes"),
    ).toBeInTheDocument();
    expect(await screen.findAllByText("No changes")).toHaveLength(2);
    expect(screen.queryByText("Awaiting review")).toBeNull();
    for (const name of ["Approve", "Reject", "Override"])
      expect(screen.queryByRole("button", { name })).toBeNull();
  });

  it("marks an empty earlier attempt in History and reviews the newer one", async () => {
    getTaskRevisions.mockResolvedValue([rev2, { ...rev1, no_changes: true }]);
    render(<TaskChangesTab workspaceId="W" taskId="T" />);
    fireEvent.click(await screen.findByRole("button", { name: "History (1)" }));
    const list = screen.getByRole("list", { name: "Revision history" });
    expect(within(list).getByText(/no changes/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve" })).toBeEnabled();
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

  it("does not ask for a diff when the task has no revisions", async () => {
    getTaskRevisions.mockResolvedValue([]);
    render(<TaskChangesTab workspaceId="W" taskId="T" />);
    expect(await screen.findByText("No revisions yet.")).toBeInTheDocument();
    expect(getTaskDiff).not.toHaveBeenCalled();
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

    it("shows one section per repo, ordered by repo name, each with its own diff and verdicts", async () => {
      // The list's order is not the repo order: zeta comes first.
      getTaskRevisions.mockResolvedValue([zeta, alpha]);
      getTaskDiff.mockResolvedValue([
        diffFor("A", "alpha", "alpha-code"),
        diffFor("Z", "zeta", "zeta-code"),
      ]);
      render(<TaskChangesTab workspaceId="W" taskId="T" lead="lead" />);
      const alphaSection = await screen.findByRole("region", {
        name: "Repo alpha",
      });
      const zetaSection = screen.getByRole("region", { name: "Repo zeta" });
      expect(
        alphaSection.compareDocumentPosition(zetaSection) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy();
      expect(
        await within(alphaSection).findByText("+alpha-code"),
      ).toBeVisible();
      expect(within(alphaSection).queryByText("+zeta-code")).toBeNull();
      expect(within(zetaSection).getByText("+zeta-code")).toBeVisible();
      expect(within(zetaSection).queryByText("+alpha-code")).toBeNull();
      // Each repo's own revision gets its own, enabled verdict buttons.
      const zetaApprove = await within(zetaSection).findByRole("button", {
        name: "Approve",
      });
      expect(
        within(alphaSection).getAllByRole("button", { name: "Approve" }),
      ).toHaveLength(1);
      await vi.waitFor(() => expect(zetaApprove).toBeEnabled());
      fireEvent.click(zetaApprove);
      await vi.waitFor(() =>
        expect(submitRevisionVerdict).toHaveBeenCalledWith(
          "W",
          zeta,
          "approve",
          "",
          "lead",
        ),
      );
    });

    it("keeps a repo's verdict buttons disabled until that repo's diff has loaded", async () => {
      getTaskRevisions.mockResolvedValue([zeta, alpha]);
      let release: (value: unknown) => void = () => {};
      getTaskDiff.mockReturnValue(
        new Promise((resolve) => {
          release = resolve;
        }),
      );
      render(<TaskChangesTab workspaceId="W" taskId="T" />);
      const zetaSection = await screen.findByRole("region", {
        name: "Repo zeta",
      });
      const approve = await within(zetaSection).findByRole("button", {
        name: "Approve",
      });
      expect(approve).toBeDisabled();
      expect(within(zetaSection).getByText("Loading diff…")).toBeVisible();
      release([
        diffFor("A", "alpha", "alpha-code"),
        diffFor("Z", "zeta", "zeta-code"),
      ]);
      await vi.waitFor(() => expect(approve).toBeEnabled());
    });

    it("keeps verdicts disabled when the diff shown is not the newest revision's", async () => {
      getTaskRevisions.mockResolvedValue([{ ...zeta, number: 2 }, alpha]);
      getTaskDiff.mockResolvedValue([
        diffFor("A", "alpha", "alpha-code"),
        diffFor("Z", "zeta", "zeta-code"),
      ]);
      render(<TaskChangesTab workspaceId="W" taskId="T" />);
      const zetaSection = await screen.findByRole("region", {
        name: "Repo zeta",
      });
      expect(await within(zetaSection).findByText("+zeta-code")).toBeVisible();
      expect(
        within(zetaSection).getByRole("button", { name: "Approve" }),
      ).toBeDisabled();
      const alphaSection = screen.getByRole("region", { name: "Repo alpha" });
      await vi.waitFor(() =>
        expect(
          within(alphaSection).getByRole("button", { name: "Approve" }),
        ).toBeEnabled(),
      );
    });
  });

  describe("verdicts only for the revision whose diff is shown", () => {
    const first = {
      ...base,
      number: 1,
      head_sha: "1".repeat(40),
      superseded: false,
    };
    const second = {
      ...base,
      number: 2,
      head_sha: "2".repeat(40),
      superseded: false,
    };
    const diffOf = (revision: number) => ({
      ...taskDiff,
      revision,
      files: [taskDiff.files[0]],
    });

    it("renders the verdicts from the tab's own revisions snapshot, not a newer fetch", async () => {
      // A second revisions fetch would already see revision 2, whose diff is
      // not on screen.
      getTaskRevisions
        .mockResolvedValueOnce([first])
        .mockResolvedValue([second, first]);
      getTaskDiff.mockResolvedValue([diffOf(1)]);
      render(<TaskChangesTab workspaceId="W" taskId="T" lead="lead" />);
      expect(
        await screen.findByText(
          "Revision 1 against the layer below it in the stack",
        ),
      ).toBeVisible();
      const approve = screen.getByRole("button", { name: "Approve" });
      expect(approve).toBeEnabled();
      expect(screen.queryByText("Revision 2")).toBeNull();
      expect(getTaskRevisions).toHaveBeenCalledTimes(1);
      fireEvent.click(approve);
      await vi.waitFor(() =>
        expect(submitRevisionVerdict).toHaveBeenCalledWith(
          "W",
          first,
          "approve",
          "",
          "lead",
        ),
      );
    });

    it("keeps a newer revision's verdicts disabled until its own diff has loaded", async () => {
      getTaskRevisions
        .mockResolvedValueOnce([first])
        .mockResolvedValue([second, first]);
      let releaseSecond: (value: unknown) => void = () => {};
      getTaskDiff.mockResolvedValueOnce([diffOf(1)]).mockReturnValueOnce(
        new Promise((resolve) => {
          releaseSecond = resolve;
        }),
      );
      render(<TaskChangesTab workspaceId="W" taskId="T" />);
      fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
      // The verdict reloads the snapshot: revision 2 appears while the diff on
      // screen is still revision 1's.
      expect(await screen.findByText("Revision 2")).toBeVisible();
      expect(
        screen.getByText("Revision 1 against the layer below it in the stack"),
      ).toBeVisible();
      expect(screen.getByRole("button", { name: "Approve" })).toBeDisabled();
      releaseSecond([diffOf(2)]);
      expect(
        await screen.findByText(
          "Revision 2 against the layer below it in the stack",
        ),
      ).toBeVisible();
      await vi.waitFor(() =>
        expect(screen.getByRole("button", { name: "Approve" })).toBeEnabled(),
      );
    });
  });
});
