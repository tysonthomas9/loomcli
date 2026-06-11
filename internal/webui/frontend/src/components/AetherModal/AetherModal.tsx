/**
 * AetherModal — shared app dialog shell rendered via portal.
 */

import { createPortal } from "react-dom";
import type { ReactNode, RefObject } from "react";

import styles from "./AetherModal.module.css";

export interface AetherModalProps {
  isOpen: boolean;
  title: string;
  ariaLabel?: string;
  onClose: () => void;
  /** Defaults to onClose when backdrop dismiss is enabled. */
  onOverlayClick?: () => void;
  /** When true, clicking the backdrop does nothing. */
  disableOverlayDismiss?: boolean;
  children: ReactNode;
  footer?: ReactNode;
  dialogRef?: RefObject<HTMLDivElement>;
  overlayTestId?: string;
  closeTestId?: string;
  showCloseButton?: boolean;
}

export function AetherModal({
  isOpen,
  title,
  ariaLabel,
  onClose,
  onOverlayClick,
  disableOverlayDismiss = false,
  children,
  footer,
  dialogRef,
  overlayTestId,
  closeTestId,
  showCloseButton = true,
}: AetherModalProps): JSX.Element | null {
  if (!isOpen) return null;

  const handleOverlayClick = disableOverlayDismiss
    ? undefined
    : (onOverlayClick ?? onClose);

  return createPortal(
    <div
      className={styles.overlay}
      onClick={handleOverlayClick}
      data-testid={overlayTestId}
    >
      <div
        ref={dialogRef}
        className={styles.dialog}
        role="dialog"
        aria-modal="true"
        aria-label={ariaLabel ?? title}
        onClick={(event) => event.stopPropagation()}
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
    </div>,
    document.body,
  );
}

export { styles as aetherModalStyles };
