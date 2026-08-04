/**
 * localStorage startup normalization.
 * Runs once at app boot before React renders.
 */

export const CURRENT_VERSION = "7";
export const VERSION_KEY = "cortex-version";

export function getStorageVersion(): string | null {
  try {
    return localStorage.getItem(VERSION_KEY);
  } catch {
    return null;
  }
}

/**
 * Stamp fresh browser state with the current storage version.
 * Existing or future versions are left untouched.
 */
export function migrateLocalStorage(): void {
  try {
    const version = getStorageVersion();
    if (version !== null) return;
    localStorage.setItem(VERSION_KEY, CURRENT_VERSION);
  } catch {
    console.warn("[Storage] localStorage unavailable, skipping startup stamp");
  }
}
