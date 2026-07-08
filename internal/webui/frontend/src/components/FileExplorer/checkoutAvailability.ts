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

export function unavailableCheckoutCount(checkouts: FileCheckout[]): number {
  return checkouts.filter((checkout) => checkout.status_error === true).length;
}
