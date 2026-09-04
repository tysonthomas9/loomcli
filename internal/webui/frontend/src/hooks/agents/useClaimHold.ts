/**
 * useClaimHold polls the workspace claim hold and releases it.
 *
 * A hold is a rare, deliberately visible state — it means the daemon is
 * refusing to start new work — so the banner it feeds must never be stale for
 * long, and must disappear promptly once the hold is released or expires.
 *
 * Owning the api calls here keeps ClaimHoldBanner presentational and inside
 * the components→hooks boundary (components must not import @/api directly).
 */

import { useCallback, useEffect, useRef, useState } from "react";

import { ApiError } from "@/api/common";
import {
  fetchClaimHold,
  releaseClaimHold,
  type ClaimHold,
  type ClaimHoldRunningAgent,
} from "@/api/agents/claimHold";
import { useWorkspaceContext } from "@/hooks/workspace";

const POLL_MS = 10000;

export interface UseClaimHoldReturn {
  /** null when claims are free. */
  hold: ClaimHold | null;
  /** Agents whose runs were already in flight; a hold never touches them. */
  running: ClaimHoldRunningAgent[];
  /** How many agents are cycling but gated. */
  gated: number;
  busy: boolean;
  error: string | null;
  /** True when release was refused because another actor owns the hold. */
  canForceRelease: boolean;
  /** Resolves true when the hold was released. */
  release: (force?: boolean) => Promise<boolean>;
  refresh: () => Promise<void>;
}

export function useClaimHold(): UseClaimHoldReturn {
  const { workspaceId } = useWorkspaceContext();
  const [hold, setHold] = useState<ClaimHold | null>(null);
  const [running, setRunning] = useState<ClaimHoldRunningAgent[]>([]);
  const [gated, setGated] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [canForceRelease, setCanForceRelease] = useState(false);
  const mounted = useRef(true);

  const refresh = useCallback(async () => {
    if (!workspaceId) return;
    try {
      const status = await fetchClaimHold(workspaceId);
      if (!mounted.current) return;
      setHold(status.hold ?? null);
      setRunning(status.running ?? []);
      setGated(status.gated ?? 0);
      if (!status.hold?.held) setCanForceRelease(false);
    } catch {
      // A daemon without the claim-hold route (older build, remote mode, or
      // simply not running) is not an error state for a banner — there is
      // just nothing to show.
      if (mounted.current) {
        setHold(null);
        setRunning([]);
        setGated(0);
      }
    }
  }, [workspaceId]);

  useEffect(() => {
    mounted.current = true;
    void refresh();
    const timer = setInterval(() => void refresh(), POLL_MS);
    return () => {
      mounted.current = false;
      clearInterval(timer);
    };
  }, [refresh]);

  const release = useCallback(
    async (force = false): Promise<boolean> => {
      if (!workspaceId) return false;
      setBusy(true);
      setError(null);
      setCanForceRelease(false);
      try {
        const status = await releaseClaimHold(workspaceId, { force });
        if (mounted.current) {
          setHold(status.hold ?? null);
          setRunning(status.running ?? []);
          setGated(status.gated ?? 0);
          setCanForceRelease(false);
        }
        return true;
      } catch (e) {
        // A 409 here is the daemon refusing to let one operator silently undo
        // another's quiesce; surface its wording rather than a generic failure.
        if (mounted.current) {
          setError(e instanceof Error ? e.message : "release failed");
          setCanForceRelease(e instanceof ApiError && e.status === 409);
        }
        return false;
      } finally {
        if (mounted.current) setBusy(false);
      }
    },
    [workspaceId],
  );

  return {
    hold,
    running,
    gated,
    busy,
    error,
    canForceRelease,
    release,
    refresh,
  };
}

export type { ClaimHold, ClaimHoldRunningAgent };
