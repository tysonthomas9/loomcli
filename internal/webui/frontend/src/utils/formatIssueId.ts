/**
 * Format issue ID for display.
 * Shows last 7 characters of the ID for readability.
 */
export function formatIssueId(id: string): string {
  if (!id) {
    if (process.env.NODE_ENV === 'development') {
      console.warn('[formatIssueId] Received empty issue ID');
    }
    return 'unknown';
  }
  // If ID is short enough, return as-is
  if (id.length <= 10) return id;
  // Otherwise show last 7 characters
  return id.slice(-7);
}
