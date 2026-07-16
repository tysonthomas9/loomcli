import type { FileCheckout } from "@/api/workspace";

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
