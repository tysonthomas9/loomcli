/**
 * NoBackendsEmptyState component.
 * Shown when no AI backends are configured, with a link to Settings.
 */

import styles from "./NoBackendsEmptyState.module.css";

interface NoBackendsEmptyStateProps {
  onGoToSettings?: () => void;
}

export function NoBackendsEmptyState({
  onGoToSettings,
}: NoBackendsEmptyStateProps): JSX.Element {
  return (
    <div
      className={styles.container}
      data-testid="no-backends-empty-state"
      role="status"
    >
      <div className={styles.icon}>&#x2B1A;</div>
      <h2 className={styles.heading}>No backends configured</h2>
      <p className={styles.description}>
        Configure at least one AI backend to start using Talk to Lead.
      </p>
      {onGoToSettings && (
        <button
          type="button"
          className={styles.settingsButton}
          onClick={onGoToSettings}
          data-testid="go-to-settings-button"
        >
          Go to Settings
        </button>
      )}
    </div>
  );
}
