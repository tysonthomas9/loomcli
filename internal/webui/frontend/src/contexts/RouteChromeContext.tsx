/**
 * RouteChromeContext — lets a routed view own the app chrome.
 *
 * By default App renders the global header and NavRail around every view.
 * A view that draws its own navigation column and breadcrumb (the /prs
 * stacked PR workspace) calls `useRouteChrome()` while mounted; App then
 * switches AppLayout to `chrome="route"` and hands the view the live shell
 * controls it would otherwise lose (theme, workspace switch, back to the
 * workspace, account menu). Unmounting releases the claim, so every other
 * view — including /prs review deep links — keeps the standard shell.
 */

import {
  createContext,
  useContext,
  useLayoutEffect,
  type ReactNode,
} from "react";

import type { Theme } from "@/hooks/ui/useTheme";

export interface RouteChromeWorkspace {
  id: string;
  name: string;
}

/** Live shell controls handed to a view that owns its chrome. */
export interface RouteChromeControls {
  theme: Theme;
  onToggleTheme: () => void;
  workspaces: RouteChromeWorkspace[];
  activeWorkspaceId: string;
  onWorkspaceSwitch: (workspaceId: string) => void;
  /** Return to the workspace home (the view NavRail's Home button opens). */
  onBackToWorkspace: () => void;
  /** Account menu (sign-out); null when auth has nothing to show. */
  accountMenu: ReactNode;
}

interface RouteChromeContextValue {
  controls: RouteChromeControls;
  /** Register a chrome-owning view; returns the release callback. */
  claim: () => () => void;
}

const RouteChromeContext = createContext<RouteChromeContextValue | null>(null);

export function RouteChromeProvider({
  value,
  children,
}: {
  value: RouteChromeContextValue;
  children: ReactNode;
}): JSX.Element {
  return (
    <RouteChromeContext.Provider value={value}>
      {children}
    </RouteChromeContext.Provider>
  );
}

/**
 * Claim route-owned chrome for the calling view's lifetime and return the
 * shell controls. Returns null outside a provider (isolated renders), where
 * the view simply shows no shell controls.
 */
export function useRouteChrome(): RouteChromeControls | null {
  const ctx = useContext(RouteChromeContext);
  const claim = ctx?.claim;
  // Layout effect: the global header is dropped before first paint, so the
  // route never flashes both chromes.
  useLayoutEffect(() => (claim ? claim() : undefined), [claim]);
  return ctx?.controls ?? null;
}
