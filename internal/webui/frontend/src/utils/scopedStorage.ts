/**
 * Workspace-scoped localStorage utility.
 * Keys follow the pattern `loom:{wsId}:keyname` for workspace isolation.
 * All functions are safe in private browsing / quota-exceeded scenarios.
 */

/** Build a workspace-scoped localStorage key. */
export function wsKey(wsId: string, key: string): string {
  return `loom:${wsId}:${key}`;
}

/** Get a workspace-scoped value from localStorage. Returns null on miss or error. */
export function wsGet(wsId: string, key: string): string | null {
  try {
    return localStorage.getItem(wsKey(wsId, key));
  } catch {
    return null;
  }
}

/** Set a workspace-scoped value in localStorage. Silently ignores errors. */
export function wsSet(wsId: string, key: string, value: string): void {
  try {
    localStorage.setItem(wsKey(wsId, key), value);
  } catch {
    /* quota/private browsing */
  }
}

/** Remove a workspace-scoped value from localStorage. Silently ignores errors. */
export function wsRemove(wsId: string, key: string): void {
  try {
    localStorage.removeItem(wsKey(wsId, key));
  } catch {
    /* ignore */
  }
}

/**
 * Global key for the last-used workspace UUID.
 * Not scoped — tracks which workspace was most recently active.
 */
const LAST_WORKSPACE_KEY = "loom:last-workspace-id";

/** Read the last workspace ID synchronously from localStorage. */
export function getLastWorkspaceId(): string | null {
  try {
    return localStorage.getItem(LAST_WORKSPACE_KEY);
  } catch {
    return null;
  }
}

/** Store the last workspace ID in localStorage. */
export function setLastWorkspaceId(id: string): void {
  try {
    localStorage.setItem(LAST_WORKSPACE_KEY, id);
  } catch {
    /* ignore */
  }
}
