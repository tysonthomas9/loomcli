/**
 * useOnboardingStatus — React hook that fetches onboarding status from
 * the server, joins it with the static step registry, and exposes a
 * per-workspace dismiss flag backed by scopedStorage.
 *
 * Workspace-less calls (the no-workspace landing page) pass undefined
 * for `workspaceId`; the hook then hits the top-level endpoint and
 * disables dismissal (there is no workspace to scope the flag to).
 */

import { useCallback, useEffect, useState } from "react";

import { fetchOnboardingStatus } from "@/api/onboarding";
import type {
  OnboardingStatusWire,
  OnboardingStepId,
  OnboardingStepStatus,
  OnboardingStepWire,
} from "@/types/onboarding";
import { wsGet, wsSet } from "@/utils/scopedStorage";

import {
  ONBOARDING_STEPS,
  type OnboardingStepDefinition,
} from "./stepRegistry";

export const ONBOARDING_DISMISS_KEY = "onboarding-dismissed";

/**
 * A step joined with its registry definition for rendering.
 */
export interface OnboardingStep extends OnboardingStepDefinition {
  status: OnboardingStepStatus;
  message?: string;
}

export interface UseOnboardingStatusResult {
  steps: OnboardingStep[];
  workspaceId: string | undefined;
  activeRepo: string | undefined;
  allComplete: boolean;
  isLoading: boolean;
  error: Error | null;
  isDismissed: boolean;
  dismiss: () => void;
  refetch: () => Promise<void>;
}

function joinSteps(wire: OnboardingStatusWire): OnboardingStep[] {
  const wireById = new Map<OnboardingStepId, OnboardingStepWire>(
    wire.steps.map((s) => [s.id, s]),
  );
  // Render in registry order so the UI is stable even if the server
  // reorders steps in a future revision.
  return ONBOARDING_STEPS.map((def) => {
    const w = wireById.get(def.id);
    const step: OnboardingStep = {
      ...def,
      status: w?.status ?? "blocked",
    };
    if (w?.message) {
      step.message = w.message;
    }
    return step;
  });
}

export function useOnboardingStatus(
  workspaceId: string | undefined,
): UseOnboardingStatusResult {
  const [wire, setWire] = useState<OnboardingStatusWire | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);
  const [isDismissed, setIsDismissed] = useState(() => {
    if (!workspaceId) return false;
    return wsGet(workspaceId, ONBOARDING_DISMISS_KEY) === "1";
  });

  const refetch = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      const data = await fetchOnboardingStatus(workspaceId);
      setWire(data);
    } catch (e) {
      setError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      setIsLoading(false);
    }
  }, [workspaceId]);

  useEffect(() => {
    void refetch();
  }, [refetch]);

  // Re-read dismiss flag when the workspace changes.
  useEffect(() => {
    if (!workspaceId) {
      setIsDismissed(false);
      return;
    }
    setIsDismissed(wsGet(workspaceId, ONBOARDING_DISMISS_KEY) === "1");
  }, [workspaceId]);

  const dismiss = useCallback(() => {
    if (!workspaceId) return;
    wsSet(workspaceId, ONBOARDING_DISMISS_KEY, "1");
    setIsDismissed(true);
  }, [workspaceId]);

  return {
    steps: wire ? joinSteps(wire) : [],
    workspaceId: wire?.workspace_id,
    activeRepo: wire?.active_repo,
    allComplete: wire?.all_complete ?? false,
    isLoading,
    error,
    isDismissed,
    dismiss,
    refetch,
  };
}
