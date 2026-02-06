/**
 * Color constants for the beads-web-ui frontend.
 *
 * These constants mirror the CSS custom properties defined in variables.css
 * and provide TypeScript-accessible color values for use in JavaScript/React
 * components (e.g., react-flow nodes, canvas rendering, dynamic styling).
 *
 * IMPORTANT: Keep these values in sync with variables.css.
 */

/**
 * Issue state colors for dependency graph visualization.
 * Used to color nodes based on their blocking/ready/closed state.
 */
export const StateColors = {
  /** Color for blocked issues (waiting on dependencies) */
  blocked: '#ef4444', // red-500
  /** Color for ready issues (no blockers, can be worked on) */
  ready: '#22c55e', // green-500
  /** Color for closed/completed issues */
  closed: '#10b981', // emerald-500 (same as status-closed)
} as const;

/**
 * Issue status colors.
 * Used for status badges and indicators.
 */
export const StatusColors = {
  open: '#3b82f6', // blue-500
  in_progress: '#f59e0b', // amber-500
  review: '#8b5cf6', // purple-500
  closed: '#10b981', // emerald-500
} as const;

/**
 * Priority colors (P0-P4).
 * Used for priority badges and indicators.
 */
export const PriorityColors = {
  0: '#dc2626', // red-600, critical
  1: '#ea580c', // orange-600, high
  2: '#ca8a04', // yellow-600, medium
  3: '#2563eb', // blue-600, normal
  4: '#6b7280', // gray-500, low/backlog
} as const;

/**
 * Semantic colors for general UI elements.
 */
export const SemanticColors = {
  primary: '#3b82f6', // blue-500
  success: '#22c55e', // green-500
  warning: '#f59e0b', // amber-500
  danger: '#ef4444', // red-500
  info: '#06b6d4', // cyan-500
} as const;

/**
 * Issue type colors.
 */
export const TypeColors = {
  epic: '#8b5cf6', // purple-500
} as const;

/**
 * Type definitions for color values.
 */
export type StateColor = (typeof StateColors)[keyof typeof StateColors];
export type StatusColor = (typeof StatusColors)[keyof typeof StatusColors];
export type PriorityColor = (typeof PriorityColors)[keyof typeof PriorityColors];
export type SemanticColor = (typeof SemanticColors)[keyof typeof SemanticColors];
export type TypeColor = (typeof TypeColors)[keyof typeof TypeColors];

/**
 * Helper function to get state color by issue state.
 *
 * @param state - The issue state ('blocked', 'ready', or 'closed')
 * @returns The corresponding color hex value
 *
 * @example
 * ```tsx
 * const nodeColor = getStateColor('blocked'); // '#ef4444'
 * ```
 */
export function getStateColor(state: keyof typeof StateColors): StateColor {
  return StateColors[state];
}

/**
 * Helper function to get priority color by priority level.
 *
 * @param priority - The priority level (0-4)
 * @returns The corresponding color hex value
 *
 * @example
 * ```tsx
 * const badgeColor = getPriorityColor(0); // '#dc2626' (critical)
 * ```
 */
export function getPriorityColor(priority: keyof typeof PriorityColors): PriorityColor {
  return PriorityColors[priority];
}

/**
 * Helper function to get status color by status name.
 *
 * @param status - The status name ('open', 'in_progress', or 'closed')
 * @returns The corresponding color hex value
 *
 * @example
 * ```tsx
 * const indicatorColor = getStatusColor('in_progress'); // '#f59e0b'
 * ```
 */
export function getStatusColor(status: keyof typeof StatusColors): StatusColor {
  return StatusColors[status];
}
