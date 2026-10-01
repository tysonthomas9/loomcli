import { afterEach, expect, it, vi } from "vitest";

import { api } from "@/api/common";

import { fetchGraphIssues } from "../issues";

afterEach(() => {
  vi.restoreAllMocks();
});

it("#682c graph mapping keeps source_repo for repo-filtered views", async () => {
  vi.spyOn(api, "GET").mockResolvedValue({
    data: {
      success: true,
      data: [
        {
          id: "issue-1",
          title: "Scoped node",
          status: "open",
          priority: 2,
          issue_type: "task",
          source_repo: "repo-a",
        },
      ],
    },
    error: undefined,
    response: new Response(null, { status: 200 }),
  } as never);

  const [issue] = await fetchGraphIssues("ws-replay", {
    source_repos: ["repo-a"],
  });

  expect(
    issue?.source_repo,
    "graph client mapping dropped source_repo, so repo filtering drops the node",
  ).toBe("repo-a");
});
