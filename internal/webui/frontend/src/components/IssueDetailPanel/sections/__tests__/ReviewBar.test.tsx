/** @vitest-environment jsdom */

import "@testing-library/jest-dom";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReviewRevision, TaskDiff } from "@/hooks/api";
import { ApiError } from "@/types";
import {
  ReviewBar,
  ReviewStatusLink,
  reviewSummary,
  statusLine,
} from "../ReviewBar";

const {
  applyRevision,
  approveRevisionMerge,
  approveTask,
  cancelRevisionMerge,
  getTaskRevisions,
  submitRevisionVerdict,
  updateIssue,
} = vi.hoisted(() => ({
  applyRevision: vi.fn(),
  approveTask: vi.fn(),
  approveRevisionMerge: vi.fn(),
  cancelRevisionMerge: vi.fn(),
  getTaskRevisions: vi.fn(),
  submitRevisionVerdict: vi.fn(),
  updateIssue: vi.fn(),
}));
vi.mock("@/api/git/revisions", () => ({
  applyRevision,
  approveRevisionMerge,
  approveTask,
  cancelRevisionMerge,
  getTaskRevisions,
  submitRevisionVerdict,
}));
vi.mock("@/api/issues/issues", async (original) => ({
  ...(await original<typeof import("@/api/issues/issues")>()),
  updateIssue,
}));

const revision: ReviewRevision = {
  change_id: "C",
  repo: "source-repo",
  number: 2,
  head_sha: "a23a6acac6a1".padEnd(40, "0"),
  outcome: "completed",
  incomplete: false,
  applied: false,
  needs_working_area: false,
  superseded: false,
  no_changes: false,
  author: "coder",
  date: "2026-10-09T23:14:24Z",
};
const patch =
  "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1 +1,2 @@\n-old\n+new\n+more\n";
const diffFor = (r: ReviewRevision): TaskDiff => ({
  revision: r.number,
  change: r.change_id,
  repo: r.repo,
  compare: "layer",
  files: [{ path: "f", patchSize: patch.length, truncated: false, patch }],
});

function renderBar(
  current: ReviewRevision[],
  {
    diffs = current.map(diffFor),
    taskStatus = "review",
    lead = "lead",
    onChanged = vi.fn(),
  }: {
    diffs?: TaskDiff[] | null;
    taskStatus?: string;
    lead?: string;
    onChanged?: () => void;
  } = {},
) {
  return render(
    <ReviewBar
      workspaceId="W"
      taskId="T"
      lead={lead}
      taskStatus={taskStatus}
      current={current}
      diffs={diffs}
      onChanged={onChanged}
    />,
  );
}

const primary = () =>
  screen.getByRole("button", { name: "Approve code & create PR" });

describe("ReviewBar awaiting review", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    submitRevisionVerdict.mockResolvedValue("recorded");
  });

  it("shows a header without revision jargon and one primary, Reject and ⋯", () => {
    renderBar([revision]);
    const header = screen.getByTestId("review-header");
    expect(header).toHaveTextContent(
      /^Code changes · by coder · 1 file, \+2 −1 · /,
    );
    // The SHA is only in the tooltip.
    expect(header).toHaveAttribute("title", `commit ${revision.head_sha}`);
    const bar = screen.getByTestId("revisions-section");
    expect(bar).not.toHaveTextContent(/Revision|a23a6acac6a1/);
    expect(screen.getByTestId("review-awaiting")).toHaveTextContent(
      "Code awaiting review",
    );
    expect(primary()).toBeEnabled();
    expect(screen.getByRole("button", { name: "Reject" })).toBeEnabled();
    expect(
      screen.getByRole("button", { name: "More review options" }),
    ).toBeEnabled();
    // D40: one Approve code action; no Approve only / without PR, no Create PR.
    expect(bar).not.toHaveTextContent(/Approve only|without PR|Create PR/);
    fireEvent.click(
      screen.getByRole("button", { name: "More review options" }),
    );
    expect(
      screen.getAllByRole("menuitem").map((item) => item.textContent),
    ).toEqual(["Override (needs a reason)"]);
  });

  it("approves the revision on screen at its exact head and reloads", async () => {
    const onChanged = vi.fn();
    renderBar([revision], { onChanged });
    fireEvent.click(primary());
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    expect(submitRevisionVerdict).toHaveBeenCalledWith(
      "W",
      revision,
      "approve",
      "",
      "lead",
    );
  });

  it("keeps the buttons disabled until the diff of the newest revision is on screen", () => {
    const { rerender } = renderBar([revision], { diffs: null });
    expect(primary()).toBeDisabled();
    expect(screen.getByRole("button", { name: "Reject" })).toBeDisabled();
    rerender(
      <ReviewBar
        workspaceId="W"
        taskId="T"
        lead="lead"
        current={[revision]}
        diffs={[{ ...diffFor(revision), revision: 1 }]}
        onChanged={() => {}}
      />,
    );
    expect(primary()).toBeDisabled();
  });

  it("asks why before rejecting, and sends the reason", async () => {
    renderBar([revision]);
    fireEvent.click(screen.getByRole("button", { name: "Reject" }));
    const confirm = screen.getByRole("button", { name: "Reject and rerun" });
    expect(confirm).toBeDisabled();
    fireEvent.change(screen.getByLabelText(/The agent sees this/), {
      target: { value: "use the helper" },
    });
    fireEvent.click(confirm);
    await waitFor(() =>
      expect(submitRevisionVerdict).toHaveBeenCalledWith(
        "W",
        revision,
        "reject",
        "use the helper",
        "lead",
      ),
    );
  });

  it("puts Override in the ⋯ menu and needs a reason", async () => {
    renderBar([revision]);
    fireEvent.click(
      screen.getByRole("button", { name: "More review options" }),
    );
    fireEvent.click(
      screen.getByRole("menuitem", { name: "Override (needs a reason)" }),
    );
    const record = screen.getByRole("button", { name: "Record override" });
    expect(record).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Override reason"), {
      target: { value: "hotfix" },
    });
    fireEvent.click(record);
    await waitFor(() =>
      expect(submitRevisionVerdict).toHaveBeenCalledWith(
        "W",
        revision,
        "override",
        "hotfix",
        "lead",
      ),
    );
  });

  it("cannot approve an incomplete capture", () => {
    renderBar([{ ...revision, incomplete: true }]);
    expect(screen.getByText(/Capture incomplete/)).toBeInTheDocument();
    expect(primary()).toBeDisabled();
    expect(screen.getByRole("button", { name: "Reject" })).toBeDisabled();
  });

  it("shows No changes with no buttons for an empty attempt", () => {
    renderBar([{ ...revision, no_changes: true }]);
    expect(screen.getByTestId("revision-no-changes")).toHaveTextContent(
      "No changes",
    );
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
});

describe("ReviewBar status line after a decision", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    submitRevisionVerdict.mockResolvedValue("recorded");
    applyRevision.mockResolvedValue(undefined);
    updateIssue.mockResolvedValue({});
  });

  const approved = {
    ...revision,
    verdict: "approve",
    follow_status: "applied",
  };
  const status = () => screen.getByTestId("review-status");
  const next = () => screen.getByTestId("review-next-action");
  const noVerdictButtons = () => {
    expect(
      screen.queryByRole("button", { name: /^Approve code/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Reject" }),
    ).not.toBeInTheDocument();
  };

  it("approved and applied with a PR", () => {
    renderBar([
      {
        ...approved,
        applied: true,
        pr_number: 4,
        pr_url: "https://github.com/o/r/pull/4",
        publish_status: "published",
      },
    ]);
    expect(status()).toHaveTextContent(
      "✅ Approved · Applied to lead · PR #4 is open",
    );
    expect(screen.getByTestId("revision-pr")).toHaveTextContent(
      "PR #4 is open",
    );
    expect(screen.getByRole("link", { name: "PR #4" })).toHaveAttribute(
      "href",
      "https://github.com/o/r/pull/4",
    );
    noVerdictButtons();
  });

  it("approved with no provider says not published and offers Retry", async () => {
    renderBar([
      {
        ...approved,
        applied: true,
        publish_status: "not_published",
        publish_reason:
          "not published: no provider (the repository has no origin remote)",
      },
    ]);
    expect(status()).toHaveTextContent(
      "✅ Approved · Applied · not published: no provider",
    );
    noVerdictButtons();
    fireEvent.click(next());
    await waitFor(() =>
      expect(submitRevisionVerdict).toHaveBeenCalledWith(
        "W",
        expect.objectContaining({ number: 2 }),
        "approve",
        "",
        "lead",
      ),
    );
  });

  it("an approval from before D40 (applied, no PR, no intent) offers Retry", () => {
    renderBar([{ ...approved, applied: true }]);
    expect(status()).toHaveTextContent("✅ Approved · Applied · no PR yet");
    expect(next()).toHaveTextContent("Retry");
  });

  it("couldn't apply: conflict offers Rerun from latest, which sends the task back", async () => {
    renderBar([{ ...approved, follow_status: "conflict" }]);
    expect(status()).toHaveTextContent("⚠️ Approved · Couldn't apply");
    expect(next()).toHaveTextContent("Rerun from latest");
    fireEvent.click(next());
    await waitFor(() =>
      expect(submitRevisionVerdict).toHaveBeenCalledWith(
        "W",
        expect.objectContaining({ number: 2 }),
        "reject",
        expect.stringContaining("conflicts with the lead's current code"),
        "lead",
      ),
    );
  });

  it("after Unapply offers Apply (F6) and no stale not-published text", async () => {
    renderBar([
      {
        ...approved,
        follow_status: "unapplied",
        publish_status: "not_published",
        publish_reason: "not published: no provider",
      },
    ]);
    expect(status()).toHaveTextContent(/^Approved · not applied/);
    expect(status()).not.toHaveTextContent("not published");
    fireEvent.click(next());
    await waitFor(() =>
      expect(applyRevision).toHaveBeenCalledWith(
        "W",
        expect.objectContaining({ number: 2 }),
        "lead",
      ),
    );
  });

  it("a spent approval applies again by approving again", async () => {
    renderBar([
      {
        ...approved,
        follow_status: "spent",
        follow_reason: "it was unapplied",
      },
    ]);
    expect(screen.getByTestId("review-status-detail")).toHaveTextContent(
      "it was unapplied",
    );
    fireEvent.click(next());
    await waitFor(() =>
      expect(submitRevisionVerdict).toHaveBeenCalledWith(
        "W",
        expect.anything(),
        "approve",
        "",
        "lead",
      ),
    );
    expect(applyRevision).not.toHaveBeenCalled();
  });

  it("rejected: shows the reason and that the task went back to the agent", () => {
    renderBar(
      [{ ...revision, verdict: "reject", verdict_reason: "wrong file" }],
      {
        taskStatus: "open",
      },
    );
    expect(status()).toHaveTextContent("✗ Rejected: wrong file");
    expect(screen.getByTestId("review-status-detail")).toHaveTextContent(
      "Sent back to the agent",
    );
    noVerdictButtons();
  });

  it("rejected on a closed task offers Retry task, which reopens it", async () => {
    renderBar([{ ...revision, verdict: "reject" }], { taskStatus: "closed" });
    fireEvent.click(screen.getByRole("button", { name: "Retry task" }));
    await waitFor(() =>
      expect(updateIssue).toHaveBeenCalledWith("W", "T", { status: "open" }),
    );
  });

  it("an approval still opening its PR says so", () => {
    renderBar([
      {
        ...approved,
        applied: true,
        publish_status: "pending",
        publish_reason: "provider down",
      },
    ]);
    expect(status()).toHaveTextContent(
      "✅ Approved · Applied to lead · PR not opened yet",
    );
    expect(screen.getByTestId("review-status-detail")).toHaveTextContent(
      "provider down. Loom retries on its own.",
    );
    expect(screen.queryByTestId("review-next-action")).not.toBeInTheDocument();
  });
});

describe("ReviewBar for a task that changes several repos", () => {
  const api = { ...revision, change_id: "A", repo: "api" };
  const web = { ...revision, change_id: "B", repo: "web" };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("has one set of buttons and approves every repo in one request", async () => {
    approveTask.mockResolvedValue("published");
    renderBar([api, web]);
    expect(
      screen.getAllByRole("button", { name: "Approve code & create PR" }),
    ).toHaveLength(1);
    expect(screen.getByTestId("review-header")).toHaveTextContent(
      "2 files, +4 −2",
    );
    fireEvent.click(primary());
    await waitFor(() => expect(approveTask).toHaveBeenCalledTimes(1));
    // All or none (P2.23): the server publishes every repo or no repo.
    expect(approveTask).toHaveBeenCalledWith(
      "W",
      "T",
      [api, web],
      "approve",
      "",
      "lead",
    );
    expect(submitRevisionVerdict).not.toHaveBeenCalled();
  });

  it("shows the server's reason naming the repo that did not apply", async () => {
    approveTask.mockRejectedValue(
      new ApiError(409, "Conflict", {
        error: "not_all_applied",
        status: "recorded",
        repo: "web",
        data: { Kind: "approve" },
        message:
          "web: couldn't apply: it conflicts with the lead's current code. No PR is opened for any repo until every repo applies.",
      }),
    );
    renderBar([api, web]);
    fireEvent.click(primary());
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /^web: couldn't apply: .*No PR is opened for any repo/,
    );
    // Both approvals are recorded, so neither repo offers Approve again.
    expect(screen.queryByTestId("approve-create-pr")).toBeNull();
  });

  it("retries a repo held back by another repo together with that repo", async () => {
    approveTask.mockResolvedValue("published");
    const held = {
      ...api,
      verdict: "approve",
      applied: true,
      follow_status: "applied",
    };
    const rerun = { ...web, number: 3 };
    renderBar([held, rerun]);
    fireEvent.click(primary());
    await waitFor(() => expect(approveTask).toHaveBeenCalledTimes(1));
    expect(approveTask.mock.calls[0]![2]).toEqual([held, rerun]);
    expect(submitRevisionVerdict).not.toHaveBeenCalled();
  });

  it("rejects each repo in turn, stopping at a failure that names the repo", async () => {
    submitRevisionVerdict
      .mockRejectedValueOnce(
        new ApiError(409, "Conflict", { error: "stale_subject" }),
      )
      .mockResolvedValue("recorded");
    renderBar([api, web]);
    fireEvent.click(screen.getByTestId("review-reject"));
    fireEvent.change(screen.getByTestId("review-reason"), {
      target: { value: "no" },
    });
    fireEvent.click(screen.getByTestId("reject-confirm"));
    expect(await screen.findByRole("alert")).toHaveTextContent(/^api: /);
    expect(submitRevisionVerdict).toHaveBeenCalledTimes(1);
    expect(approveTask).not.toHaveBeenCalled();
  });

  it("names the repo on each status line", () => {
    renderBar([
      { ...api, verdict: "approve", follow_status: "conflict" },
      { ...web, verdict: "approve", applied: true, pr_number: 9 },
    ]);
    const lines = screen.getAllByTestId("review-status");
    expect(lines[0]).toHaveTextContent("api: ⚠️ Approved · Couldn't apply");
    expect(lines[1]).toHaveTextContent(
      "web: ✅ Approved · Applied to lead · PR #9 is open",
    );
  });
});

describe("review summary for Details", () => {
  beforeEach(() => vi.clearAllMocks());

  it("says what the review stands at", () => {
    expect(reviewSummary([revision])).toBe("Code awaiting review");
    expect(
      reviewSummary([
        { ...revision, verdict: "approve", applied: true, pr_number: 4 },
      ]),
    ).toBe("✅ Approved · Applied to lead · PR #4 is open");
    expect(reviewSummary([])).toBe("");
    expect(statusLine(revision)).toBeNull();
  });

  it("links to Changes", async () => {
    getTaskRevisions.mockResolvedValue([{ ...revision, number: 1 }, revision]);
    const onOpen = vi.fn();
    render(
      <ReviewStatusLink
        workspaceId="W"
        taskId="T"
        lead="lead"
        onOpen={onOpen}
      />,
    );
    const link = await screen.findByTestId("review-status-link");
    expect(link).toHaveTextContent("Code awaiting review → Changes");
    fireEvent.click(screen.getByRole("button", { name: "→ Changes" }));
    expect(onOpen).toHaveBeenCalled();
  });

  it("shows nothing for a task with no code yet", async () => {
    getTaskRevisions.mockResolvedValue([]);
    const { container } = render(
      <ReviewStatusLink workspaceId="W" taskId="T" onOpen={() => {}} />,
    );
    await waitFor(() => expect(getTaskRevisions).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });
});
