/**
 * ToastContainer component.
 * Renders a stack of toasts in a fixed position on the screen.
 */

import type { Toast as ToastData } from '@/hooks/useToast';

import { Toast } from './Toast';
import styles from './Toast.module.css';

/**
 * Position options for the toast container.
 */
export type ToastPosition = 'top-right' | 'top-left' | 'bottom-right' | 'bottom-left';

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
  /** Additional CSS class */
  className?: string;
}

/**
 * ToastContainer renders all active toasts in a stacked layout.
 * Toasts stack from the chosen corner, with newest at the end of the stack.
 */
export function ToastContainer({
  toasts,
  onDismiss,
  position = 'bottom-right',
  className,
}: ToastContainerProps): JSX.Element {
  const positionClass =
    styles[position.replace('-', '') as keyof typeof styles] ?? styles.bottomright;

  const rootClassName = [styles.container, positionClass, className].filter(Boolean).join(' ');

  return (
    <div className={rootClassName} aria-label="Notifications" data-testid="toast-container">
      {toasts.map((toast) => (
        <Toast
          key={toast.id}
          id={toast.id}
          message={toast.message}
          type={toast.type}
          onDismiss={onDismiss}
        />
      ))}
    </div>
  );
}
