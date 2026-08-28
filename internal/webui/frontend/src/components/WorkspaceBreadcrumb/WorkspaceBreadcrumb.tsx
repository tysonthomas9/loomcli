/**
 * WorkspaceBreadcrumb - Displays the active view label in the AppLayout header.
 * Workspace identity (name + color dot) lives in the sidebar WorkspaceSelectorBar.
 * Falls back to "Loom" when no workspace is available.
 */

import type { ViewMode } from "@/types";

import styles from "./WorkspaceBreadcrumb.module.css";

const VIEW_LABELS: Record<ViewMode, string> = {
  kanban: "Loom Project",
  table: "List",
  graph: "Graph",
  monitor: "Monitor",
  observability: "Observability",
  terminal: "Monitor",
  agents: "Agents",
  list: "List",
  prs: "Pull Requests",
  workspace: "Workspace",
  settings: "Settings",
  files: "Files",
  skills: "Skills",
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
    return <span className={rootClassName}>Loom</span>;
  }

  const viewLabel = VIEW_LABELS[activeView] ?? "Loom Project";

  return (
    <span className={rootClassName}>
      <span className={styles.viewLabel}>{viewLabel}</span>
    </span>
  );
}
