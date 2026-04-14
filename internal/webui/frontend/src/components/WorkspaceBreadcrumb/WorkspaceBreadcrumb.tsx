/**
 * WorkspaceBreadcrumb - Displays a workspace color dot and the active view label.
 * Shows "● ViewLabel" in the AppLayout header; the workspace name is shown in the
 * sidebar's WorkspaceSelectorBar. Falls back to "Cortex" when no workspace is available.
 */

import type { ViewMode } from "@/components/ViewSwitcher";
import { getWorkspaceColor } from "@/utils/workspace";

import styles from "./WorkspaceBreadcrumb.module.css";

const VIEW_LABELS: Record<ViewMode, string> = {
  kanban: "Aether Project",
  table: "List",
  graph: "Graph",
  monitor: "Monitor",
  observability: "Observability",
  terminal: "Monitor",
  workspace: "Workspace",
  settings: "Settings",
  files: "Files",
  "issue-detail": "Issue",
};

export interface WorkspaceBreadcrumbProps {
  workspaceName: string | null;
  activeView: ViewMode;
  className?: string;
}

export function WorkspaceBreadcrumb({
  workspaceName,
  activeView,
  className,
}: WorkspaceBreadcrumbProps): JSX.Element {
  if (!workspaceName) {
    const fallbackClassName = className
      ? `${styles.breadcrumb} ${className}`
      : styles.breadcrumb;
    return <span className={fallbackClassName}>Cortex</span>;
  }

  const color = getWorkspaceColor(workspaceName);
  const viewLabel = VIEW_LABELS[activeView] ?? "Aether Project";

  const rootClassName = className
    ? `${styles.breadcrumb} ${className}`
    : styles.breadcrumb;

  return (
    <span className={rootClassName}>
      <span className={styles.dot} style={{ backgroundColor: color }} />
      <span className={styles.viewLabel}>{viewLabel}</span>
    </span>
  );
}
