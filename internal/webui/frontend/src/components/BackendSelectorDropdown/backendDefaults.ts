/**
 * Fallback metadata for known backends until GET /api/backends is available.
 */

export interface BackendInfo {
  /** Internal backend name (e.g., "claude") */
  name: string;
  /** Human-readable display name (e.g., "Claude") */
  displayName: string;
  /** Provider/organization (e.g., "Anthropic") */
  provider: string;
  /** Brand color for the status dot (CSS color string) */
  brandColor: string;
  /** Whether the backend is currently available */
  available: boolean;
  /** Optional health/status message */
  healthMessage?: string;
}

interface BackendDefaults {
  displayName: string;
  provider: string;
  brandColor: string;
}

export const KNOWN_BACKEND_DEFAULTS: Record<string, BackendDefaults> = {
  claude: {
    displayName: "Claude",
    provider: "Anthropic",
    brandColor: "#d4a574",
  },
  codex: {
    displayName: "Codex",
    provider: "OpenAI",
    brandColor: "#10a37f",
  },
  opencode: {
    displayName: "OpenCode",
    provider: "Open Source",
    brandColor: "#6366f1",
  },
  shell: {
    displayName: "Terminal",
    provider: "System",
    brandColor: "#6b7280",
  },
};

/**
 * Merge API data with known defaults to produce a BackendInfo.
 * Falls back to sensible defaults for unknown backends.
 */
export function toBackendInfo(
  name: string,
  apiData?: Partial<BackendInfo>,
): BackendInfo {
  const defaults = KNOWN_BACKEND_DEFAULTS[name];
  return {
    name,
    displayName:
      apiData?.displayName ??
      defaults?.displayName ??
      name.charAt(0).toUpperCase() + name.slice(1),
    provider: apiData?.provider ?? defaults?.provider ?? "Unknown",
    brandColor: apiData?.brandColor ?? defaults?.brandColor ?? "#888888",
    available: apiData?.available ?? true,
    ...(apiData?.healthMessage != null
      ? { healthMessage: apiData.healthMessage }
      : {}),
  };
}
