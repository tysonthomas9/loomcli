import type { Issue, IssueDetails, IssueType } from "@/types";

/**
 * Format issue type for display.
 */
export function formatIssueType(type: IssueType | undefined): string {
  if (!type) return "Task";
  if (type === "epic") return "Epic";
  if (type === "task") return "Task";
  if (type === "bug") return "Bug";
  if (type === "feature") return "Feature";
  return type;
}

/**
 * Format date for display.
 */
export function formatDate(dateString: string): string {
  const date = new Date(dateString);
  return date.toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}

/**
 * Type guard to check if issue has IssueDetails fields.
 * Checks for fields that indicate this is a detailed issue response.
 * Note: The backend may omit empty arrays (dependents, dependencies),
 * but always includes comments array in IssueDetails responses.
 */
export function isIssueDetails(
  issue: Issue | IssueDetails,
): issue is IssueDetails {
  // Check for any IssueDetails-specific field that the backend includes
  // Comments is always present in /api/issues/{id} responses
  return (
    "dependents" in issue || "dependencies" in issue || "comments" in issue
  );
}
