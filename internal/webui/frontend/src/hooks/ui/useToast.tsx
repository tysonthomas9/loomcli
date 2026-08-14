/**
 * useToast - React context and hook for toast notifications.
 * Provides a centralized API for showing, dismissing, and managing toast notifications.
 */

import {
  createContext,
  useContext,
  useReducer,
  useCallback,
  useEffect,
  useRef,
  type ReactNode,
} from "react";

/**
 * Toast type for styling.
 */
export type ToastType = "success" | "error" | "warning" | "info";

/**
 * Type-specific default durations in milliseconds.
 */
const defaultDurations: Record<ToastType, number> = {
  success: 4000,
  error: 10000,
  warning: 5000,
  info: 5000,
};

/**
 * Options for showing a toast.
 */
export interface ToastOptions {
  /** Toast type for styling (default: 'info') */
  type?: ToastType;
  /** Auto-dismiss duration in ms (default: type-specific, 0 = no auto-dismiss) */
  duration?: number;
  /** Callback for undo action on destructive toasts */
  onUndo?: () => void;
}

/**
 * Individual toast data.
 */
export interface Toast {
  id: string;
  message: string;
  type: ToastType;
  duration: number;
  onUndo?: () => void;
}

/**
 * Context value exposed by ToastProvider.
 */
export interface ToastContextValue {
  /** Active toasts */
  toasts: Toast[];
  /** Show a toast notification */
  showToast: (message: string, options?: ToastOptions) => string;
  /** Dismiss a specific toast by ID */
  dismissToast: (id: string) => void;
  /** Dismiss all toasts */
  dismissAll: () => void;
}

// Generate unique IDs for toasts
let nextId = 0;
function generateId(): string {
  return `toast-${nextId++}-${Date.now()}`;
}

// Toast reducer actions
type ToastAction =
  | { type: "ADD"; payload: Toast; maxToasts: number }
  | { type: "REMOVE"; payload: string }
  | { type: "CLEAR" };

function toastReducer(state: Toast[], action: ToastAction): Toast[] {
  switch (action.type) {
    case "ADD": {
      const last = state[state.length - 1];
      if (
        last &&
        last.message === action.payload.message &&
        last.type === action.payload.type
      ) {
        return state;
      }
      const next = [...state, action.payload];
      return next.length > action.maxToasts
        ? next.slice(next.length - action.maxToasts)
        : next;
    }
    case "REMOVE":
      return state.filter((toast) => toast.id !== action.payload);
    case "CLEAR":
      return [];
    default:
      return state;
  }
}

/** No-op fallback for components rendered outside ToastProvider (e.g. unit tests). */
const noopToastContext: ToastContextValue = {
  toasts: [],
  showToast: () => "",
  dismissToast: () => {},
  dismissAll: () => {},
};

const ToastContext = createContext<ToastContextValue>(noopToastContext);

/**
 * Props for ToastProvider.
 */
export interface ToastProviderProps {
  children: ReactNode;
  /** Maximum number of visible toasts (default: 3) */
  maxToasts?: number;
}

/**
 * ToastProvider wraps the app and provides toast context to all children.
 */
export function ToastProvider({
  children,
  maxToasts = 3,
}: ToastProviderProps): JSX.Element {
  const [toasts, dispatch] = useReducer(toastReducer, []);
  const toastsRef = useRef<Toast[]>([]);

  // Track auto-dismiss timeouts
  const timeoutsRef = useRef<Map<string, ReturnType<typeof setTimeout>>>(
    new Map(),
  );

  useEffect(() => {
    toastsRef.current = toasts;
  }, [toasts]);

  // Cleanup all timeouts on unmount
  useEffect(() => {
    const timeouts = timeoutsRef.current;
    return () => {
      for (const timeoutId of timeouts.values()) {
        clearTimeout(timeoutId);
      }
      timeouts.clear();
    };
  }, []);

  const dismissToast = useCallback((id: string) => {
    // Clear the timeout if it exists
    const timeoutId = timeoutsRef.current.get(id);
    if (timeoutId) {
      clearTimeout(timeoutId);
      timeoutsRef.current.delete(id);
    }
    toastsRef.current = toastsRef.current.filter((toast) => toast.id !== id);
    dispatch({ type: "REMOVE", payload: id });
  }, []);

  const showToast = useCallback(
    (message: string, options?: ToastOptions): string => {
      const type = options?.type ?? "info";
      const duration = options?.duration ?? defaultDurations[type];
      const activeToasts = toastsRef.current;
      const last = activeToasts[activeToasts.length - 1];
      if (last && last.message === message && last.type === type) {
        return last.id;
      }

      const id = generateId();
      const toast: Toast = { id, message, type, duration };
      if (options?.onUndo) {
        toast.onUndo = options.onUndo;
      }

      const nextToasts = [...activeToasts, toast];
      const overflowCount = Math.max(0, nextToasts.length - maxToasts);
      for (const overflowToast of nextToasts.slice(0, overflowCount)) {
        const timeoutId = timeoutsRef.current.get(overflowToast.id);
        if (timeoutId) {
          clearTimeout(timeoutId);
          timeoutsRef.current.delete(overflowToast.id);
        }
      }
      toastsRef.current =
        overflowCount > 0 ? nextToasts.slice(overflowCount) : nextToasts;

      dispatch({ type: "ADD", payload: toast, maxToasts });

      // Set up auto-dismiss if duration > 0
      if (duration > 0) {
        const timeoutId = setTimeout(() => {
          dismissToast(id);
        }, duration);
        timeoutsRef.current.set(id, timeoutId);
      }

      return id;
    },
    [dismissToast, maxToasts],
  );

  const dismissAll = useCallback(() => {
    // Clear all timeouts
    for (const timeoutId of timeoutsRef.current.values()) {
      clearTimeout(timeoutId);
    }
    timeoutsRef.current.clear();
    toastsRef.current = [];
    dispatch({ type: "CLEAR" });
  }, []);

  // Enforce maxToasts limit - remove oldest when exceeded
  // Note: We use toasts.length to trigger, but read toasts via ref-like access
  // to avoid infinite loops (dismissToast changes toasts, which would retrigger)
  useEffect(() => {
    if (toasts.length > maxToasts) {
      // Get the IDs to remove (oldest first)
      const idsToRemove = toasts
        .slice(0, toasts.length - maxToasts)
        .map((t) => t.id);
      for (const id of idsToRemove) {
        dismissToast(id);
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- toasts.length is sufficient
  }, [toasts.length, maxToasts, dismissToast]);

  const value: ToastContextValue = {
    toasts,
    showToast,
    dismissToast,
    dismissAll,
  };

  return (
    <ToastContext.Provider value={value}>{children}</ToastContext.Provider>
  );
}

/**
 * Hook to access toast context.
 * Must be used within a ToastProvider.
 *
 * @example
 * ```tsx
 * function MyComponent() {
 *   const { showToast } = useToast()
 *
 *   const handleSave = async () => {
 *     try {
 *       await saveData()
 *       showToast('Saved successfully!', { type: 'success' })
 *     } catch (err) {
 *       showToast('Failed to save', { type: 'error' })
 *     }
 *   }
 *
 *   return <button onClick={handleSave}>Save</button>
 * }
 * ```
 */
export function useToast(): ToastContextValue {
  return useContext(ToastContext);
}
