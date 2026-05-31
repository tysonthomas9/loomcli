/**
 * View mode types shared across the app.
 *
 * Lives in src/types/ so that hooks, utils, and contexts can reach it
 * without crossing the frontend layer DAG back into components.
 */

/**
 * Available view modes.
 */
export type ViewMode =
  | "kanban"
  | "table"
  | "graph"
  | "monitor"
  | "workflows"
  | "observability"
  | "terminal"
  | "workspace"
  | "settings"
  | "files"
  | "issue-detail"
  | "agents";

/**
 * Default view when none is specified.
 */
export const DEFAULT_VIEW: ViewMode = "kanban";
