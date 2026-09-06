import { isRecoveryEnvelope } from "./recoveryEnvelope";

/** A source-bound retry offer, not a snapshot or permission to reset SSE. */
export interface RecoveryHandle {
  readonly handle: string;
  readonly source_identity: string;
  readonly workspace: string;
  readonly source_repos: readonly string[];
  readonly expires_at: string;
  readonly manifest: "fleet.issue-workspace.v2";
}

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
  const keys = [
    "handle",
    "source_identity",
    "workspace",
    "source_repos",
    "expires_at",
    "manifest",
  ];
  if (
    Object.keys(offer).length !== keys.length ||
    keys.some((key) => !Object.prototype.hasOwnProperty.call(offer, key))
  )
    return undefined;
  if (!isRecoveryEnvelope(offer.source_identity, "s1.")) return undefined;
  if (
    typeof offer.handle !== "string" ||
    !/^[A-Za-z0-9_-]{43}$/.test(offer.handle)
  )
    return undefined;
  try {
    const decoded = atob(
      offer.handle.replace(/-/g, "+").replace(/_/g, "/") + "=",
    );
    if (
      decoded.length !== 32 ||
      btoa(decoded)
        .replace(/\+/g, "-")
        .replace(/\//g, "_")
        .replace(/=+$/, "") !== offer.handle
    )
      return undefined;
  } catch {
    return undefined;
  }
  if (
    offer.workspace !== workspace ||
    offer.manifest !== "fleet.issue-workspace.v2"
  )
    return undefined;
  if (
    typeof offer.expires_at !== "string" ||
    !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(
      offer.expires_at,
    )
  )
    return undefined;
  const expiry = Date.parse(offer.expires_at);
  if (
    !Number.isFinite(expiry) ||
    expiry <= now ||
    new Date(expiry).toISOString().slice(0, 19) !==
      offer.expires_at.slice(0, 19)
  )
    return undefined;
  if (
    !Array.isArray(offer.source_repos) ||
    offer.source_repos.some(
      (repo: unknown) =>
        typeof repo !== "string" || repo.length === 0 || repo.trim() !== repo,
    )
  )
    return undefined;
  const repos = offer.source_repos as string[];
  const expected = wireScope(sourceRepos);
  const actual = new Set(repos);
  if (
    actual.size !== expected.size ||
    [...actual].some((repo) => !expected.has(repo))
  )
    return undefined;
  return Object.freeze({
    handle: offer.handle,
    source_identity: offer.source_identity,
    workspace,
    source_repos: Object.freeze([...repos]),
    expires_at: offer.expires_at,
    manifest: "fleet.issue-workspace.v2",
  });
}
