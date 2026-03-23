/**
 * localStorage migration from V5 to V6 key naming convention.
 * Runs once at app boot before React renders.
 * Idempotent: safe to run multiple times.
 */

export const CURRENT_VERSION = "6";
export const VERSION_KEY = "cortex-version";

/** V5 key → V6 key mapping */
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

      // Validate JSON keys that store JSON (beads-recent-assignees)
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

  // Stamp version as the LAST step
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

    // No version or version < 6 — run migration
    migrateV5toV6();
  } catch {
    // localStorage completely unavailable — log and continue
    console.warn("[Migration] localStorage unavailable, skipping migration");
  }
}
