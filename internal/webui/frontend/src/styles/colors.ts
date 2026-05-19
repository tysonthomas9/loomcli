/**
 * Color constants for the loom web UI frontend.
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
  blocked: "#f87171", // red-400
  /** Color for ready issues (no blockers, can be worked on) */
  ready: "#34d399", // emerald-400
  /** Color for closed/completed issues */
  closed: "#34d399", // emerald-400 (same as status-closed)
} as const;

/**
 * Issue status colors.
 * Used for status badges and indicators.
 */
export const StatusColors = {
  open: "#60a5fa", // blue-400
  in_progress: "#facc15", // yellow-400 (lemon — amber is reserved for brand)
  review: "#a78bfa", // violet-400
  closed: "#34d399", // emerald-400
} as const;

/**
 * Priority colors (P0-P4).
 * Used for priority badges and indicators.
 */
export const PriorityColors = {
  0: "#f87171", // red-400, critical
  1: "#fb923c", // orange-400, high
  2: "#fbbf24", // amber-400, medium
  3: "#60a5fa", // blue-400, normal
  4: "#6b7280", // gray-500, low/backlog
} as const;

/**
 * Semantic colors for general UI elements.
 */
export const SemanticColors = {
  primary: "#e8a020", // Cortex amber — brand accent
  success: "#34d399", // emerald-400
  warning: "#facc15", // lemon (amber is reserved for brand)
  danger: "#f87171", // red-400
  info: "#06b6d4", // cyan-500
} as const;

/**
 * Issue type colors.
 */
export const TypeColors = {
  epic: "#a78bfa", // violet-400
} as const;

/**
 * Type definitions for color values.
 */
export type StateColor = (typeof StateColors)[keyof typeof StateColors];
export type StatusColor = (typeof StatusColors)[keyof typeof StatusColors];
export type PriorityColor =
  (typeof PriorityColors)[keyof typeof PriorityColors];
export type SemanticColor =
  (typeof SemanticColors)[keyof typeof SemanticColors];
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
export function getPriorityColor(
  priority: keyof typeof PriorityColors,
): PriorityColor {
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
