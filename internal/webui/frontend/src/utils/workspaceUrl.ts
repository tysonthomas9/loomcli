/**
 * Helpers for building workspace URLs that preserve workspace-independent
 * search params on workspace switch.
 *
 * The whitelist is intentionally narrow: only `view=` is preserved across
 * workspace boundaries because it's the one search param whose meaning is
 * workspace-agnostic. Other params — `issue=`, `repo=`, filter params, etc.
 * — would leak stale IDs from one workspace into another and are dropped.
 */

const PRESERVED_PARAMS = ["view"] as const;

/**
 * Build a `/ws/${id}/` URL, optionally re-attaching whitelisted search
 * params from the current location.
 *
 * @param targetId - the destination workspace UUID
 * @param currentSearch - the current location.search string (default:
 *                       window.location.search). Pass explicitly in tests
 *                       or where window may not be defined.
 */
export function buildWorkspaceSwitchUrl(
  targetId: string,
  currentSearch: string = typeof window !== "undefined"
    ? window.location.search
    : "",
): string {
  const current = new URLSearchParams(currentSearch);
  const next = new URLSearchParams();
  for (const key of PRESERVED_PARAMS) {
    const value = current.get(key);
    if (value !== null && value.length > 0) {
      next.set(key, value);
    }
  }
  const qs = next.toString();
  return `/ws/${targetId}/${qs ? `?${qs}` : ""}`;
}
