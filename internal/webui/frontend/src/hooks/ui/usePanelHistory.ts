/**
 * In-panel navigation history stack for the issue detail panel.
 *
 * Records issue IDs the user has navigated away from so a back button
 * can restore them. Independent of usePanelManager (mutual exclusivity
 * and transitions) and of browser history.
 */

import { useState, useCallback, useRef } from "react";

const MAX_STACK_DEPTH = 50;

export interface UsePanelHistoryReturn {
  /** Whether there is history to go back to. */
  canGoBack: boolean;
  /** Push the current issue ID onto the history stack. */
  push: (issueId: string) => void;
  /** Pop and return the most recent issue ID, or null if the stack is empty. */
  pop: () => string | null;
  /** Clear the entire history stack. */
  clear: () => void;
  /** Current stack depth. */
  depth: number;
}

export function usePanelHistory(): UsePanelHistoryReturn {
  const [stack, setStack] = useState<string[]>([]);
  // Authoritative mirror: mutated synchronously inside push/pop/clear so
  // callers that invoke these multiple times in the same tick see the
  // updated stack before React has re-rendered. Kept in sync with the
  // rendered `stack` via the assignment on every render.
  const stackRef = useRef(stack);
  stackRef.current = stack;

  const push = useCallback((issueId: string) => {
    const next = [...stackRef.current, issueId];
    if (next.length > MAX_STACK_DEPTH) {
      next.shift();
    }
    stackRef.current = next;
    setStack(next);
  }, []);

  const pop = useCallback((): string | null => {
    const current = stackRef.current;
    if (current.length === 0) return null;
    const last = current[current.length - 1] ?? null;
    const next = current.slice(0, -1);
    stackRef.current = next;
    setStack(next);
    return last;
  }, []);

  const clear = useCallback(() => {
    stackRef.current = [];
    setStack([]);
  }, []);

  return {
    canGoBack: stack.length > 0,
    push,
    pop,
    clear,
    depth: stack.length,
  };
}
