import { useCallback, useEffect, useRef } from "react";

/**
 * Returns a debounced version of the given callback.
 * The callback is delayed by `delay` ms; rapid successive calls reset the timer
 * so only the last invocation fires. Cleans up on unmount.
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function useDebouncedCallback<T extends (...args: any[]) => void>(
  callback: T,
  delay: number,
): T {
  const callbackRef = useRef(callback);
  const timeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Always keep the latest callback so the debounced wrapper never fires a stale closure.
  callbackRef.current = callback;

  // Cleanup on unmount or when delay changes (cancels stale in-flight timer)
  useEffect(() => {
    return () => {
      if (timeoutRef.current !== null) {
        clearTimeout(timeoutRef.current);
      }
    };
  }, [delay]);

  // Return a stable function reference
  return useCallback(
    ((...args: Parameters<T>) => {
      if (timeoutRef.current !== null) {
        clearTimeout(timeoutRef.current);
      }
      timeoutRef.current = setTimeout(() => {
        timeoutRef.current = null;
        callbackRef.current(...args);
      }, delay);
    }) as T,
    [delay],
  );
}
