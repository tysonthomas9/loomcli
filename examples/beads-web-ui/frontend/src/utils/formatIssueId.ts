/**
 * Format issue ID for display.
 * Shows last 7 characters of the ID for readability.
 */
export function formatIssueId(id: string): string {
  if (!id) return 'unknown';
  // If ID is short enough, return as-is
  if (id.length <= 10) return id;
  // Otherwise show last 7 characters
  return id.slice(-7);
}
