/**
 * Shared client-side issue search for board-level filtering.
 * Aligns with global search (id + title) and AgentWorkPanel task search fields.
 */

import type { Issue } from "@/types";

/**
 * Whether an issue's epic metadata (parent id or parent title) matches the term.
 */
export function epicMetadataMatchesSearch(
  issue: Issue,
  normalizedTerm: string,
): boolean {
  if (
    typeof issue.parent === "string" &&
    issue.parent.toLowerCase().includes(normalizedTerm)
  ) {
    return true;
  }

  if (
    typeof issue.parent_title === "string" &&
    issue.parent_title.toLowerCase().includes(normalizedTerm)
  ) {
    return true;
  }

  return false;
}

/**
 * Check if a single issue matches a search term (case-insensitive substring).
 */
export function issueMatchesSearch(issue: Issue, term: string): boolean {
  const normalizedTerm = term.trim().toLowerCase();
  if (!normalizedTerm) return true;

  // Guard id like every other field: optimistic/partial records may arrive with
  // a nullish id, and an unguarded toLowerCase() would crash the board render.
  if (
    typeof issue.id === "string" &&
    issue.id.toLowerCase().includes(normalizedTerm)
  ) {
    return true;
  }

  if (
    typeof issue.title === "string" &&
    issue.title.toLowerCase().includes(normalizedTerm)
  ) {
    return true;
  }

  if (
    typeof issue.description === "string" &&
    issue.description.toLowerCase().includes(normalizedTerm)
  ) {
    return true;
  }

  if (
    typeof issue.notes === "string" &&
    issue.notes.toLowerCase().includes(normalizedTerm)
  ) {
    return true;
  }

  if (
    typeof issue.assignee === "string" &&
    issue.assignee.toLowerCase().includes(normalizedTerm)
  ) {
    return true;
  }

  const status = (issue.status ?? "open").toLowerCase();
  if (status.includes(normalizedTerm)) return true;

  if (
    Array.isArray(issue.labels) &&
    issue.labels.some((label) => label.toLowerCase().includes(normalizedTerm))
  ) {
    return true;
  }

  if (epicMetadataMatchesSearch(issue, normalizedTerm)) return true;

  return false;
}

/**
 * Filter issues by search term. When an epic id or title matches, all tasks in
 * that epic remain visible so swim-lane and list views keep the lane intact.
 */
export function filterIssuesBySearch(issues: Issue[], term: string): Issue[] {
  const normalizedTerm = term.trim().toLowerCase();
  if (!normalizedTerm) return issues;

  const matchingEpicIds = new Set<string>();

  for (const issue of issues) {
    if (
      issue.issue_type === "epic" &&
      issueMatchesSearch(issue, normalizedTerm)
    ) {
      matchingEpicIds.add(issue.id);
    }
    if (issue.parent && epicMetadataMatchesSearch(issue, normalizedTerm)) {
      matchingEpicIds.add(issue.parent);
    }
  }

  return issues.filter((issue) => {
    if (issueMatchesSearch(issue, normalizedTerm)) return true;
    if (issue.parent && matchingEpicIds.has(issue.parent)) return true;
    return false;
  });
}
