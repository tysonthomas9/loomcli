/**
 * ToastContainer component.
 * Renders a stack of toasts in a fixed position on the screen.
 */

import type { Toast as ToastData } from "@/hooks/ui";

import { Toast } from "./Toast";
import styles from "./Toast.module.css";

/**
 * Position options for the toast container.
 */
export type ToastPosition =
  | "top-right"
  | "top-left"
  | "bottom-right"
  | "bottom-left";

/**
 * Props for the ToastContainer component.
 */
export interface ToastContainerProps {
  /** Active toasts to render */
  toasts: ToastData[];
  /** Callback when a toast is dismissed */
  onDismiss: (id: string) => void;
  /** Position on screen (default: 'bottom-right') */
  position?: ToastPosition;
  /** Maximum visible toasts (default: 3) */
  maxVisible?: number;
  /** Additional CSS class */
  className?: string;
}

/**
 * ToastContainer renders the newest active toasts in a stacked layout.
 * Toasts stack from the chosen corner, with newest at the end of the stack.
 */
export function ToastContainer({
  toasts,
  onDismiss,
  position = "bottom-right",
  maxVisible = 3,
  className,
}: ToastContainerProps): JSX.Element {
  const positionClass =
    styles[position.replace("-", "") as keyof typeof styles] ??
    styles.bottomright;

  const rootClassName = [styles.container, positionClass, className]
    .filter(Boolean)
    .join(" ");
  const visibleCount = Math.max(0, maxVisible);
  const visibleToasts = toasts.slice(Math.max(0, toasts.length - visibleCount));

  return (
    <div
      className={rootClassName}
      aria-label="Notifications"
      data-testid="toast-container"
    >
      {visibleToasts.map((toast) => (
        <Toast
          key={toast.id}
          id={toast.id}
          message={toast.message}
          type={toast.type}
          onDismiss={onDismiss}
          {...(toast.onUndo ? { onUndo: toast.onUndo } : {})}
        />
      ))}
    </div>
  );
}
