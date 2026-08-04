/**
 * Shared types and cache helpers for store-backed backend configuration.
 */

// ============= Types =============

/**
 * Per-agent backend override entry.
 */
export interface AgentBackendOverride {
  worktree: string;
  role: string;
  backend: string;
}

/**
 * Backend configuration data returned by the API.
 */
export interface BackendConfigData {
  backend: string;
  source: string;
  available: string[];
  agents: AgentBackendOverride[];
}

// ============= Cache =============

const CONFIG_CACHE_KEY = "cortex:config:backend";

/**
 * Get cached backend config from localStorage.
 * Returns null if no cache exists or data is corrupted.
 */
export function getCachedBackendConfig(): BackendConfigData | null {
  try {
    const raw = localStorage.getItem(CONFIG_CACHE_KEY);
    if (!raw) return null;
    const parsed: unknown = JSON.parse(raw);
    // Runtime shape check to guard against stale/corrupted cache
    if (
      typeof parsed !== "object" ||
      parsed === null ||
      typeof (parsed as Record<string, unknown>).backend !== "string" ||
      !Array.isArray((parsed as Record<string, unknown>).available) ||
      !Array.isArray((parsed as Record<string, unknown>).agents)
    ) {
      return null;
    }
    return parsed as BackendConfigData;
  } catch {
    return null;
  }
}

/** Cache backend config in localStorage. */
export function cacheBackendConfig(data: BackendConfigData): void {
  try {
    localStorage.setItem(CONFIG_CACHE_KEY, JSON.stringify(data));
  } catch {
    // localStorage may be unavailable (private browsing, quota exceeded) — ignore
  }
}
