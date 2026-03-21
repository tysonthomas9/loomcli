/**
 * useSplitView hook.
 * Manages split view state for side-by-side terminal comparison.
 */

import { useState, useCallback, useEffect } from "react";

import {
  DEFAULT_SPLIT_RATIO,
  MIN_SPLIT_WIDTH_PX,
  type TabState,
} from "./terminalTabUtils";

interface UseSplitViewOptions {
  tabs: TabState[];
  activeTabId: string;
}

export function useSplitView({ tabs, activeTabId }: UseSplitViewOptions) {
  const [isSplitView, setIsSplitView] = useState(() => {
    return sessionStorage.getItem("terminal-split-view") === "true";
  });
  const [splitRatio, setSplitRatio] = useState(() => {
    const stored = sessionStorage.getItem("terminal-split-ratio");
    return stored
      ? parseFloat(stored) || DEFAULT_SPLIT_RATIO
      : DEFAULT_SPLIT_RATIO;
  });
  const [rightPaneTabId, setRightPaneTabId] = useState(() => {
    return sessionStorage.getItem("terminal-split-right-tab") ?? "";
  });
  const [focusedPane, setFocusedPane] = useState<"left" | "right">("left");

  // Auto-disable below MIN_SPLIT_WIDTH_PX
  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const mql = window.matchMedia(`(min-width: ${MIN_SPLIT_WIDTH_PX}px)`);
    const handler = (e: MediaQueryListEvent) => {
      if (!e.matches) setIsSplitView(false);
    };
    mql.addEventListener("change", handler);
    if (!mql.matches) setIsSplitView(false);
    return () => mql.removeEventListener("change", handler);
  }, []);

  // Persist state to sessionStorage
  useEffect(() => {
    sessionStorage.setItem("terminal-split-view", String(isSplitView));
  }, [isSplitView]);

  useEffect(() => {
    if (rightPaneTabId) {
      sessionStorage.setItem("terminal-split-right-tab", rightPaneTabId);
    }
  }, [rightPaneTabId]);

  useEffect(() => {
    sessionStorage.setItem("terminal-split-ratio", String(splitRatio));
  }, [splitRatio]);

  // When activeTabId matches rightPaneTabId, auto-switch right pane
  useEffect(() => {
    if (!isSplitView) return;
    if (activeTabId && activeTabId === rightPaneTabId) {
      const other = tabs.find((t) => t.id !== activeTabId);
      if (other) setRightPaneTabId(other.id);
    }
  }, [activeTabId, rightPaneTabId, isSplitView, tabs]);

  // Handle tab removal: fallback or disable split
  useEffect(() => {
    if (!isSplitView) return;
    if (tabs.length < 2) {
      setIsSplitView(false);
      return;
    }
    if (rightPaneTabId && !tabs.find((t) => t.id === rightPaneTabId)) {
      const other = tabs.find((t) => t.id !== activeTabId);
      if (other) setRightPaneTabId(other.id);
      else setIsSplitView(false);
    }
  }, [tabs, rightPaneTabId, activeTabId, isSplitView]);

  const handleToggleSplit = useCallback(() => {
    setIsSplitView((prev) => {
      if (!prev) {
        const firstOther = tabs.find((t) => t.id !== activeTabId);
        if (firstOther) setRightPaneTabId(firstOther.id);
        return true;
      }
      return false;
    });
  }, [tabs, activeTabId]);

  const handleSplitRatioChange = useCallback((ratio: number) => {
    setSplitRatio(ratio);
  }, []);

  const handleRightPaneTabChange = useCallback(
    (tabId: string) => {
      if (tabId === activeTabId) return;
      setRightPaneTabId(tabId);
    },
    [activeTabId],
  );

  const canSplit = tabs.length >= 2;

  return {
    isSplitView,
    splitRatio,
    rightPaneTabId,
    focusedPane,
    setFocusedPane,
    canSplit,
    handleToggleSplit,
    handleSplitRatioChange,
    handleRightPaneTabChange,
  };
}
