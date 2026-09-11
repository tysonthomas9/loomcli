import { isRecoveryEnvelope } from "./recoveryEnvelope";

/** A source-bound retry offer, not a snapshot or permission to reset SSE. */
export interface RecoveryHandle {
  readonly handle: string;
  readonly source_identity: string;
  readonly workspace: string;
  readonly source_repos: readonly string[];
  readonly expires_at: string;
  readonly manifest: "fleet.issue-workspace.v6";
}

const recoveryHandleKeys = [
  "handle",
  "source_identity",
  "workspace",
  "source_repos",
  "expires_at",
  "manifest",
] as const;

/** Matches the existing comma-separated SSE query and server-side trimming. */
function wireScope(repos: readonly string[] | undefined): Set<string> {
  return new Set(
    (repos ?? [])
      .join(",")
      .split(",")
      .map((repo) => repo.trim())
      .filter(Boolean),
  );
}

function canonicalHandle(value: unknown): value is string {
  if (typeof value !== "string" || !/^[A-Za-z0-9_-]{43}$/.test(value))
    return false;
  try {
    const decoded = atob(value.replace(/-/g, "+").replace(/_/g, "/") + "=");
    return (
      decoded.length === 32 &&
      btoa(decoded)
        .replace(/\+/g, "-")
        .replace(/\//g, "_")
        .replace(/=+$/, "") === value
    );
  } catch {
    return false;
  }
}

function futureCanonicalExpiry(value: unknown, now: number): value is string {
  if (
    typeof value !== "string" ||
    !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value)
  )
    return false;
  const expiry = Date.parse(value);
  return (
    Number.isFinite(expiry) &&
    expiry > now &&
    new Date(expiry).toISOString().slice(0, 19) === value.slice(0, 19)
  );
}

function matchingScope(
  value: unknown,
  expectedRepos: readonly string[] | undefined,
): value is string[] {
  if (
    !Array.isArray(value) ||
    value.some(
      (repo: unknown) =>
        typeof repo !== "string" || repo.length === 0 || repo.trim() !== repo,
    )
  )
    return false;
  const expected = wireScope(expectedRepos);
  const actual = new Set(value);
  return (
    actual.size === expected.size &&
    [...actual].every((repo) => expected.has(repo))
  );
}

/** Validate offer shape and scope only. The SSE generation guard rejects retired
 * retry loops; it does not establish ownership across HTTP reconnects or retained
 * offers. Browser recovery still needs attempt ownership and query coverage. */
export function decodeRecoveryHandle(
  value: unknown,
  workspace: string,
  sourceRepos: readonly string[] | undefined,
  now = Date.now(),
): RecoveryHandle | undefined {
  if (value === null || typeof value !== "object" || Array.isArray(value))
    return undefined;
  const offer = value as Record<string, unknown>;
  if (
    Object.keys(offer).length !== recoveryHandleKeys.length ||
    recoveryHandleKeys.some(
      (key) => !Object.prototype.hasOwnProperty.call(offer, key),
    )
  )
    return undefined;
  if (!isRecoveryEnvelope(offer.source_identity, "s1.")) return undefined;
  if (!canonicalHandle(offer.handle)) return undefined;
  if (
    offer.workspace !== workspace ||
    offer.manifest !== "fleet.issue-workspace.v6"
  )
    return undefined;
  if (!futureCanonicalExpiry(offer.expires_at, now)) return undefined;
  if (!matchingScope(offer.source_repos, sourceRepos)) return undefined;
  const repos = offer.source_repos;
  return Object.freeze({
    handle: offer.handle,
    source_identity: offer.source_identity,
    workspace,
    source_repos: Object.freeze([...repos]),
    expires_at: offer.expires_at,
    manifest: "fleet.issue-workspace.v6",
  });
}
