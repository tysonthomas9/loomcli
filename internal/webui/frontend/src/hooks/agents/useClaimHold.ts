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
// Cadence for a workspace whose server can reach no agent supervisor: nothing
// can take a hold there, so a hold cannot appear between polls either.
const IDLE_POLL_MS = 60000;

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
  const [supervisorAvailable, setSupervisorAvailable] = useState(true);
  const mounted = useRef(true);
  const activeWorkspace = useRef(workspaceId);
  activeWorkspace.current = workspaceId;

  const refresh = useCallback(async () => {
    if (!workspaceId) return;
    const requestedWorkspace = workspaceId;
    try {
      const status = await fetchClaimHold(workspaceId);
      if (!mounted.current || activeWorkspace.current !== requestedWorkspace)
        return;
      setHold(status.hold ?? null);
      setRunning(status.running ?? []);
      setGated(status.gated ?? 0);
      if (!status.hold?.held) setCanForceRelease(false);
      setSupervisorAvailable(status.supervisor_available !== false);
    } catch {
      // A daemon without the claim-hold route (older build, remote mode, or
      // simply not running) is not an error state for a banner — there is
      // just nothing to show. The transport no longer reports these 503s
      // either (isOutageExemptPath in @/api/common), so do not "fix" one half
      // and leave the other. Reachability is deliberately NOT inferred here:
      // an older server that still 503s carries no field, and the slow poll
      // simply does not engage.
      if (mounted.current && activeWorkspace.current === requestedWorkspace) {
        setHold(null);
        setRunning([]);
        setGated(0);
      }
    }
  }, [workspaceId]);

  // Reset-and-prime: keyed on refresh identity (i.e. workspaceId) only, so a
  // workspace switch clears the banner and starts optimistic. It must NOT
  // depend on supervisorAvailable — this body fetches, and a
  // reachability-keyed re-run would re-fetch immediately and loop.
  useEffect(() => {
    mounted.current = true;
    setHold(null);
    setRunning([]);
    setGated(0);
    setBusy(false);
    setError(null);
    setCanForceRelease(false);
    setSupervisorAvailable(true);
    void refresh();
    return () => {
      mounted.current = false;
    };
  }, [refresh]);

  // Cadence: the only effect that knows about reachability. Its body creates
  // and clears a timer and does nothing else — no state writes, no refresh()
  // call — so re-running it on a reachability change cannot cause a fetch.
  // A host with no supervisor answers "no hold" forever; polling that at 10 s
  // was the entire client-error rate of the dashboard (PUPPET-529).
  useEffect(() => {
    const timer = setInterval(
      () => void refresh(),
      supervisorAvailable ? POLL_MS : IDLE_POLL_MS,
    );
    return () => clearInterval(timer);
  }, [refresh, supervisorAvailable]);

  const release = useCallback(
    async (force = false): Promise<boolean> => {
      if (!workspaceId) return false;
      const requestedWorkspace = workspaceId;
      setBusy(true);
      setError(null);
      setCanForceRelease(false);
      try {
        const status = await releaseClaimHold(workspaceId, { force });
        if (mounted.current && activeWorkspace.current === requestedWorkspace) {
          setHold(status.hold ?? null);
          setRunning(status.running ?? []);
          setGated(status.gated ?? 0);
          setCanForceRelease(false);
        }
        return true;
      } catch (e) {
        // A 409 here is the daemon refusing to let one operator silently undo
        // another's quiesce; surface its wording rather than a generic failure.
        if (mounted.current && activeWorkspace.current === requestedWorkspace) {
          setError(e instanceof Error ? e.message : "release failed");
          if (e instanceof ApiError && e.status === 409) {
            try {
              const status = await fetchClaimHold(requestedWorkspace);
              if (
                mounted.current &&
                activeWorkspace.current === requestedWorkspace
              ) {
                setHold(status.hold ?? null);
                setRunning(status.running ?? []);
                setGated(status.gated ?? 0);
                setCanForceRelease(Boolean(status.hold?.held));
              }
            } catch {
              // Never offer an unconditional force release without a fresh,
              // authoritative holder to name in the confirmation.
              if (
                mounted.current &&
                activeWorkspace.current === requestedWorkspace
              ) {
                setCanForceRelease(false);
              }
            }
          }
        }
        return false;
      } finally {
        if (mounted.current && activeWorkspace.current === requestedWorkspace) {
          setBusy(false);
        }
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
