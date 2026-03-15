/**
 * Global keyboard shortcut system.
 *
 * Provides a centralized KeyboardShortcutProvider that handles:
 * - View switching via number keys (1-6, 0)
 * - Escape key priority chain via a self-contained layer registry
 * - Cmd/Ctrl+K to focus search
 * - ? to open/close keyboard cheatsheet
 * - Arrow key navigation delegation
 *
 * All shortcuts are suppressed when focus is inside an input, textarea,
 * contenteditable, CodeMirror editor, or xterm terminal — except for
 * Escape and Cmd/Ctrl+K which always work.
 *
 * The Escape layer system is self-contained and works without the provider
 * (e.g., in tests). The provider adds view switching, search focus, and
 * cheatsheet functionality.
 */

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";

import type { ViewMode } from "@/components/ViewSwitcher";

// ---------------------------------------------------------------------------
// Layer priorities (higher = closes first on Escape)
// ---------------------------------------------------------------------------
export const LAYER_CONFIRM_DIALOG = 60;
export const LAYER_TOAST = 50;
export const LAYER_CHEATSHEET = 45;
export const LAYER_MODAL = 40;
export const LAYER_TERMINAL_PANEL = 30;
export const LAYER_AGENT_PANEL = 20;
export const LAYER_ISSUE_PANEL = 10;
export const LAYER_TERMINAL_SEARCH = 5;

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------
interface Layer {
  id: string;
  priority: number;
  handler: () => void;
}

interface KeyboardShortcutContextValue {
  isCheatsheetOpen: boolean;
  toggleCheatsheet: () => void;
  closeCheatsheet: () => void;
}

// ---------------------------------------------------------------------------
// Context
// ---------------------------------------------------------------------------
const KeyboardShortcutContext = createContext<KeyboardShortcutContextValue>({
  isCheatsheetOpen: false,
  toggleCheatsheet: () => {},
  closeCheatsheet: () => {},
});

export function useKeyboardShortcuts(): KeyboardShortcutContextValue {
  return useContext(KeyboardShortcutContext);
}

// ---------------------------------------------------------------------------
// Input focus detection
// ---------------------------------------------------------------------------
function isInputFocused(event: KeyboardEvent): boolean {
  const target = event.target as HTMLElement | null;
  if (!target) return false;

  const tagName = target.tagName;

  if (tagName === "TEXTAREA" || tagName === "SELECT") return true;
  if (tagName === "INPUT") {
    const inputType = (target as HTMLInputElement).type;
    if (
      inputType === "button" ||
      inputType === "submit" ||
      inputType === "reset" ||
      inputType === "checkbox" ||
      inputType === "radio"
    ) {
      return false;
    }
    return true;
  }

  if (target.isContentEditable) return true;
  if (target.closest(".cm-editor")) return true;
  if (target.closest(".xterm")) return true;

  return false;
}

// ---------------------------------------------------------------------------
// Self-contained Escape layer registry
//
// Manages its own document keydown listener for Escape. The listener is
// attached when the first layer registers and detached when the last one
// unregisters. This means useRegisterEscapeLayer works in any context
// (with or without KeyboardShortcutProvider), including unit tests.
// ---------------------------------------------------------------------------
let layerIdCounter = 0;
let escapeListenerAttached = false;

const layers: Layer[] = [];

function handleEscapeKeyDown(event: KeyboardEvent) {
  if (event.key !== "Escape") return;
  if (layers.length === 0) return;
  event.preventDefault();
  const top = layers[0];
  if (top) top.handler();
}

function attachEscapeListener() {
  if (!escapeListenerAttached) {
    document.addEventListener("keydown", handleEscapeKeyDown);
    escapeListenerAttached = true;
  }
}

function detachEscapeListener() {
  if (escapeListenerAttached && layers.length === 0) {
    document.removeEventListener("keydown", handleEscapeKeyDown);
    escapeListenerAttached = false;
  }
}

function registerLayer(priority: number, handler: () => void): string {
  const id = `layer-${++layerIdCounter}`;
  layers.push({ id, priority, handler });
  layers.sort((a, b) => b.priority - a.priority);
  attachEscapeListener();
  return id;
}

function unregisterLayer(id: string): void {
  const idx = layers.findIndex((l) => l.id === id);
  if (idx !== -1) layers.splice(idx, 1);
  detachEscapeListener();
}

// ---------------------------------------------------------------------------
// Provider props
// ---------------------------------------------------------------------------
export interface KeyboardShortcutProviderProps {
  children: ReactNode;
  onViewChange?: (view: ViewMode) => void;
  onSearchFocus?: () => void;
  onArrowNav?: (event: KeyboardEvent) => void;
}

// ---------------------------------------------------------------------------
// Provider — handles non-Escape shortcuts (view switching, Cmd+K, ?, arrows)
// ---------------------------------------------------------------------------
export function KeyboardShortcutProvider({
  children,
  onViewChange,
  onSearchFocus,
  onArrowNav,
}: KeyboardShortcutProviderProps) {
  const [isCheatsheetOpen, setIsCheatsheetOpen] = useState(false);

  // Stable callback refs so the global listener always sees latest callbacks
  const onViewChangeRef = useRef(onViewChange);
  onViewChangeRef.current = onViewChange;
  const onSearchFocusRef = useRef(onSearchFocus);
  onSearchFocusRef.current = onSearchFocus;
  const onArrowNavRef = useRef(onArrowNav);
  onArrowNavRef.current = onArrowNav;

  // Global keydown listener for non-Escape shortcuts
  useEffect(() => {
    function handleKeyDown(event: KeyboardEvent) {
      // Escape is handled by the layer registry's own listener — skip here
      if (event.key === "Escape") return;

      const inInput = isInputFocused(event);

      // Cmd/Ctrl+K: handle even in inputs (override browser default)
      if (event.key === "k" && (event.metaKey || event.ctrlKey)) {
        event.preventDefault();
        onSearchFocusRef.current?.();
        return;
      }

      // All remaining shortcuts: suppress when in input
      if (inInput) return;

      // View switching: only when no modifiers held
      if (
        !event.metaKey &&
        !event.ctrlKey &&
        !event.altKey &&
        !event.shiftKey
      ) {
        const viewMap: Record<string, ViewMode> = {
          "1": "kanban",
          "2": "table",
          "3": "terminal",
          "4": "observability",
          "5": "files",
          "6": "workspace",
          "0": "settings",
        };
        const view = viewMap[event.key];
        if (view) {
          event.preventDefault();
          onViewChangeRef.current?.(view);
          return;
        }

        // ? for cheatsheet
        if (event.key === "?") {
          event.preventDefault();
          setIsCheatsheetOpen((prev) => !prev);
          return;
        }
      }

      // Arrow key navigation
      if (
        ["ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight"].includes(event.key)
      ) {
        onArrowNavRef.current?.(event);
      }
    }

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, []);

  const toggleCheatsheet = useCallback(() => {
    setIsCheatsheetOpen((prev) => !prev);
  }, []);

  const closeCheatsheet = useCallback(() => {
    setIsCheatsheetOpen(false);
  }, []);

  const contextValue: KeyboardShortcutContextValue = {
    isCheatsheetOpen,
    toggleCheatsheet,
    closeCheatsheet,
  };

  return (
    <KeyboardShortcutContext.Provider value={contextValue}>
      {children}
    </KeyboardShortcutContext.Provider>
  );
}

// ---------------------------------------------------------------------------
// useRegisterEscapeLayer hook
//
// Self-contained: works with or without KeyboardShortcutProvider.
// ---------------------------------------------------------------------------
export function useRegisterEscapeLayer(
  priority: number,
  handler: () => void,
  active: boolean,
): void {
  const layerIdRef = useRef<string | null>(null);
  const handlerRef = useRef(handler);
  handlerRef.current = handler;

  useEffect(() => {
    if (!active) {
      if (layerIdRef.current) {
        unregisterLayer(layerIdRef.current);
        layerIdRef.current = null;
      }
      return;
    }

    // Register with a stable wrapper that reads the latest handler.
    // Capture the id locally so the cleanup closure always unregisters
    // the correct layer (not a newer one written to the ref).
    const id = registerLayer(priority, () => handlerRef.current());
    layerIdRef.current = id;

    return () => {
      unregisterLayer(id);
      if (layerIdRef.current === id) layerIdRef.current = null;
    };
  }, [active, priority]);
}
