import type { FileScopeRef } from "@/api/workspace";

export type CheckoutRef = FileScopeRef;

export interface CheckoutTabIdentity {
  ref: CheckoutRef;
  path: string;
}

export const TAB_KEY_SEPARATOR = "\u001f";

export function cleanPath(path: string): string {
  return path.replace(/^\/+|\/+$/g, "");
}

export function normalizeCheckoutRef(ref: CheckoutRef): CheckoutRef {
  const target = ref.target?.trim();
  const repo = ref.repo?.trim();
  return {
    scope: ref.scope,
    ...(target ? { target } : {}),
    ...(ref.scope === "agent" && repo ? { repo } : {}),
  };
}

export function checkoutRefKey(ref: CheckoutRef): string {
  const normalized = normalizeCheckoutRef(ref);
  return [
    normalized.scope,
    normalized.target ?? "",
    normalized.scope === "agent" ? (normalized.repo ?? "") : "",
  ].join(":");
}

export function sameCheckoutRef(a: CheckoutRef, b: CheckoutRef): boolean {
  return checkoutRefKey(a) === checkoutRefKey(b);
}

export function tabIdentityKey(tab: CheckoutTabIdentity): string {
  return `${checkoutRefKey(tab.ref)}${TAB_KEY_SEPARATOR}${cleanPath(tab.path)}`;
}

export function checkoutLabel(ref: CheckoutRef): string {
  const normalized = normalizeCheckoutRef(ref);
  if (normalized.scope === "workspace") return "Workspace files";
  if (normalized.scope === "repo") return normalized.target ?? "Repo";
  if (normalized.repo)
    return `${normalized.target ?? "Agent"} · ${normalized.repo}`;
  return normalized.target ?? "Agent";
}

export function checkoutSuffix(ref: CheckoutRef): string {
  const normalized = normalizeCheckoutRef(ref);
  if (normalized.scope === "workspace") return "Workspace";
  return normalized.target ?? checkoutLabel(normalized);
}

export function checkoutTitle(ref: CheckoutRef, path: string): string {
  return `${checkoutLabel(ref)}: ${cleanPath(path)}`;
}

export function legacyScopeStorageKey(ref: CheckoutRef): string {
  const normalized = normalizeCheckoutRef(ref);
  const target =
    normalized.target && normalized.target.trim()
      ? normalized.target.trim()
      : "root";
  return `file-browser-tabs:v2:${normalized.scope}:${target}`;
}

export interface MappedWorkspacePath {
  ref: CheckoutRef;
  path: string;
}

export function mapWorkspaceIndexPathToCheckout(
  rawPath: string,
  knownRefs: CheckoutRef[],
): MappedWorkspacePath {
  const path = cleanPath(rawPath);
  const known = new Map(knownRefs.map((ref) => [checkoutRefKey(ref), ref]));
  const parts = path.split("/").filter(Boolean);

  if (parts[0] === "worktrees" && parts.length >= 4) {
    const repo = parts[1] ?? "";
    const agent = parts[2] ?? "";
    const rest = parts.slice(3).join("/");
    const exact: CheckoutRef = { scope: "agent", target: agent, repo };
    const flattened: CheckoutRef = { scope: "agent", target: agent };
    return {
      ref:
        known.get(checkoutRefKey(exact)) ??
        known.get(checkoutRefKey(flattened)) ??
        exact,
      path: rest,
    };
  }

  if (parts.length >= 2) {
    const repo = parts[0] ?? "";
    const rest = parts.slice(1).join("/");
    const ref: CheckoutRef = { scope: "repo", target: repo };
    if (known.has(checkoutRefKey(ref))) {
      return { ref: known.get(checkoutRefKey(ref)) ?? ref, path: rest };
    }
  }

  return { ref: { scope: "workspace" }, path };
}
