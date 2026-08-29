import type { SkillsScopeGroup } from "@/api/workspace";
import {
  checkoutLabel,
  checkoutRefKey,
  checkoutSuffix,
  checkoutTitle,
  cleanPath,
  sameCheckoutRef,
  normalizeCheckoutRef,
  type CheckoutRef,
} from "@/utils/fileExplorerRefs";

export type ExplorerRef =
  | { kind: "checkout"; checkout: CheckoutRef }
  | { kind: "skills"; group: SkillsScopeGroup };

export type CheckoutExplorerRef = Extract<ExplorerRef, { kind: "checkout" }>;
export type SkillsExplorerRef = Extract<ExplorerRef, { kind: "skills" }>;

export function checkoutExplorerRef(ref: CheckoutRef): CheckoutExplorerRef {
  return { kind: "checkout", checkout: ref };
}

export function skillsExplorerRef(group: SkillsScopeGroup): SkillsExplorerRef {
  return { kind: "skills", group };
}

export function normalizeExplorerRef(ref: ExplorerRef): ExplorerRef {
  if (ref.kind === "checkout") {
    return checkoutExplorerRef(normalizeCheckoutRef(ref.checkout));
  }
  return ref.group.kind === "workspace"
    ? skillsExplorerRef({ kind: "workspace" })
    : skillsExplorerRef({ kind: "role", role: ref.group.role.trim() });
}

export function isSkillsRef(ref: ExplorerRef): ref is SkillsExplorerRef {
  return ref.kind === "skills";
}

export function asCheckoutRef(ref: ExplorerRef): CheckoutRef | null {
  return ref.kind === "checkout" ? ref.checkout : null;
}

export function explorerRefKey(ref: ExplorerRef): string {
  if (ref.kind === "checkout") return checkoutRefKey(ref.checkout);
  return ref.group.kind === "workspace"
    ? "skills:workspace:"
    : `skills:role:${ref.group.role}:`;
}

export function sameExplorerRef(a: ExplorerRef, b: ExplorerRef): boolean {
  if (a.kind !== b.kind) return false;
  if (a.kind === "checkout" && b.kind === "checkout") {
    return sameCheckoutRef(a.checkout, b.checkout);
  }
  return explorerRefKey(a) === explorerRefKey(b);
}

export function explorerLabel(ref: ExplorerRef): string {
  if (ref.kind === "checkout") return checkoutLabel(ref.checkout);
  return ref.group.kind === "workspace"
    ? "Workspace skills"
    : `Role: ${ref.group.role}`;
}

export function explorerSuffix(ref: ExplorerRef): string {
  if (ref.kind === "checkout") return checkoutSuffix(ref.checkout);
  return ref.group.kind === "workspace" ? "Workspace" : ref.group.role;
}

export function explorerTitle(ref: ExplorerRef, path: string): string {
  if (ref.kind === "checkout") return checkoutTitle(ref.checkout, path);
  return `${explorerLabel(ref)}: ${cleanPath(path)}`;
}

export function tabIdentityKey(tab: {
  ref: ExplorerRef;
  path: string;
}): string {
  return `${explorerRefKey(tab.ref)}\u001f${cleanPath(tab.path)}`;
}
