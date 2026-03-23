import { useRef, useCallback, useEffect } from "react";

/**
 * A hook that returns a debounced version of the provided callback.
 * The callback will only be invoked after the specified delay has passed
 * since the last call. Rapid successive calls reset the timer.
 *
 * The returned function is stable across re-renders. The latest callback
 * is always used (not a stale closure).
 *
 * Cleans up any pending timeout on unmount.
 *
 * @param callback - The function to debounce
 * @param delay - Debounce delay in milliseconds
 * @returns A debounced version of the callback
 */
export function useDebouncedCallback<T extends (...args: unknown[]) => void>(
  callback: T,
  delay: number,
): (...args: Parameters<T>) => void {
  const callbackRef = useRef(callback);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  callbackRef.current = callback;

  useEffect(() => {
    return () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current);
        timerRef.current = null;
      }
    };
  }, []);

  return useCallback(
    (...args: Parameters<T>) => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current);
      }
      timerRef.current = setTimeout(() => {
        timerRef.current = null;
        callbackRef.current(...args);
      }, delay);
    },
    [delay],
  );
}
