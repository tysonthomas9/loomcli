import { describe, expect, it } from "vitest";

import type { Issue } from "@/types";

import { getOpenIssueCountByRepo } from "../openIssueCountByRepo";

function issue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "1",
    title: "Task",
    status: "open",
    issue_type: "task",
    ...overrides,
  } as Issue;
}

describe("getOpenIssueCountByRepo", () => {
  it("counts open issues per repo and skips epics and closed", () => {
    const counts = getOpenIssueCountByRepo([
      issue({ id: "1", repo: "alpha", status: "open" }),
      issue({ id: "2", repo: "alpha", status: "in_progress" }),
      issue({ id: "3", repo: "beta", source_repo: "beta", status: "review" }),
      issue({ id: "4", repo: "alpha", status: "closed" }),
      issue({ id: "5", repo: "gamma", issue_type: "epic", status: "open" }),
      issue({ id: "6", status: "open" }),
    ]);

    expect(counts).toEqual({ alpha: 2, beta: 1 });
  });
});
