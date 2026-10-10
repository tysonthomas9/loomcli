/**
 * AetherModal — shared app dialog shell rendered via portal.
 */

import { useContext } from "react";
import { createPortal } from "react-dom";
import type { CSSProperties, ReactNode, RefObject } from "react";

import {
  EscapeRegistryContext,
  LAYER_MODAL,
  useRegisterEscapeLayer,
} from "@/hooks/ui/useKeyboardShortcuts";

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
  /** Escape calls onClose (default true); set false while closing is blocked. */
  closeOnEscape?: boolean;
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
  closeOnEscape = true,
}: AetherModalProps): JSX.Element | null {
  // Escape goes through the app's Escape layers (topmost wins). Outside a
  // KeyboardShortcutProvider (isolated tests) there is no registry.
  const hasEscapeRegistry = useContext(EscapeRegistryContext) !== null;
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
      {hasEscapeRegistry && closeOnEscape && <ModalEscape onClose={onClose} />}
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

function ModalEscape({ onClose }: { onClose: () => void }): null {
  useRegisterEscapeLayer(LAYER_MODAL, onClose, true);
  return null;
}

export { styles as aetherModalStyles };
