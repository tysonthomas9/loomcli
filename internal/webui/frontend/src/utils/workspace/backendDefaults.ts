/**
 * Fallback metadata for known backends until GET /api/backends is available.
 *
 * Lives in src/utils/ because it is pure data + a pure merge function with
 * no React or component dependencies — callable from hooks and components
 * alike. Moved out of src/components/BackendSelectorDropdown/ in Phase 7 so
 * the frontend layer DAG (hooks → utils OK, hooks → components forbidden)
 * stays consistent.
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
  /** Whether the CLI binary is installed on PATH. */
  installed?: boolean;
  /** Whether credentials are configured, when the backend requires them. */
  apiKeySet?: boolean;
  /** Optional detected CLI version. */
  version?: string;
  /** Optional health/status message */
  healthMessage?: string;
}

const TESTING_ONLY_BACKENDS = new Set(["localdogfood"]);

/**
 * Whether a discovered backend belongs in user-facing backend selectors.
 * Test-only backends are opt-in so a helper executable on PATH cannot leak
 * into production settings or agent creation screens.
 */
export function isUserFacingBackend(
  name: string,
  showTestingBackends = import.meta.env.VITE_SHOW_TESTING_BACKENDS === "true",
): boolean {
  const normalizedName = name.toLowerCase().replace(/[-_]/g, "");
  return showTestingBackends || !TESTING_ONLY_BACKENDS.has(normalizedName);
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
  gemini: {
    displayName: "Gemini",
    provider: "Google",
    brandColor: "#8e24aa",
  },
  cursor: {
    displayName: "Cursor",
    provider: "Anysphere",
    brandColor: "#00e5ff",
  },
  browser: {
    displayName: "Browser",
    provider: "System",
    brandColor: "#f59e0b",
  },
  shell: {
    displayName: "Terminal",
    provider: "System",
    brandColor: "#6b7280",
  },
};

/**
 * Brand color per known backend, derived from KNOWN_BACKEND_DEFAULTS.
 *
 * Lives here (next to its data source) rather than inside the TerminalView
 * component so non-terminal consumers can import the constant without pulling
 * the terminal component graph (xterm, etc.) into their module/test graph.
 */
export const BACKEND_BRAND_COLORS: Record<string, string> = Object.fromEntries(
  Object.entries(KNOWN_BACKEND_DEFAULTS).map(([k, v]) => [k, v.brandColor]),
);

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
    ...(apiData?.installed != null ? { installed: apiData.installed } : {}),
    ...(apiData?.apiKeySet != null ? { apiKeySet: apiData.apiKeySet } : {}),
    ...(apiData?.version != null ? { version: apiData.version } : {}),
    ...(apiData?.healthMessage != null
      ? { healthMessage: apiData.healthMessage }
      : {}),
  };
}
