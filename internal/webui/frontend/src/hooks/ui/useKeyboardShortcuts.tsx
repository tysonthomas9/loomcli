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
 * The Escape layer system requires a KeyboardShortcutProvider ancestor.
 * The provider adds view switching, search focus, and cheatsheet functionality.
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

import type { ViewMode } from "@/types";

// ---------------------------------------------------------------------------
// Layer priorities (higher = closes first on Escape)
// ---------------------------------------------------------------------------
export const LAYER_CONFIRM_DIALOG = 60;
export const LAYER_TOAST = 50;
export const LAYER_CHEATSHEET = 45;
export const LAYER_WORKSPACE_SWITCHER = 42;
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
  if (!(target instanceof HTMLElement)) return false;

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
// Escape layer registry
//
// Each registry instance manages its own layers and document keydown listener.
// The KeyboardShortcutProvider creates a context-scoped registry.
// ---------------------------------------------------------------------------

export interface EscapeRegistryAPI {
  registerLayer: (priority: number, handler: () => void) => string;
  unregisterLayer: (id: string) => void;
  destroy: () => void;
}

function createEscapeRegistry(): EscapeRegistryAPI {
  const state = {
    layers: [] as Layer[],
    idCounter: 0,
    listenerAttached: false,
  };

  function handleEscapeKeyDown(event: KeyboardEvent) {
    if (event.key !== "Escape") return;
    if (state.layers.length === 0) return;
    event.preventDefault();
    const top = state.layers[0];
    if (top) top.handler();
  }

  return {
    registerLayer(priority: number, handler: () => void): string {
      const id = `layer-${++state.idCounter}`;
      state.layers.push({ id, priority, handler });
      state.layers.sort((a, b) => b.priority - a.priority);
      if (!state.listenerAttached) {
        document.addEventListener("keydown", handleEscapeKeyDown);
        state.listenerAttached = true;
      }
      return id;
    },
    unregisterLayer(id: string): void {
      const idx = state.layers.findIndex((l) => l.id === id);
      if (idx !== -1) state.layers.splice(idx, 1);
      if (state.listenerAttached && state.layers.length === 0) {
        document.removeEventListener("keydown", handleEscapeKeyDown);
        state.listenerAttached = false;
      }
    },
    destroy(): void {
      if (state.listenerAttached) {
        document.removeEventListener("keydown", handleEscapeKeyDown);
        state.listenerAttached = false;
      }
      state.layers.length = 0;
      state.idCounter = 0;
    },
  };
}

export const EscapeRegistryContext = createContext<EscapeRegistryAPI | null>(
  null,
);

// ---------------------------------------------------------------------------
// Provider props
// ---------------------------------------------------------------------------
export interface KeyboardShortcutProviderProps {
  children: ReactNode;
  onViewChange?: (view: ViewMode) => void;
  onSearchFocus?: () => void;
  onArrowNav?: (event: KeyboardEvent) => void;
  /** Opens the workspace quick-switcher (Cmd/Ctrl+K in multi-repo mode) */
  onWorkspaceSwitcher?: () => void;
  /** Direct workspace switching via Cmd/Ctrl+Shift+1-9 */
  onWorkspacePositionalSwitch?: (index: number) => void;
}

// ---------------------------------------------------------------------------
// Provider — handles non-Escape shortcuts (view switching, Cmd+K, ?, arrows)
// ---------------------------------------------------------------------------
export function KeyboardShortcutProvider({
  children,
  onViewChange,
  onSearchFocus,
  onArrowNav,
  onWorkspaceSwitcher,
  onWorkspacePositionalSwitch,
}: KeyboardShortcutProviderProps) {
  const [isCheatsheetOpen, setIsCheatsheetOpen] = useState(false);

  // Stable callback refs so the global listener always sees latest callbacks
  const onViewChangeRef = useRef(onViewChange);
  onViewChangeRef.current = onViewChange;
  const onSearchFocusRef = useRef(onSearchFocus);
  onSearchFocusRef.current = onSearchFocus;
  const onArrowNavRef = useRef(onArrowNav);
  onArrowNavRef.current = onArrowNav;
  const onWorkspaceSwitcherRef = useRef(onWorkspaceSwitcher);
  onWorkspaceSwitcherRef.current = onWorkspaceSwitcher;
  const onWorkspacePositionalSwitchRef = useRef(onWorkspacePositionalSwitch);
  onWorkspacePositionalSwitchRef.current = onWorkspacePositionalSwitch;

  // Provider-scoped escape registry
  const registryRef = useRef<EscapeRegistryAPI | null>(null);
  if (!registryRef.current) {
    registryRef.current = createEscapeRegistry();
  }

  // Clean up escape registry on unmount
  useEffect(() => {
    const registry = registryRef.current;
    return () => {
      registry?.destroy();
    };
  }, []);

  // Global keydown listener for non-Escape shortcuts
  useEffect(() => {
    function handleKeyDown(event: KeyboardEvent) {
      // Escape is handled by the layer registry's own listener — skip here
      if (event.key === "Escape") return;

      const inInput = isInputFocused(event);

      // Cmd/Ctrl+K: workspace switcher (multi-repo) or search focus (single-repo)
      // Fires even in inputs (override browser default)
      if (event.key === "k" && (event.metaKey || event.ctrlKey)) {
        event.preventDefault();
        if (onWorkspaceSwitcherRef.current) {
          onWorkspaceSwitcherRef.current();
        } else {
          onSearchFocusRef.current?.();
        }
        return;
      }

      // Cmd/Ctrl+Shift+1-9: workspace positional switching
      // Fires even in inputs (override browser default)
      if (
        (event.metaKey || event.ctrlKey) &&
        event.shiftKey &&
        event.key >= "1" &&
        event.key <= "9"
      ) {
        const index = parseInt(event.key, 10) - 1;
        if (onWorkspacePositionalSwitchRef.current) {
          event.preventDefault();
          onWorkspacePositionalSwitchRef.current(index);
        }
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
          "2": "terminal",
          "3": "observability",
          "4": "files",
          "5": "workspace",
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
      <EscapeRegistryContext.Provider value={registryRef.current}>
        {children}
      </EscapeRegistryContext.Provider>
    </KeyboardShortcutContext.Provider>
  );
}

// ---------------------------------------------------------------------------
// useRegisterEscapeLayer hook
//
// Requires KeyboardShortcutProvider ancestor.
// ---------------------------------------------------------------------------
export function useRegisterEscapeLayer(
  priority: number,
  handler: () => void,
  active: boolean,
): void {
  const contextRegistry = useContext(EscapeRegistryContext);
  if (!contextRegistry) {
    throw new Error(
      "useRegisterEscapeLayer must be used within a KeyboardShortcutProvider",
    );
  }
  const registry = contextRegistry;

  const layerIdRef = useRef<string | null>(null);
  const handlerRef = useRef(handler);
  handlerRef.current = handler;

  useEffect(() => {
    if (!active) {
      if (layerIdRef.current) {
        registry.unregisterLayer(layerIdRef.current);
        layerIdRef.current = null;
      }
      return;
    }

    // Register with a stable wrapper that reads the latest handler.
    // Capture the id locally so the cleanup closure always unregisters
    // the correct layer (not a newer one written to the ref).
    const id = registry.registerLayer(priority, () => handlerRef.current());
    layerIdRef.current = id;

    return () => {
      registry.unregisterLayer(id);
      if (layerIdRef.current === id) layerIdRef.current = null;
    };
  }, [active, priority, registry]);
}
