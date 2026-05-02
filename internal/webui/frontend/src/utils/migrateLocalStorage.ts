/**
 * localStorage migration (V5→V6→V7 key naming conventions).
 * Runs once at app boot before React renders.
 * Idempotent: safe to run multiple times.
 */

export const CURRENT_VERSION = "7";
export const VERSION_KEY = "cortex-version";

/** V5 legacy key -> V6 key mapping. Legacy key names are migration-only. */
export const V5_TO_V6_KEY_MAP: Record<string, string> = {
  "theme-preference": "cortex:theme",
  "beads-recent-assignees": "cortex:recent-assignees",
  "terminal-font-family": "cortex:terminal-font-family",
  "terminal-font-size": "cortex:terminal-font-size",
};

/** Keys that existed in V5 but are not carried forward to V6. */
const OBSOLETE_KEYS: string[] = [];

export function getStorageVersion(): string | null {
  try {
    return localStorage.getItem(VERSION_KEY);
  } catch {
    return null;
  }
}

function migrateV5toV6(): void {
  for (const [v5Key, v6Key] of Object.entries(V5_TO_V6_KEY_MAP)) {
    let oldValue: string | null = null;
    try {
      oldValue = localStorage.getItem(v5Key);
      if (oldValue === null) continue;

      // Validate JSON keys that store JSON.
      if (v5Key === "beads-recent-assignees") {
        try {
          JSON.parse(oldValue);
        } catch {
          // Corrupted JSON — remove and skip
          localStorage.removeItem(v5Key);
          continue;
        }
      }

      // Only write V6 key if it doesn't already exist (partial migration recovery)
      const existingV6 = localStorage.getItem(v6Key);
      if (existingV6 === null) {
        localStorage.setItem(v6Key, oldValue);
      }

      // Remove the old V5 key
      localStorage.removeItem(v5Key);
    } catch (e) {
      if (e instanceof DOMException && e.name === "QuotaExceededError") {
        // Free space by removing old key, then retry write
        try {
          localStorage.removeItem(v5Key);
          if (oldValue !== null) {
            localStorage.setItem(v6Key, oldValue);
          }
        } catch {
          // Give up on this key — V5 key may still exist for retry on next launch
        }
        console.warn(`[Migration] Quota exceeded migrating ${v5Key}`);
      }
      // Continue with other keys
    }
  }

  // Remove obsolete keys
  for (const key of OBSOLETE_KEYS) {
    try {
      localStorage.removeItem(key);
    } catch {
      // Ignore
    }
  }

  // Stamp version 6 (not CURRENT_VERSION — further migrations may follow)
  try {
    localStorage.setItem(VERSION_KEY, "6");
  } catch {
    console.warn("[Migration] Could not stamp version");
  }
}

/** V6 global key → V7 scoped key suffix mapping for workspace-scoped keys. */
export const V6_TO_V7_SCOPED_KEYS: Record<string, string> = {
  "workspace-tree-collapsed": "tree-collapsed",
  "workspace-tree-active-filter": "tree-active-filter",
  "workspace-tree-repo-collapsed": "tree-repo-collapsed",
  "workspace-tree-work-queue-expanded": "work-queue-expanded",
  "agents-sidebar-collapsed": "agents-sidebar-collapsed",
  "agents-sidebar-work-queue-expanded": "agents-sidebar-work-queue-expanded",
  "agents-sidebar-repo-groups-collapsed":
    "agents-sidebar-repo-groups-collapsed",
  "agents-sidebar-ws-collapsed": "agents-sidebar-ws-collapsed",
  "graph-show-closed": "graph-show-closed",
  "graph-status-filter": "graph-status-filter",
  "graph-dep-type-filter": "graph-dep-type-filter",
  "loom-selected-repos": "selected-repos",
};

/**
 * Attempt to resolve workspace name → UUID from cached backend config.
 * Returns null if resolution is unavailable.
 */
function resolveWorkspaceUUID(workspaceName: string): string | null {
  try {
    const configRaw = localStorage.getItem("cortex:config:backend");
    if (!configRaw) return null;
    const config = JSON.parse(configRaw);
    // Config may store workspace list in various shapes.
    // WorkspaceData has { id, name, workspaces: WorkspaceSummary[] }
    if (config && typeof config === "object") {
      // Direct match on the workspace data itself
      if (config.name === workspaceName && config.id) {
        return config.id;
      }
      // Search the workspaces array
      if (Array.isArray(config.workspaces)) {
        for (const ws of config.workspaces) {
          if (ws && ws.name === workspaceName && ws.id) {
            return ws.id;
          }
        }
      }
    }
  } catch {
    // Corrupted or missing cache — fall through
  }
  return null;
}

/**
 * Resolve a workspace name to UUID, searching all workspaces in cached config.
 */
function resolveAnyWorkspaceUUID(workspaceName: string): string | null {
  return resolveWorkspaceUUID(workspaceName);
}

function migrateV6toV7(): void {
  // 1. Determine current workspace and resolve to UUID
  const activeWorkspaceName = localStorage.getItem("loom-active-workspace");
  let wsUUID: string | null = null;

  if (activeWorkspaceName) {
    wsUUID = resolveWorkspaceUUID(activeWorkspaceName);
  }

  // 2. Move workspace-scoped keys to loom:{uuid}:* namespace
  if (wsUUID) {
    for (const [globalKey, scopedSuffix] of Object.entries(
      V6_TO_V7_SCOPED_KEYS,
    )) {
      try {
        const value = localStorage.getItem(globalKey);
        if (value === null) continue;
        const scopedKey = `loom:${wsUUID}:${scopedSuffix}`;
        // Only write if scoped key doesn't already exist (partial migration recovery)
        if (localStorage.getItem(scopedKey) === null) {
          localStorage.setItem(scopedKey, value);
        }
        localStorage.removeItem(globalKey);
      } catch {
        // Continue with other keys
      }
    }
  } else {
    // No UUID resolution — just remove old global keys
    for (const globalKey of Object.keys(V6_TO_V7_SCOPED_KEYS)) {
      try {
        localStorage.removeItem(globalKey);
      } catch {
        // Ignore
      }
    }
  }

  // 3. Handle workspace-tree-epic-collapsed:{workspaceName} keys
  try {
    const keysToProcess: string[] = [];
    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i);
      if (key && key.startsWith("workspace-tree-epic-collapsed:")) {
        keysToProcess.push(key);
      }
    }
    for (const key of keysToProcess) {
      const wsName = key.slice("workspace-tree-epic-collapsed:".length);
      const uuid = resolveAnyWorkspaceUUID(wsName);
      if (uuid) {
        const value = localStorage.getItem(key);
        if (value !== null) {
          const scopedKey = `loom:${uuid}:tree-epic-collapsed`;
          if (localStorage.getItem(scopedKey) === null) {
            localStorage.setItem(scopedKey, value);
          }
        }
      }
      localStorage.removeItem(key);
    }
  } catch {
    // Ignore
  }

  // 4. Rename loom-active-workspace → loom:last-workspace-id (storing UUID)
  try {
    if (wsUUID) {
      localStorage.setItem("loom:last-workspace-id", wsUUID);
    }
    localStorage.removeItem("loom-active-workspace");
  } catch {
    // Ignore
  }

  // 5. Stamp version
  try {
    localStorage.setItem(VERSION_KEY, CURRENT_VERSION);
  } catch {
    console.warn("[Migration] Could not stamp version");
  }
}

/**
 * Run localStorage migration. Call synchronously at app boot, before React renders.
 * Safe to call multiple times (idempotent).
 */
export function migrateLocalStorage(): void {
  try {
    const version = getStorageVersion();

    // Already at current version — no-op
    if (version === CURRENT_VERSION) return;

    // Future version — do not downgrade
    if (
      version !== null &&
      parseInt(version, 10) > parseInt(CURRENT_VERSION, 10)
    )
      return;

    const versionNum = version ? parseInt(version, 10) : 0;

    // Run V5→V6 if needed
    if (versionNum < 6) {
      migrateV5toV6();
    }

    // Run V6→V7 if needed (migrateV5toV6 stamps "6", so check actual storage version again)
    const postV6Version = getStorageVersion();
    const postV6Num = postV6Version ? parseInt(postV6Version, 10) : 0;
    if (postV6Num < 7) {
      migrateV6toV7();
    }
  } catch {
    // localStorage completely unavailable — log and continue
    console.warn("[Migration] localStorage unavailable, skipping migration");
  }
}
