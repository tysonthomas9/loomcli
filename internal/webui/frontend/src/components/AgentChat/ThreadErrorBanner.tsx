// Ported from T3 Code apps/web/src/components/chat/ThreadErrorBanner.tsx at
// commit 2daff8c25. Copyright (c) 2026 T3 Tools Inc. MIT License; see
// THIRD_PARTY_NOTICES.md. Changes: CSS modules and a plain alert in place of
// T3's Alert, Tooltip and lucide icons; the full error shows on hover as a
// title.

import { memo } from "react";
import styles from "./PendingAsk.module.css";

export function getThreadErrorBannerKey(
  threadKey: string,
  error: string | null,
): string | null {
  return error === null ? null : `${threadKey}\u0000${error}`;
}

// Session-scoped (module-level so it survives AgentChat remounts, e.g. route
// changes between agents). A dismissal is remembered per agent plus message,
// so a different error on the same agent still appears.
const sessionDismissedThreadErrorBannerKeys = new Set<string>();

export function dismissThreadErrorBannerForSession(
  bannerKey: string | null,
): void {
  if (bannerKey !== null) {
    sessionDismissedThreadErrorBannerKeys.add(bannerKey);
  }
}

export function isThreadErrorBannerDismissedForSession(
  bannerKey: string | null,
): boolean {
  return (
    bannerKey !== null && sessionDismissedThreadErrorBannerKeys.has(bannerKey)
  );
}

export const ThreadErrorBanner = memo(function ThreadErrorBanner({
  error,
  onDismiss,
}: {
  error: string | null;
  onDismiss?: () => void;
}) {
  if (!error) return null;
  return (
    <div className={styles.errorBannerWrap}>
      <div className={styles.errorBanner} role="alert" data-testid="turn-error">
        <span className={styles.errorIcon} aria-hidden="true">
          !
        </span>
        <div className={styles.errorText} title={error}>
          {error}
        </div>
        {onDismiss && (
          <button
            type="button"
            className={styles.errorDismiss}
            aria-label="Dismiss error"
            onClick={onDismiss}
          >
            ×
          </button>
        )}
      </div>
    </div>
  );
});
