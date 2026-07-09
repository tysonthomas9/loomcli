import { describe, it, expect } from "vitest";

import type { GitPullRequest } from "@/api/workspace";
import type { Issue } from "@/types";
import {
  buildPullRequestRows,
  prReviewRef,
  prStateFromGithub,
  rowState,
} from "@/views/PRsPage";

function makeIssue(overrides: Partial<Issue>): Issue {
  return {
    id: "task-1",
    title: "A task",
    status: "review",
    ...overrides,
  } as Issue;
}

describe("prStateFromGithub", () => {
  const base: GitPullRequest = {
    number: 7,
    title: "Fix bug",
    url: "https://github.com/org/repo/pull/7",
    state: "OPEN",
    is_draft: false,
    head_ref_name: "feat",
    base_ref_name: "main",
    repo_name: "repo",
  };

  it("shows merged state from GitHub", () => {
    expect(prStateFromGithub({ ...base, state: "MERGED" }).label).toBe(
      "Merged",
    );
  });

  it("shows draft state", () => {
    expect(prStateFromGithub({ ...base, is_draft: true }).label).toBe("Draft");
  });

  it("shows changes requested from review decision", () => {
    expect(
      prStateFromGithub({
        ...base,
        review_decision: "CHANGES_REQUESTED",
      }).label,
    ).toBe("Changes");
  });
});

describe("prReviewRef", () => {
  const base: GitPullRequest = {
    number: 7,
    title: "Fix bug",
    url: "https://github.com/octocat/hello/pull/7",
    state: "OPEN",
    is_draft: false,
    head_ref_name: "feat",
    base_ref_name: "main",
    repo_name: "octocat/hello",
  };

  it("builds the review route ref from repo name and number", () => {
    expect(prReviewRef(base)).toBe("octocat/hello#7");
  });

  it("returns null when repo name or number is missing", () => {
    expect(prReviewRef({ ...base, repo_name: "" })).toBeNull();
    expect(
      prReviewRef({ ...base, number: undefined as unknown as number }),
    ).toBeNull();
  });
});

describe("buildPullRequestRows (loom-first queue)", () => {
  const ghPr = (
    n: number,
    overrides: Partial<GitPullRequest> = {},
  ): GitPullRequest => ({
    number: n,
    title: `PR ${n}`,
    url: `https://github.com/org/repo/pull/${n}`,
    state: "OPEN",
    is_draft: false,
    head_ref_name: `feat-${n}`,
    base_ref_name: "main",
    repo_name: "repo",
    updated_at: `2026-06-0${n}T00:00:00Z`,
    ...overrides,
  });

  it("includes review-stage issues without a PR (works with gh empty)", () => {
    const issue = makeIssue({ id: "plan-1", status: "review" });
    const rows = buildPullRequestRows([issue], []);
    expect(rows).toHaveLength(1);
    expect(rows[0]?.issue?.id).toBe("plan-1");
    expect(rows[0]?.pr).toBeUndefined();
    expect(rowState(rows[0]!)).toEqual({
      label: "Plan review",
      key: "review",
    });
  });

  it("enriches issues with GitHub metadata via owner/repo#number join", () => {
    const issue = makeIssue({
      id: "task-2",
      // URL variant: www + trailing slash must still join.
      external_ref: "https://www.github.com/org/repo/pull/2/",
    });
    const rows = buildPullRequestRows([issue], [ghPr(2)]);
    expect(rows).toHaveLength(1);
    expect(rows[0]?.issue?.id).toBe("task-2");
    expect(rows[0]?.pr?.number).toBe(2);
  });

  it("renders unlinked GitHub PRs as their own rows", () => {
    const issue = makeIssue({ id: "plan-1", status: "review" });
    const rows = buildPullRequestRows([issue], [ghPr(3)]);
    expect(rows).toHaveLength(2);
    const unlinked = rows.find((r) => !r.issue);
    expect(unlinked?.pr?.number).toBe(3);
  });

  it("excludes issues that are neither in review nor PR-linked", () => {
    const open = makeIssue({ id: "open-1", status: "open" });
    expect(buildPullRequestRows([open], [])).toHaveLength(0);
  });

  it("keeps a PR-linked issue in the queue after it closes", () => {
    const done = makeIssue({
      id: "done-1",
      status: "closed",
      external_ref: "https://github.com/org/repo/pull/4",
    });
    const rows = buildPullRequestRows([done], [ghPr(4, { state: "MERGED" })]);
    expect(rows).toHaveLength(1);
    expect(rows[0]?.pr?.state).toBe("MERGED");
  });

  it("sorts rows by most recent update", () => {
    const rows = buildPullRequestRows([], [ghPr(1), ghPr(3), ghPr(2)]);
    expect(rows.map((r) => r.pr?.number)).toEqual([3, 2, 1]);
  });
});
