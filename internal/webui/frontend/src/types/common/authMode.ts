/**
 * Auth mode constants shared across the frontend.
 *
 * These live in src/types/ so components and hooks that render auth-gated
 * UIs can reference them without crossing the frontend layer DAG back into
 * the api layer. The api layer re-exports them for its own call sites.
 */

export const AUTH_MODE_OPEN = "open" as const;
export const AUTH_MODE_OIDC = "oidc" as const;

export type AuthMode = typeof AUTH_MODE_OPEN | typeof AUTH_MODE_OIDC;

/**
 * App-level auth configuration returned by GET /api/config.
 */
export type AppConfig =
  | { mode: typeof AUTH_MODE_OPEN }
  | { mode: typeof AUTH_MODE_OIDC; auth_url: string };
