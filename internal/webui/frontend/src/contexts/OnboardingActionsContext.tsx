/**
 * OnboardingActionsContext — single dispatch point for the onboarding
 * flow's call-to-action buttons.
 *
 * Why this lives outside `App.tsx`: the no-workspace `RedirectToWorkspace`
 * route renders above `App`. The provider therefore wraps the entire
 * router output (in `main.tsx`) so both surfaces can register and
 * dispatch actions through one channel.
 *
 * Each onboarding step has an `OnboardingAction` identifier (matching
 * the Go ActionOpen* constants). Components that own the side effect for
 * an action — `RedirectToWorkspace` for `open_workspace_repo_wizard`,
 * `App.tsx` for the workspace-scoped modals — call
 * `useRegisterOnboardingAction(action, handler)` on mount. The hook
 * cleans up on unmount so a stale handler can never fire.
 *
 * `dispatch(action)` looks up the live handler and invokes it. With no
 * handler registered (e.g. workspace-scoped action triggered from the
 * no-workspace screen) the call is a logged no-op.
 */

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  type ReactNode,
} from "react";

import type { OnboardingAction } from "@/types/onboarding";

export type OnboardingActionHandler = () => void;

export interface OnboardingActionsContextValue {
  dispatch: (action: OnboardingAction) => void;
  register: (
    action: OnboardingAction,
    handler: OnboardingActionHandler,
  ) => () => void;
}

const OnboardingActionsContext =
  createContext<OnboardingActionsContextValue | null>(null);

interface OnboardingActionsProviderProps {
  children: ReactNode;
  /**
   * Optional fallback called when dispatch fires for an unregistered
   * action. Defaults to a console.warn. Tests can override.
   */
  onUnregistered?: (action: OnboardingAction) => void;
}

export function OnboardingActionsProvider({
  children,
  onUnregistered,
}: OnboardingActionsProviderProps): JSX.Element {
  // Use a ref-backed map so handler registration does not trigger a
  // re-render of every consumer of dispatch.
  const handlersRef = useRef<Map<OnboardingAction, OnboardingActionHandler>>(
    new Map(),
  );

  const register = useCallback(
    (action: OnboardingAction, handler: OnboardingActionHandler) => {
      handlersRef.current.set(action, handler);
      return () => {
        // Only delete if the still-registered handler is the one this
        // closure owned; otherwise a re-registration from a subsequent
        // mount would be wiped out by an outdated cleanup.
        if (handlersRef.current.get(action) === handler) {
          handlersRef.current.delete(action);
        }
      };
    },
    [],
  );

  const dispatch = useCallback(
    (action: OnboardingAction) => {
      const handler = handlersRef.current.get(action);
      if (handler) {
        handler();
        return;
      }
      if (onUnregistered) {
        onUnregistered(action);
      } else {
        // eslint-disable-next-line no-console
        console.warn(
          `[onboarding] dispatch(${action}): no handler registered`,
        );
      }
    },
    [onUnregistered],
  );

  const value = useMemo<OnboardingActionsContextValue>(
    () => ({ dispatch, register }),
    [dispatch, register],
  );

  return (
    <OnboardingActionsContext.Provider value={value}>
      {children}
    </OnboardingActionsContext.Provider>
  );
}

/**
 * Throws when used outside `OnboardingActionsProvider`. Components
 * dispatching actions must be inside the provider.
 */
export function useOnboardingActions(): OnboardingActionsContextValue {
  const ctx = useContext(OnboardingActionsContext);
  if (!ctx) {
    throw new Error(
      "useOnboardingActions must be used inside <OnboardingActionsProvider>",
    );
  }
  return ctx;
}

/**
 * Register a handler for an onboarding action while this component is
 * mounted. The handler is wrapped in a ref so consumers can re-create
 * the function on each render without causing redundant register/
 * deregister cycles.
 */
export function useRegisterOnboardingAction(
  action: OnboardingAction,
  handler: OnboardingActionHandler,
): void {
  const { register } = useOnboardingActions();
  const handlerRef = useRef(handler);
  handlerRef.current = handler;

  useEffect(() => {
    const stable: OnboardingActionHandler = () => handlerRef.current();
    return register(action, stable);
  }, [action, register]);
}
