import { useState, useCallback, useEffect, useMemo, useRef } from "react";

interface UseUnreadTrackingOptions {
  activeTabIdRef: React.MutableRefObject<string>;
  isActive: boolean | undefined;
  onUnreadChange?: ((hasAnyUnread: boolean) => void) | undefined;
}

interface UseUnreadTrackingReturn {
  tabUnread: Map<string, boolean>;
  handleOutput: (tabId: string) => void;
  clearTabUnread: (tabId: string) => void;
}

export function useUnreadTracking({
  activeTabIdRef,
  isActive,
  onUnreadChange,
}: UseUnreadTrackingOptions): UseUnreadTrackingReturn {
  const [tabUnread, setTabUnread] = useState<Map<string, boolean>>(
    () => new Map(),
  );

  const handleOutput = useCallback(
    (tabId: string) => {
      if (tabId !== activeTabIdRef.current) {
        setTabUnread((prev) => {
          if (prev.get(tabId)) return prev;
          const next = new Map(prev);
          next.set(tabId, true);
          return next;
        });
      }
    },
    [activeTabIdRef],
  );

  const clearTabUnread = useCallback((tabId: string) => {
    setTabUnread((prev) => {
      if (!prev.get(tabId)) return prev;
      const next = new Map(prev);
      next.delete(tabId);
      return next;
    });
  }, []);

  // Compute aggregate unread and notify parent
  const hasAnyUnread = useMemo(() => {
    for (const val of tabUnread.values()) {
      if (val) return true;
    }
    return false;
  }, [tabUnread]);

  useEffect(() => {
    onUnreadChange?.(hasAnyUnread);
  }, [hasAnyUnread, onUnreadChange]);

  // When view becomes active, clear unread on the currently active tab
  const prevIsActiveRef = useRef(isActive);
  useEffect(() => {
    if (isActive && !prevIsActiveRef.current) {
      clearTabUnread(activeTabIdRef.current);
    }
    prevIsActiveRef.current = isActive;
  }, [isActive, activeTabIdRef, clearTabUnread]);

  return {
    tabUnread,
    handleOutput,
    clearTabUnread,
  };
}
