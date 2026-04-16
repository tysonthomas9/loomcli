import { useEffect, useRef, type MutableRefObject } from "react";

import type { TabState } from "@/components/TerminalView/tabs";
import { MAX_TABS } from "@/components/TerminalView/tabs";

export interface UseTerminalKeyboardShortcutsOptions {
  isActive: boolean;
  tabsRef: MutableRefObject<TabState[]>;
  activeTabIdRef: MutableRefObject<string>;
  isSessionPromptOpen: boolean;
  dismissedWelcome: boolean;
  onCycleTab: (direction: "forward" | "backward") => void;
  onSwitchTabByIndex: (index: number) => void;
  onNewTab: () => void;
  onCloseTab: () => void;
  onEscape: (() => void) | undefined;
  announce: (msg: string) => void;
}

export function useTerminalKeyboardShortcuts({
  isActive,
  tabsRef,
  activeTabIdRef,
  isSessionPromptOpen,
  dismissedWelcome,
  onCycleTab,
  onSwitchTabByIndex,
  onNewTab,
  onCloseTab,
  onEscape,
  announce,
}: UseTerminalKeyboardShortcutsOptions): void {
  const onCycleTabRef = useRef(onCycleTab);
  onCycleTabRef.current = onCycleTab;
  const onSwitchTabByIndexRef = useRef(onSwitchTabByIndex);
  onSwitchTabByIndexRef.current = onSwitchTabByIndex;
  const onNewTabRef = useRef(onNewTab);
  onNewTabRef.current = onNewTab;
  const onCloseTabRef = useRef(onCloseTab);
  onCloseTabRef.current = onCloseTab;
  const onEscapeRef = useRef(onEscape);
  onEscapeRef.current = onEscape;
  const announceRef = useRef(announce);
  announceRef.current = announce;

  useEffect(() => {
    if (!isActive) return;
    const handler = (e: KeyboardEvent) => {
      // Ctrl+Tab / Ctrl+Shift+Tab: cycle tabs
      // Alt+ArrowRight / Alt+ArrowLeft: Firefox-compatible fallback
      if (
        (e.ctrlKey && e.key === "Tab") ||
        (e.altKey && (e.key === "ArrowRight" || e.key === "ArrowLeft"))
      ) {
        e.preventDefault();
        const goForward =
          e.key === "Tab" ? !e.shiftKey : e.key === "ArrowRight";
        onCycleTabRef.current(goForward ? "forward" : "backward");
        return;
      }

      // Escape: return to previous view when nothing else to dismiss
      if (
        e.key === "Escape" &&
        !isSessionPromptOpen &&
        dismissedWelcome
      ) {
        e.preventDefault();
        onEscapeRef.current?.();
        return;
      }

      // Cmd/Ctrl+1-9: switch tabs by index
      if (
        (e.metaKey || e.ctrlKey) &&
        !e.shiftKey &&
        !e.altKey &&
        e.key >= "1" &&
        e.key <= "9"
      ) {
        e.preventDefault();
        const index = parseInt(e.key) - 1;
        const currentTabs = tabsRef.current;
        const targetTab = currentTabs[index];
        if (targetTab) {
          onSwitchTabByIndexRef.current(index);
          announceRef.current(`Switched to tab ${targetTab.label}`);
        }
        return;
      }

      // Cmd/Ctrl+T: new tab
      if (
        (e.metaKey || e.ctrlKey) &&
        !e.shiftKey &&
        !e.altKey &&
        e.key === "t"
      ) {
        e.preventDefault();
        if (tabsRef.current.length < MAX_TABS) {
          onNewTabRef.current();
        }
        return;
      }

      // Cmd/Ctrl+W: close active tab
      if (
        (e.metaKey || e.ctrlKey) &&
        !e.shiftKey &&
        !e.altKey &&
        e.key === "w"
      ) {
        e.preventDefault();
        const currentTabs = tabsRef.current;
        if (currentTabs.length > 1) {
          const closedTab = currentTabs.find(
            (t) => t.id === activeTabIdRef.current,
          );
          onCloseTabRef.current();
          if (closedTab) announceRef.current(`Tab ${closedTab.label} closed`);
        }
        return;
      }

      // Cmd+F / Ctrl+F intentionally NOT intercepted — browser's native
      // find-in-page works against wterm's DOM-rendered cells.
    };
    document.addEventListener("keydown", handler);
    return () => document.removeEventListener("keydown", handler);
  }, [
    isActive,
    isSessionPromptOpen,
    dismissedWelcome,
    tabsRef,
    activeTabIdRef,
  ]);
}
