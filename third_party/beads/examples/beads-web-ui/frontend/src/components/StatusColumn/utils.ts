/**
 * Utility functions for StatusColumn component.
 *
 * Note: getStatusColor is exported for use by downstream components (T027, T028, T032)
 * that need programmatic access to status colors. The StatusColumn component itself
 * uses CSS data-attribute selectors for status-specific styling.
 */

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
    .split('_')
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1).toLowerCase())
    .join(' ');
}

/**
 * Get CSS variable name for a status color.
 * Maps status to the corresponding CSS custom property.
 *
 * @example getStatusColor('open') => 'var(--color-status-open)'
 * @example getStatusColor('in_progress') => 'var(--color-status-in-progress)'
 * @example getStatusColor('unknown') => 'var(--color-text-secondary)'
 */
export function getStatusColor(status: string): string {
  // Convert snake_case to kebab-case for CSS variable naming
  const kebabStatus = status.replace(/_/g, '-');

  // Known statuses and column IDs have specific color variables
  const knownStatuses = ['open', 'in-progress', 'review', 'closed', 'ready', 'backlog', 'done'];

  if (knownStatuses.includes(kebabStatus)) {
    return `var(--color-status-${kebabStatus})`;
  }

  // Default to secondary text color for unknown statuses
  return 'var(--color-text-secondary)';
}
