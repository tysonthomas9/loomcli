/**
 * Auth mode discovery: fetches GET /api/config to determine whether
 * the server requires external OAuth authentication.
 *
 * Uses bare fetch (not the API client) because this endpoint is called
 * before auth is initialized — it determines WHETHER auth is needed.
 * The check-no-raw-fetch linter excludes src/api/ via EXCLUDE_DIRS.
 */

// ============= Constants and Types =============
//
// AUTH_MODE_*, AuthMode, AppConfig, and AppConfigError live in src/types/
// so components and hooks can reference them without crossing the frontend
// layer DAG back into the api layer. Re-export here so existing @/api
// consumers continue to work.

export {
  AUTH_MODE_OPEN,
  AUTH_MODE_OIDC,
  type AuthMode,
  type AppConfig,
} from "@/types/common";
import { AUTH_MODE_OPEN, AUTH_MODE_OIDC, type AppConfig } from "@/types/common";

export { AppConfigError } from "@/types/common";
import { AppConfigError } from "@/types/common";

// ============= Module-level Cache =============

let configPromise: Promise<AppConfig> | null = null;

// ============= Internal =============

async function doFetch(): Promise<AppConfig> {
  const controller = new AbortController();
  let timedOut = false;
  const timeoutId = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, 5000);

  try {
    const response = await fetch("/api/config", {
      headers: { Accept: "application/json" },
      signal: controller.signal,
    });

    if (!response.ok) {
      throw new AppConfigError(`Server returned ${response.status}`);
    }

    const contentType = response.headers.get("Content-Type") ?? "";
    if (!contentType.includes("application/json")) {
      throw new AppConfigError("Server returned non-JSON response");
    }

    const data = await response.json();

    if (typeof data !== "object" || data === null || !("mode" in data)) {
      throw new AppConfigError("Invalid config response: missing mode");
    }

    const { mode } = data;

    if (mode === AUTH_MODE_OPEN) {
      return { mode: AUTH_MODE_OPEN };
    }

    if (mode === AUTH_MODE_OIDC) {
      const authUrl = data.auth_url;
      if (typeof authUrl !== "string" || authUrl === "") {
        throw new AppConfigError("OIDC auth mode missing auth_url");
      }
      return { mode: AUTH_MODE_OIDC, auth_url: authUrl };
    }

    throw new AppConfigError(`Unknown auth mode: ${mode}`);
  } catch (error) {
    if (error instanceof AppConfigError) {
      throw error;
    }
    if (
      error instanceof DOMException &&
      error.name === "AbortError" &&
      timedOut
    ) {
      throw new AppConfigError("Config request timed out");
    }
    throw new AppConfigError("Unable to reach server", error);
  } finally {
    clearTimeout(timeoutId);
  }
}

// ============= Public API =============

/**
 * Fetch the server's auth configuration. Returns cached result on subsequent
 * calls. Resets cache on failure so retry is possible on refresh.
 *
 * SECURITY: Fails closed — errors throw AppConfigError, never default to
 * {mode:'open'}. The caller must catch and show an error page.
 */
export async function fetchAppConfig(): Promise<AppConfig> {
  if (configPromise) return configPromise;
  configPromise = doFetch().catch((err) => {
    configPromise = null;
    throw err;
  });
  return configPromise;
}
