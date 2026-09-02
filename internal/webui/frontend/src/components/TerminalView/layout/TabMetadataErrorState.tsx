/**
 * TabMetadataErrorState component.
 * Shown in place of the loading skeleton when the tab metadata list failed to
 * load. A failed load deliberately leaves the hook "loading" (PUPPET-125), so
 * without this the view would sit on the skeleton forever with no way back.
 */

import styles from "./TabMetadataErrorState.module.css";

interface TabMetadataErrorStateProps {
  message: string;
  onRetry: () => void;
}

export function TabMetadataErrorState({
  message,
  onRetry,
}: TabMetadataErrorStateProps): JSX.Element {
  return (
    <div
      className={styles.container}
      data-testid="tab-metadata-error"
      role="alert"
    >
      <h2 className={styles.heading}>Couldn&rsquo;t load terminal tabs</h2>
      <p className={styles.description}>{message}</p>
      <button
        type="button"
        className={styles.retryButton}
        onClick={onRetry}
        data-testid="retry-tab-metadata"
      >
        Retry
      </button>
    </div>
  );
}
