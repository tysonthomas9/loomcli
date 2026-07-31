/**
 * WorkspaceBreadcrumb - Displays the Loom brand in the AppLayout header.
 * Workspace identity (name + color dot) lives in the sidebar WorkspaceSelectorBar.
 */

import type { ViewMode } from "@/types";

import styles from "./WorkspaceBreadcrumb.module.css";

export interface WorkspaceBreadcrumbProps {
  workspaceName: string | null;
  activeView: ViewMode;
  className?: string;
}

export function WorkspaceBreadcrumb({
  className,
}: WorkspaceBreadcrumbProps): JSX.Element {
  const rootClassName = className
    ? `${styles.breadcrumb} ${className}`
    : styles.breadcrumb;

  return (
    <span className={rootClassName}>
      <span className={styles.brandMark} aria-hidden="true">
        ◇
      </span>
      <span className={styles.viewLabel}>Loom</span>
    </span>
  );
}
