import type { FileCheckout } from "@/api/workspace";
import type { CheckoutRef } from "@/utils/fileExplorerRefs";

export function checkoutRepairRequest(ref: CheckoutRef, force = false) {
  if (ref.scope === "agent" && ref.target) {
    const request = {
      scope: "agent" as const,
      target: ref.target,
      force,
    };
    if (ref.repo) {
      return { ...request, repo: ref.repo };
    }
    return request;
  }
  if (ref.scope === "repo" && ref.target) {
    return { scope: "repo" as const, target: ref.target, force };
  }
  return null;
}

export function hasAvailableCheckoutStatus(
  checkout: Pick<FileCheckout, "exists" | "status_error">,
): boolean {
  return checkout.exists && checkout.status_error !== true;
}

export function checkoutChangeCount(
  checkout: FileCheckout | undefined,
): number {
  if (!checkout || !hasAvailableCheckoutStatus(checkout)) return 0;
  return checkout.change_count;
}

export function checkoutDisplayName(checkout: FileCheckout): string {
  if (checkout.kind === "agent") {
    return checkout.agent
      ? `${checkout.agent} · ${checkout.repo}`
      : `agent · ${checkout.repo}`;
  }
  return checkout.repo;
}

export function unavailableCheckoutLabels(checkouts: FileCheckout[]): string[] {
  return checkouts
    .filter((checkout) => checkout.status_error === true)
    .map(checkoutDisplayName);
}

export function unavailableCheckoutSummary(labels: string[]): string {
  if (labels.length === 0) return "";
  const visible = labels.slice(0, 3);
  const suffix =
    labels.length > visible.length
      ? ` and ${labels.length - visible.length} more`
      : "";
  return `${visible.join(", ")}${suffix} unavailable`;
}
