/** Human labels for runtime error identifiers shown in compact UI surfaces. */
const RUN_ERROR_LABELS: Record<string, string> = {
  AuthFailure: "auth failed",
  BackendUnavailable: "backend unavailable",
  BillingError: "billing error",
  ContextOverflow: "context overflow",
  LockConflict: "lock conflict",
  ModelNotFound: "model not found",
  RateLimited: "rate limited",
  SpawnFailure: "launch failed",
  Timeout: "timed out",
  local_backend_auth_missing: "local backend authentication required",
  local_backend_unavailable: "local backend unavailable",
  local_worktree_unprovisioned: "local worktree not provisioned",
  sandbox_required: "sandbox required",
};

/** Return a compact label for a known error class, with a safe generic fallback. */
export function shortRunErrorLabel(
  value: string | undefined,
): string | undefined {
  if (!value) return undefined;
  return RUN_ERROR_LABELS[value] ?? "run failed";
}

/**
 * Turn machine identifiers into sentence case while preserving detailed prose
 * verbatim. The raw value remains available in the detailed run view.
 */
export function humanizeRunError(value: string): string {
  const trimmed = value.trim();
  const known = RUN_ERROR_LABELS[trimmed];
  if (known) return known.charAt(0).toUpperCase() + known.slice(1);

  if (!/^[A-Za-z][A-Za-z0-9_-]*$/.test(trimmed)) return trimmed;

  const identifier = trimmed
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/[_-]+/g, " ");
  if (identifier === trimmed) return trimmed;

  const lowercase = identifier.toLowerCase();
  return lowercase.charAt(0).toUpperCase() + lowercase.slice(1);
}
