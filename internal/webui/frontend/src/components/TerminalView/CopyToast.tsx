/**
 * Minimal toast notification showing "Copied!" when text is copied to clipboard.
 * Auto-dismisses after 1.5 seconds with fade animation.
 */

import styles from "./CopyToast.module.css";

interface CopyToastProps {
  visible: boolean;
}

export function CopyToast({ visible }: CopyToastProps): JSX.Element | null {
  if (!visible) return null;

  return (
    <div className={styles.toast} role="status" aria-live="polite">
      Copied!
    </div>
  );
}
