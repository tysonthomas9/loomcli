/**
 * TabMarkers — the marker slot rendered next to a terminal tab's label.
 *
 * Deliberately a list rather than a single boolean: markers are independent
 * truths about a tab (its shell was replaced, it is waiting for input, …) and
 * more of them are coming, so they share one slot and one layout.
 *
 * Markers are separate from the connection dot. A replaced tab can be happily
 * connected; the dot says "is the socket up", a marker says "something
 * happened to this session that you have not acknowledged".
 */

import styles from "./TabMarkers.module.css";

export interface TabMarkersProps {
  /** Tab id, used to build stable test ids. */
  tabId: string;
  /**
   * RFC3339 timestamp of the last shell replacement. Empty string (dismissed)
   * and undefined (never replaced) both render nothing.
   */
  replacedAt?: string | undefined;
  /** When set, the restart marker is clickable and dismisses on click. */
  onDismissRestartNotice?: (() => void) | undefined;
  /** Non-interactive rendering (drag overlay), no buttons, no click targets. */
  static?: boolean | undefined;
}

/** Human-readable restart time for the accessible label; falls back to raw. */
function formatRestartTime(replacedAt: string): string {
  const parsed = new Date(replacedAt);
  if (Number.isNaN(parsed.getTime())) return replacedAt;
  return parsed.toLocaleString();
}

function WarningGlyph() {
  return (
    <svg
      width="11"
      height="11"
      viewBox="0 0 16 16"
      fill="currentColor"
      aria-hidden="true"
      focusable="false"
    >
      <path d="M8 1.5a.9.9 0 0 1 .78.45l6.1 10.6a.9.9 0 0 1-.78 1.35H1.9a.9.9 0 0 1-.78-1.35l6.1-10.6A.9.9 0 0 1 8 1.5zm0 3.7a.7.7 0 0 0-.7.76l.28 3.3a.42.42 0 0 0 .84 0l.28-3.3A.7.7 0 0 0 8 5.2zm0 5.5a.85.85 0 1 0 0 1.7.85.85 0 0 0 0-1.7z" />
    </svg>
  );
}

export function TabMarkers({
  tabId,
  replacedAt,
  onDismissRestartNotice,
  static: isStatic,
}: TabMarkersProps): JSX.Element | null {
  const showRestart = replacedAt != null && replacedAt !== "";
  if (!showRestart) return null;

  const label = `Session restarted ${formatRestartTime(replacedAt)}`;

  return (
    <span className={styles.markers} data-testid={`tab-markers-${tabId}`}>
      {isStatic ? (
        <span
          className={styles.marker}
          data-marker="restart"
          role="img"
          aria-label={label}
          title={label}
          data-testid={`tab-marker-restart-${tabId}`}
        >
          <WarningGlyph />
        </span>
      ) : (
        <button
          type="button"
          tabIndex={-1}
          draggable={false}
          className={`${styles.marker} ${styles.markerButton}`}
          data-marker="restart"
          aria-label={`${label} — dismiss`}
          title={`${label}. Click to dismiss.`}
          data-testid={`tab-marker-restart-${tabId}`}
          onDragStart={(e) => e.stopPropagation()}
          onClick={(e) => {
            e.stopPropagation();
            onDismissRestartNotice?.();
          }}
        >
          <WarningGlyph />
        </button>
      )}
    </span>
  );
}
