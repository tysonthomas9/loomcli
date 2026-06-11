import type { Issue } from "@/types";

/** Open (non-done) issue counts keyed by repo name. */
export function getOpenIssueCountByRepo(
  issues: Issue[],
): Record<string, number> {
  const counts: Record<string, number> = {};

  for (const issue of issues) {
    if (issue.issue_type === "epic") continue;
    if (issue.status === "closed") continue;

    const repo = issue.repo ?? issue.source_repo;
    if (!repo) continue;

    counts[repo] = (counts[repo] ?? 0) + 1;
  }

  return counts;
}
