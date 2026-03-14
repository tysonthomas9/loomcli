/**
 * WorkspaceView component.
 * Placeholder for the multi-repo workspace view.
 */

import styles from "./WorkspaceView.module.css";

export interface WorkspaceViewProps {
  className?: string;
}

export function WorkspaceView({ className }: WorkspaceViewProps): JSX.Element {
  const rootClassName = [styles.workspaceView, className]
    .filter(Boolean)
    .join(" ");

  return (
    <div className={rootClassName} data-testid="workspace-view">
      <h2 className={styles.pageTitle}>Workspace</h2>
      <p className={styles.emptyState}>
        Multi-repo workspace view coming soon.
      </p>
    </div>
  );
}
