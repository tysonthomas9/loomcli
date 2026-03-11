/**
 * Format snake_case status to human-readable label.
 * Converts each word to Title Case and joins with spaces.
 *
 * @example formatStatusLabel('in_progress') => 'In Progress'
 * @example formatStatusLabel('open') => 'Open'
 * @example formatStatusLabel('custom_status') => 'Custom Status'
 */
export function formatStatusLabel(status: string): string {
  return status
    .split("_")
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1).toLowerCase())
    .join(" ");
}
