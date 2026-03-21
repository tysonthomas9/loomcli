/**
 * Format issue ID for display.
 * Preserves the project prefix for recognizable IDs.
 * Only truncates unusually long IDs (e.g. hierarchical sub-task IDs).
 */
export function formatIssueId(id: string): string {
  if (!id) {
    if (process.env.NODE_ENV === "development") {
      console.warn("[formatIssueId] Received empty issue ID");
    }
    return "unknown";
  }
  // Most IDs (e.g. 'loomcli-pso6j') are ≤16 chars — show in full
  if (id.length <= 16) return id;
  // For longer IDs, preserve prefix and truncate
  const hyphenIdx = id.indexOf("-");
  if (hyphenIdx > 0) {
    const prefix = id.slice(0, hyphenIdx);
    const rest = id.slice(hyphenIdx + 1);
    return `${prefix}-${rest.slice(0, 5)}...`;
  }
  // No hyphen — just truncate
  return `${id.slice(0, 13)}...`;
}
