/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useTerminalKeyboardShortcuts hook.
 */

import { renderHook } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fireEvent } from "@testing-library/react";
import type { MutableRefObject } from "react";

import { useTerminalKeyboardShortcuts } from "../useTerminalKeyboardShortcuts";
import type { TabState } from "../terminalTabUtils";
import { MAX_TABS } from "../terminalTabUtils";

function makeTab(id: string, label?: string): TabState {
  return {
    id,
    label: label ?? id,
    sessionName: id,
    connectionState: "connected",
    backendName: "claude",
  };
}

function makeTabs(count: number): TabState[] {
  return Array.from({ length: count }, (_, i) =>
    makeTab(`tab-${i + 1}`, `Terminal ${i + 1}`),
  );
}

interface CreateOptionsOverrides {
  isActive?: boolean;
  tabs?: TabState[];
  activeTabId?: string;
  isSearchOpen?: boolean;
  isSessionPromptOpen?: boolean;
  pendingPasteText?: string | null;
  dismissedWelcome?: boolean;
  onCycleTab?: (direction: "forward" | "backward") => void;
  onSwitchTabByIndex?: (index: number) => void;
  onNewTab?: () => void;
  onCloseTab?: () => void;
  onToggleSearch?: () => void;
  onEscape?: (() => void) | undefined;
  announce?: (msg: string) => void;
}

function createOptions(overrides: CreateOptionsOverrides = {}) {
  const tabs = overrides.tabs ?? makeTabs(3);
  const activeTabId = overrides.activeTabId ?? "tab-1";
  return {
    isActive: overrides.isActive ?? true,
    tabsRef: { current: tabs } as MutableRefObject<TabState[]>,
    activeTabIdRef: { current: activeTabId } as MutableRefObject<string>,
    isSearchOpen: overrides.isSearchOpen ?? false,
    isSessionPromptOpen: overrides.isSessionPromptOpen ?? false,
    pendingPasteText: overrides.pendingPasteText ?? null,
    dismissedWelcome: overrides.dismissedWelcome ?? true,
    onCycleTab: overrides.onCycleTab ?? vi.fn(),
    onSwitchTabByIndex: overrides.onSwitchTabByIndex ?? vi.fn(),
    onNewTab: overrides.onNewTab ?? vi.fn(),
    onCloseTab: overrides.onCloseTab ?? vi.fn(),
    onToggleSearch: overrides.onToggleSearch ?? vi.fn(),
    onEscape: overrides.onEscape ?? vi.fn(),
    announce: overrides.announce ?? vi.fn(),
  };
}

describe("useTerminalKeyboardShortcuts", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("Ctrl+Tab / Ctrl+Shift+Tab: cycle tabs", () => {
    it("fires onCycleTab('forward') on Ctrl+Tab", () => {
      const onCycleTab = vi.fn();
      renderHook(() =>
        useTerminalKeyboardShortcuts(createOptions({ onCycleTab })),
      );

      fireEvent.keyDown(document, { key: "Tab", ctrlKey: true });

      expect(onCycleTab).toHaveBeenCalledWith("forward");
    });

    it("fires onCycleTab('backward') on Ctrl+Shift+Tab", () => {
      const onCycleTab = vi.fn();
      renderHook(() =>
        useTerminalKeyboardShortcuts(createOptions({ onCycleTab })),
      );

      fireEvent.keyDown(document, {
        key: "Tab",
        ctrlKey: true,
        shiftKey: true,
      });

      expect(onCycleTab).toHaveBeenCalledWith("backward");
    });

    it("fires onCycleTab('forward') on Alt+ArrowRight", () => {
      const onCycleTab = vi.fn();
      renderHook(() =>
        useTerminalKeyboardShortcuts(createOptions({ onCycleTab })),
      );

      fireEvent.keyDown(document, { key: "ArrowRight", altKey: true });

      expect(onCycleTab).toHaveBeenCalledWith("forward");
    });

    it("fires onCycleTab('backward') on Alt+ArrowLeft", () => {
      const onCycleTab = vi.fn();
      renderHook(() =>
        useTerminalKeyboardShortcuts(createOptions({ onCycleTab })),
      );

      fireEvent.keyDown(document, { key: "ArrowLeft", altKey: true });

      expect(onCycleTab).toHaveBeenCalledWith("backward");
    });
  });

  describe("Cmd/Ctrl+1-9: switch tabs by index", () => {
    it("fires onSwitchTabByIndex(0) and announce on Cmd+1", () => {
      const onSwitchTabByIndex = vi.fn();
      const announce = vi.fn();
      renderHook(() =>
        useTerminalKeyboardShortcuts(
          createOptions({ onSwitchTabByIndex, announce }),
        ),
      );

      fireEvent.keyDown(document, { key: "1", metaKey: true });

      expect(onSwitchTabByIndex).toHaveBeenCalledWith(0);
      expect(announce).toHaveBeenCalledWith("Switched to tab Terminal 1");
    });

    it("fires onSwitchTabByIndex(8) and announce on Cmd+9 when 9 tabs exist", () => {
      const onSwitchTabByIndex = vi.fn();
      const announce = vi.fn();
      const tabs = Array.from({ length: 9 }, (_, i) =>
        makeTab(`tab-${i + 1}`, `Terminal ${i + 1}`),
      );
      renderHook(() =>
        useTerminalKeyboardShortcuts(
          createOptions({ onSwitchTabByIndex, announce, tabs }),
        ),
      );

      fireEvent.keyDown(document, { key: "9", metaKey: true });

      expect(onSwitchTabByIndex).toHaveBeenCalledWith(8);
      expect(announce).toHaveBeenCalledWith("Switched to tab Terminal 9");
    });

    it("does not fire for index beyond available tabs", () => {
      const onSwitchTabByIndex = vi.fn();
      const announce = vi.fn();
      renderHook(() =>
        useTerminalKeyboardShortcuts(
          createOptions({
            onSwitchTabByIndex,
            announce,
            tabs: makeTabs(2),
          }),
        ),
      );

      fireEvent.keyDown(document, { key: "5", metaKey: true });

      expect(onSwitchTabByIndex).not.toHaveBeenCalled();
      expect(announce).not.toHaveBeenCalled();
    });

    it("also works with ctrlKey", () => {
      const onSwitchTabByIndex = vi.fn();
      const announce = vi.fn();
      renderHook(() =>
        useTerminalKeyboardShortcuts(
          createOptions({ onSwitchTabByIndex, announce }),
        ),
      );

      fireEvent.keyDown(document, { key: "2", ctrlKey: true });

      expect(onSwitchTabByIndex).toHaveBeenCalledWith(1);
      expect(announce).toHaveBeenCalledWith("Switched to tab Terminal 2");
    });
  });

  describe("Cmd/Ctrl+T: new tab", () => {
    it("fires onNewTab when tabs < MAX_TABS", () => {
      const onNewTab = vi.fn();
      renderHook(() =>
        useTerminalKeyboardShortcuts(
          createOptions({ onNewTab, tabs: makeTabs(3) }),
        ),
      );

      fireEvent.keyDown(document, { key: "t", metaKey: true });

      expect(onNewTab).toHaveBeenCalledTimes(1);
    });

    it("does not fire onNewTab when tabs >= MAX_TABS", () => {
      const onNewTab = vi.fn();
      renderHook(() =>
        useTerminalKeyboardShortcuts(
          createOptions({ onNewTab, tabs: makeTabs(MAX_TABS) }),
        ),
      );

      fireEvent.keyDown(document, { key: "t", metaKey: true });

      expect(onNewTab).not.toHaveBeenCalled();
    });
  });

  describe("Cmd/Ctrl+W: close tab", () => {
    it("fires onCloseTab when tabs > 1", () => {
      const onCloseTab = vi.fn();
      const announce = vi.fn();
      renderHook(() =>
        useTerminalKeyboardShortcuts(
          createOptions({
            onCloseTab,
            announce,
            tabs: makeTabs(3),
            activeTabId: "tab-2",
          }),
        ),
      );

      fireEvent.keyDown(document, { key: "w", metaKey: true });

      expect(onCloseTab).toHaveBeenCalledTimes(1);
      expect(announce).toHaveBeenCalledWith("Tab Terminal 2 closed");
    });

    it("does not fire onCloseTab when only 1 tab", () => {
      const onCloseTab = vi.fn();
      renderHook(() =>
        useTerminalKeyboardShortcuts(
          createOptions({ onCloseTab, tabs: makeTabs(1) }),
        ),
      );

      fireEvent.keyDown(document, { key: "w", metaKey: true });

      expect(onCloseTab).not.toHaveBeenCalled();
    });
  });

  describe("Cmd/Ctrl+F: toggle search", () => {
    it("fires onToggleSearch", () => {
      const onToggleSearch = vi.fn();
      renderHook(() =>
        useTerminalKeyboardShortcuts(createOptions({ onToggleSearch })),
      );

      fireEvent.keyDown(document, { key: "f", metaKey: true });

      expect(onToggleSearch).toHaveBeenCalledTimes(1);
    });
  });

  describe("Escape: return to previous view", () => {
    it("fires onEscape when nothing else is open", () => {
      const onEscape = vi.fn();
      renderHook(() =>
        useTerminalKeyboardShortcuts(
          createOptions({
            onEscape,
            isSearchOpen: false,
            isSessionPromptOpen: false,
            pendingPasteText: null,
            dismissedWelcome: true,
          }),
        ),
      );

      fireEvent.keyDown(document, { key: "Escape" });

      expect(onEscape).toHaveBeenCalledTimes(1);
    });

    it("does not fire onEscape when isSearchOpen is true", () => {
      const onEscape = vi.fn();
      renderHook(() =>
        useTerminalKeyboardShortcuts(
          createOptions({ onEscape, isSearchOpen: true }),
        ),
      );

      fireEvent.keyDown(document, { key: "Escape" });

      expect(onEscape).not.toHaveBeenCalled();
    });

    it("does not fire onEscape when isSessionPromptOpen is true", () => {
      const onEscape = vi.fn();
      renderHook(() =>
        useTerminalKeyboardShortcuts(
          createOptions({ onEscape, isSessionPromptOpen: true }),
        ),
      );

      fireEvent.keyDown(document, { key: "Escape" });

      expect(onEscape).not.toHaveBeenCalled();
    });

    it("does not fire onEscape when pendingPasteText is not null", () => {
      const onEscape = vi.fn();
      renderHook(() =>
        useTerminalKeyboardShortcuts(
          createOptions({ onEscape, pendingPasteText: "some text" }),
        ),
      );

      fireEvent.keyDown(document, { key: "Escape" });

      expect(onEscape).not.toHaveBeenCalled();
    });

    it("does not fire onEscape when dismissedWelcome is false", () => {
      const onEscape = vi.fn();
      renderHook(() =>
        useTerminalKeyboardShortcuts(
          createOptions({ onEscape, dismissedWelcome: false }),
        ),
      );

      fireEvent.keyDown(document, { key: "Escape" });

      expect(onEscape).not.toHaveBeenCalled();
    });

    it("does not throw when onEscape is undefined", () => {
      renderHook(() =>
        useTerminalKeyboardShortcuts(createOptions({ onEscape: undefined })),
      );

      expect(() => {
        fireEvent.keyDown(document, { key: "Escape" });
      }).not.toThrow();
    });
  });

  describe("isActive suppression", () => {
    it("does not fire any shortcuts when isActive is false", () => {
      const onCycleTab = vi.fn();
      const onSwitchTabByIndex = vi.fn();
      const onNewTab = vi.fn();
      const onCloseTab = vi.fn();
      const onToggleSearch = vi.fn();
      const onEscape = vi.fn();

      renderHook(() =>
        useTerminalKeyboardShortcuts(
          createOptions({
            isActive: false,
            onCycleTab,
            onSwitchTabByIndex,
            onNewTab,
            onCloseTab,
            onToggleSearch,
            onEscape,
          }),
        ),
      );

      fireEvent.keyDown(document, { key: "Tab", ctrlKey: true });
      fireEvent.keyDown(document, { key: "1", metaKey: true });
      fireEvent.keyDown(document, { key: "t", metaKey: true });
      fireEvent.keyDown(document, { key: "w", metaKey: true });
      fireEvent.keyDown(document, { key: "f", metaKey: true });
      fireEvent.keyDown(document, { key: "Escape" });

      expect(onCycleTab).not.toHaveBeenCalled();
      expect(onSwitchTabByIndex).not.toHaveBeenCalled();
      expect(onNewTab).not.toHaveBeenCalled();
      expect(onCloseTab).not.toHaveBeenCalled();
      expect(onToggleSearch).not.toHaveBeenCalled();
      expect(onEscape).not.toHaveBeenCalled();
    });
  });

  describe("cleanup", () => {
    it("removes event listener on unmount", () => {
      const onCycleTab = vi.fn();
      const { unmount } = renderHook(() =>
        useTerminalKeyboardShortcuts(createOptions({ onCycleTab })),
      );

      unmount();

      fireEvent.keyDown(document, { key: "Tab", ctrlKey: true });

      expect(onCycleTab).not.toHaveBeenCalled();
    });
  });
});
