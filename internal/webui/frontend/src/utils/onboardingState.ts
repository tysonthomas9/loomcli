import { wsGet, wsRemove, wsSet } from "@/utils/scopedStorage";

export const ONBOARDING_DISMISSED_KEY = "onboarding-dismissed";
export const ONBOARDING_RESTART_EVENT = "loom:onboarding-restart";

export interface OnboardingRestartDetail {
  workspaceId: string;
}

export function isOnboardingDismissed(workspaceId: string): boolean {
  return wsGet(workspaceId, ONBOARDING_DISMISSED_KEY) === "1";
}

export function dismissOnboarding(workspaceId: string): void {
  wsSet(workspaceId, ONBOARDING_DISMISSED_KEY, "1");
}

export function restartOnboarding(workspaceId: string): void {
  wsRemove(workspaceId, ONBOARDING_DISMISSED_KEY);
  if (typeof window !== "undefined") {
    window.dispatchEvent(
      new CustomEvent<OnboardingRestartDetail>(ONBOARDING_RESTART_EVENT, {
        detail: { workspaceId },
      }),
    );
  }
}
