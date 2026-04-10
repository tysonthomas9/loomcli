/**
 * WorkspaceBreadcrumb - Displays the active view label in the AppLayout header.
 * Workspace identity (name + color dot) lives in the sidebar WorkspaceSelectorBar.
 */

import type { ViewMode } from "@/components/ViewSwitcher";

import styles from "./WorkspaceBreadcrumb.module.css";

const VIEW_LABELS: Record<ViewMode, string> = {
  kanban: "Aether Project",
  table: "List",
  graph: "Graph",
  monitor: "Monitor",
  observability: "Observability",
  terminal: "Terminal",
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
  const rootClassName = className
    ? `${styles.breadcrumb} ${className}`
    : styles.breadcrumb;

  if (!workspaceName) {
    return <span className={rootClassName}>Aether</span>;
  }

  const viewLabel = VIEW_LABELS[activeView] ?? "Kanban";

  return (
    <span className={rootClassName}>
      <span className={styles.viewLabel}>{viewLabel}</span>
    </span>
  );
}
