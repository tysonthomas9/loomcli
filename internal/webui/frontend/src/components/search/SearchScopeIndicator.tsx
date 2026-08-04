/**
 * SearchScopeIndicator - Shows the active search scope as a chip.
 * Renders inline next to the search input when a workspace/repo scope is active.
 */

import styles from "./SearchScopeIndicator.module.css";

export interface SearchScopeIndicatorProps {
  /** Name of the active scope (group or repo name). Undefined = no scope. */
  scopeName?: string;
  /** Called when the user clicks the clear button to remove the scope. */
  onClear: () => void;
}

export function SearchScopeIndicator({
  scopeName,
  onClear,
}: SearchScopeIndicatorProps): JSX.Element | null {
  if (!scopeName) return null;

  return (
    <span className={styles.chip} data-testid="search-scope-indicator">
      <span className={styles.label}>in: {scopeName}</span>
      <button
        className={styles.clearButton}
        onClick={onClear}
        aria-label={`Clear scope: ${scopeName}`}
        type="button"
      >
        &times;
      </button>
    </span>
  );
}
