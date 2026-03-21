/**
 * useAnnounce hook.
 * Provides an announce function that sends messages to an aria-live region
 * for screen reader announcements. Debounces rapid sequential announcements.
 */

import { useCallback, useRef } from "react";

type Priority = "polite" | "assertive";

interface AnnounceEvent {
  message: string;
  priority: Priority;
}

/** Module-level event target for cross-component communication */
const announceTarget = new EventTarget();

/** Dispatch an announcement to the LiveRegion singleton */
function dispatchAnnounce(message: string, priority: Priority): void {
  announceTarget.dispatchEvent(
    new CustomEvent<AnnounceEvent>("announce", {
      detail: { message, priority },
    }),
  );
}

/**
 * Subscribe to announcement events.
 * Returns an unsubscribe function.
 */
export function onAnnounce(
  callback: (event: AnnounceEvent) => void,
): () => void {
  const handler = (e: Event) => {
    const detail = (e as CustomEvent<AnnounceEvent>).detail;
    callback(detail);
  };
  announceTarget.addEventListener("announce", handler);
  return () => announceTarget.removeEventListener("announce", handler);
}

/**
 * Hook that provides a debounced announce function for screen reader announcements.
 * Messages are dispatched to the LiveRegion singleton via a custom event.
 *
 * @param debounceMs - Debounce interval in ms (default 150)
 */
export function useAnnounce(debounceMs = 150): {
  announce: (message: string, priority?: Priority) => void;
} {
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const announce = useCallback(
    (message: string, priority: Priority = "polite") => {
      if (timerRef.current) {
        clearTimeout(timerRef.current);
      }
      timerRef.current = setTimeout(() => {
        dispatchAnnounce(message, priority);
        timerRef.current = null;
      }, debounceMs);
    },
    [debounceMs],
  );

  return { announce };
}
