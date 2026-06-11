import { describe, it, expect } from "vitest";

import type { GitPullRequest } from "@/api/workspace";
import { prStateFromGithub } from "@/views/PRsPage";

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
    expect(
      prStateFromGithub({ ...base, state: "MERGED" }).label,
    ).toBe("Merged");
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
