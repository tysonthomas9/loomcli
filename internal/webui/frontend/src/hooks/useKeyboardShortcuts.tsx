/**
 * Keyboard shortcuts hook with context-scoped escape layer registry.
 *
 * The escape layer registry is scoped to each KeyboardShortcutProvider instance
 * via React context, preventing cross-contamination between component trees and
 * test suites. A module-level fallback is provided for standalone usage without
 * a provider.
 */

import React, {
  createContext,
  useContext,
  useEffect,
  useRef,
} from "react";

// ============ Layer Priority Constants ============

/** Layer priorities - higher number = higher priority (handled first by Escape) */
export const LAYER_ISSUE_PANEL = 10;
export const LAYER_MODAL = 20;
export const LAYER_DROPDOWN = 30;
export const LAYER_TOOLTIP = 5;

// ============ Types ============

/** A registered escape layer with its handler and priority. */
interface EscapeLayer {
  id: number;
  priority: number;
  handler: () => void;
}

/** Internal registry state and operations. */
interface EscapeRegistry {
  layers: EscapeLayer[];
  idCounter: number;
  listenerAttached: boolean;
}

/** API exposed via context for registering/unregistering escape layers. */
interface EscapeRegistryAPI {
  register: (priority: number, handler: () => void) => number;
  unregister: (id: number) => void;
}

// ============ Registry Factory ============

/**
 * Creates a fresh escape registry with its own state and document listener management.
 */
function createEscapeRegistry(): {
  registry: EscapeRegistry;
  api: EscapeRegistryAPI;
  attachListener: () => void;
  detachListener: () => void;
} {
  const registry: EscapeRegistry = {
    layers: [],
    idCounter: 0,
    listenerAttached: false,
  };

  const handleEscapeKeyDown = (event: KeyboardEvent) => {
    if (event.key !== "Escape") return;
    if (registry.layers.length === 0) return;

    // Sort by priority descending, call the highest-priority handler
    const sorted = [...registry.layers].sort(
      (a, b) => b.priority - a.priority,
    );
    sorted[0].handler();
    event.stopPropagation();
  };

  const attachListener = () => {
    if (!registry.listenerAttached) {
      document.addEventListener("keydown", handleEscapeKeyDown);
      registry.listenerAttached = true;
    }
  };

  const detachListener = () => {
    if (registry.listenerAttached) {
      document.removeEventListener("keydown", handleEscapeKeyDown);
      registry.listenerAttached = false;
    }
  };

  const register = (priority: number, handler: () => void): number => {
    const id = ++registry.idCounter;
    registry.layers.push({ id, priority, handler });
    return id;
  };

  const unregister = (id: number) => {
    registry.layers = registry.layers.filter((layer) => layer.id !== id);
  };

  return { registry, api: { register, unregister }, attachListener, detachListener };
}

// ============ Context ============

const EscapeRegistryContext = createContext<EscapeRegistryAPI | null>(null);

// Module-level fallback for standalone usage (no provider ancestor)
let fallbackInstance: ReturnType<typeof createEscapeRegistry> | null = null;

function getFallbackRegistry(): ReturnType<typeof createEscapeRegistry> {
  if (!fallbackInstance) {
    fallbackInstance = createEscapeRegistry();
    fallbackInstance.attachListener();
  }
  return fallbackInstance;
}

/**
 * Reset the module-level fallback registry. For test cleanup only.
 */
export function resetEscapeRegistry(): void {
  if (fallbackInstance) {
    fallbackInstance.detachListener();
    fallbackInstance.registry.layers = [];
    fallbackInstance.registry.idCounter = 0;
    fallbackInstance = null;
  }
}

// ============ Provider ============

interface KeyboardShortcutProviderProps {
  children: React.ReactNode;
}

/**
 * Provides a context-scoped escape layer registry.
 * Each provider instance has its own registry and document listener.
 */
export function KeyboardShortcutProvider({
  children,
}: KeyboardShortcutProviderProps) {
  const instanceRef = useRef<ReturnType<typeof createEscapeRegistry> | null>(
    null,
  );

  // Create registry once per provider instance
  if (!instanceRef.current) {
    instanceRef.current = createEscapeRegistry();
  }

  const instance = instanceRef.current;

  // Manage document listener lifecycle
  useEffect(() => {
    instance.attachListener();
    return () => {
      instance.detachListener();
      instance.registry.layers = [];
    };
  }, [instance]);

  return (
    <EscapeRegistryContext.Provider value={instance.api}>
      {children}
    </EscapeRegistryContext.Provider>
  );
}

// ============ Hook ============

/**
 * Register an escape layer with the nearest KeyboardShortcutProvider.
 * Falls back to a module-level registry if no provider is found.
 *
 * @param priority - Layer priority (higher = handled first)
 * @param handler - Callback invoked when Escape is pressed and this is the top layer
 * @param active - Whether the layer is currently active (default: true)
 */
export function useRegisterEscapeLayer(
  priority: number,
  handler: () => void,
  active = true,
): void {
  const contextAPI = useContext(EscapeRegistryContext);
  const handlerRef = useRef(handler);

  // Keep handler ref up to date
  useEffect(() => {
    handlerRef.current = handler;
  }, [handler]);

  useEffect(() => {
    if (!active) return;

    const api = contextAPI ?? getFallbackRegistry().api;
    const id = api.register(priority, () => handlerRef.current());

    return () => {
      api.unregister(id);
    };
  }, [priority, active, contextAPI]);
}

// Export context for advanced test scenarios
export { EscapeRegistryContext };
