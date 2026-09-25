/**
 * Read-only ordered merge preview dialog.
 * Never offers merge execution, queue submission, or retargeting.
 */

import { useEffect, useId, useRef } from "react";
import { createPortal } from "react-dom";

import type { DeliveryGroupPreview } from "@/api/workspace/deliveryGroups";
import { useRegisterEscapeLayer, LAYER_CONFIRM_DIALOG } from "@/hooks";
import {
  formatObservedAtLine,
  readinessDisplay,
  shortPrKey,
  summarizeMergePreview,
} from "@/utils/pullRequest/readinessDisplay";
import { ReadinessBadge } from "./ReadinessBadge";
import styles from "./MergePreviewDialog.module.css";

export interface MergePreviewDialogProps {
  open: boolean;
  title: string;
  preview: DeliveryGroupPreview | null;
  loading: boolean;
  error: string | null;
  onClose: () => void;
}

export function MergePreviewDialog({
  open,
  title,
  preview,
  loading,
  error,
  onClose,
}: MergePreviewDialogProps): JSX.Element | null {
  const titleId = useId();
  const closeRef = useRef<HTMLButtonElement>(null);
  useRegisterEscapeLayer(LAYER_CONFIRM_DIALOG, onClose, open);

  useEffect(() => {
    if (open) closeRef.current?.focus();
  }, [open]);

  if (!open) return null;

  const summary = summarizeMergePreview(preview?.preview);
  const serverNow = preview?.server_now;

  return createPortal(
    <div
      className={styles.overlay}
      onClick={onClose}
      data-testid="merge-preview-overlay"
    >
      <div
        className={styles.dialog}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-label="Ordered merge preview"
        onClick={(e) => e.stopPropagation()}
      >
        <header className={styles.head}>
          <div>
            <h2 id={titleId} className={styles.title}>
              Ordered merge preview
            </h2>
            <p className={styles.sub}>{title} · read-only · no merge action</p>
          </div>
          <button
            ref={closeRef}
            type="button"
            className={styles.close}
            onClick={onClose}
            aria-label="Close"
          >
            ✕
          </button>
        </header>

        {loading && (
          <p className={styles.status} role="status">
            Refreshing readiness evidence…
          </p>
        )}
        {error && (
          <p className={styles.error} role="alert">
            {error}
          </p>
        )}

        {preview && (
          <div className={styles.body}>
            <p className={styles.summary} data-testid="merge-preview-summary">
              Selected prefix:{" "}
              <strong>{summary.readyCount}</strong> ready
              {summary.stopLabel ? (
                <>
                  {" · "}
                  {summary.stopLabel}
                </>
              ) : (
                " · no blocker in this prefix"
              )}
            </p>
            {summary.hasStaleMembers && (
              <p className={styles.warn} role="status">
                Stale or incomplete evidence for{" "}
                {summary.staleKeys.map(shortPrKey).join(", ")}. Stale
                observations are not currently Ready.
              </p>
            )}
            {(preview.repo_errors?.length ?? 0) > 0 && (
              <p className={styles.warn} role="status">
                Partial GitHub failure:{" "}
                {preview.repo_errors!
                  .map((e) => `${e.repo} (${e.code})`)
                  .join("; ")}
                . Persisted members are still shown.
              </p>
            )}
            <ol className={styles.list}>
              {preview.preview.members.map((m) => {
                const d = readinessDisplay(m.readiness);
                return (
                  <li
                    key={m.readiness.pr_key}
                    className={styles.row}
                    data-position={m.position}
                  >
                    <span className={styles.step}>{m.index + 1}</span>
                    <div className={styles.main}>
                      <code className={styles.key}>
                        {shortPrKey(m.readiness.pr_key)}
                      </code>
                      <ReadinessBadge display={d} />
                      <span className={styles.pos}>{m.position}</span>
                      <p className={styles.evidence}>
                        {formatObservedAtLine(d, serverNow)}
                      </p>
                      {m.reasons.length > 0 && (
                        <p className={styles.reasons}>{m.reasons.join("; ")}</p>
                      )}
                    </div>
                  </li>
                );
              })}
            </ol>
          </div>
        )}

        <footer className={styles.foot}>
          <p className={styles.note}>
            This preview never changes GitHub state. Merge execution and queue
            submission are out of scope.
          </p>
          <button type="button" className={styles.primary} onClick={onClose}>
            Close
          </button>
        </footer>
      </div>
    </div>,
    document.body,
  );
}
