/**
 * AetherModal — shared app dialog shell rendered via portal.
 */

import { createPortal } from "react-dom";
import type { CSSProperties, ReactNode, RefObject } from "react";

import styles from "./AetherModal.module.css";

const DEFAULT_OVERLAY_DISMISS_BUFFER_PX = 32;

export interface AetherModalProps {
  isOpen: boolean;
  title: string;
  ariaLabel?: string;
  onClose: () => void;
  /** Defaults to onClose when backdrop dismiss is enabled. */
  onOverlayClick?: () => void;
  /** When true, clicking the backdrop does nothing. */
  disableOverlayDismiss?: boolean;
  /** Non-dismiss padding around the dialog; clicks inside this ring are ignored. */
  overlayDismissBufferPx?: number;
  children: ReactNode;
  footer?: ReactNode;
  dialogRef?: RefObject<HTMLDivElement>;
  overlayTestId?: string;
  closeTestId?: string;
  showCloseButton?: boolean;
  /** Extra class names merged onto the dialog element (e.g. wide variant). */
  dialogClassName?: string | undefined;
}

export function AetherModal({
  isOpen,
  title,
  ariaLabel,
  onClose,
  onOverlayClick,
  disableOverlayDismiss = false,
  overlayDismissBufferPx = DEFAULT_OVERLAY_DISMISS_BUFFER_PX,
  children,
  footer,
  dialogRef,
  overlayTestId,
  closeTestId,
  showCloseButton = true,
  dialogClassName,
}: AetherModalProps): JSX.Element | null {
  if (!isOpen) return null;

  const handleOverlayClick = disableOverlayDismiss
    ? undefined
    : (onOverlayClick ?? onClose);

  const dialogShellStyle = {
    "--aether-modal-dismiss-buffer": `${overlayDismissBufferPx}px`,
  } as CSSProperties;

  return createPortal(
    <div
      className={styles.overlay}
      onClick={handleOverlayClick}
      data-testid={overlayTestId}
    >
      <div
        className={styles.dialogShell}
        style={dialogShellStyle}
        onClick={(event) => event.stopPropagation()}
      >
        <div
          ref={dialogRef}
          className={[styles.dialog, dialogClassName].filter(Boolean).join(" ")}
          role="dialog"
          aria-modal="true"
          aria-label={ariaLabel ?? title}
        >
          <header className={styles.head}>
            <h2 className={styles.title}>{title}</h2>
            {showCloseButton && (
              <button
                type="button"
                className={styles.closeButton}
                onClick={onClose}
                aria-label="Close"
                data-testid={closeTestId}
              >
                &times;
              </button>
            )}
          </header>
          <div className={styles.body}>{children}</div>
          {footer ? <footer className={styles.foot}>{footer}</footer> : null}
        </div>
      </div>
    </div>,
    document.body,
  );
}

export { styles as aetherModalStyles };
