import { useCallback, useContext, useEffect, useMemo, useState } from "react";
import { listFileCheckouts } from "@/hooks/api";
import type { FileCheckout } from "@/api/workspace";
import { ScopedQueryRequest } from "@/utils/scopedQueryRequest";
import { QueryRecoveryContext } from "./queryRecovery";

/** Mounted browser metadata only; directory and document state have other owners. */
export function useFileCheckouts(workspaceId: string, enabled: boolean) {
  const [checkouts, setCheckouts] = useState<FileCheckout[]>([]);
  const [checkoutsSettled, setSettled] = useState(false);
  const [checkoutError, setError] = useState<string | null>(null);
  const recovery = useContext(QueryRecoveryContext);
  const request = useMemo(
    () =>
      new ScopedQueryRequest({
        load: (signal) => {
          if (!enabled || !workspaceId)
            return Promise.reject(new Error("File checkouts disabled"));
          return listFileCheckouts(workspaceId, { signal });
        },
        validateRecovery: (data) => {
          if (
            data.partial ||
            data.limit_hit ||
            data.errors.length ||
            data.checkouts.some(
              (checkout) =>
                checkout.partial ||
                checkout.limit_hit ||
                checkout.status_error ||
                !!checkout.error,
            )
          ) {
            throw new Error("Checkout metadata is incomplete");
          }
        },
        commit: (data) => {
          setCheckouts(data.checkouts);
          setError(null);
        },
        onError: (error) => setError(error.message),
        onLoading: (loading) => {
          if (!loading) setSettled(true);
        },
      }),
    [workspaceId, enabled],
  );
  useEffect(() => {
    setCheckouts([]);
    setError(null);
    setSettled(false);
    return () => request.cancel();
  }, [request]);
  const refreshCheckouts = useCallback(async () => {
    if (!enabled || !workspaceId) return;
    await request.run({ fresh: true }).catch(() => {});
  }, [request, enabled, workspaceId]);
  const refreshCheckoutsForRepair = useCallback(() => {
    if (!enabled || !workspaceId)
      return Promise.reject(new Error("File checkouts disabled"));
    return request.run({ fresh: true, signal: AbortSignal.timeout(15000) });
  }, [request, enabled, workspaceId]);
  useEffect(() => {
    if (!enabled || !workspaceId || !recovery) return;
    return recovery.register("file checkout metadata", (signal) =>
      request.run({ signal, fresh: true }),
    );
  }, [request, enabled, workspaceId, recovery]);
  return {
    checkouts,
    checkoutsSettled,
    checkoutError,
    refreshCheckouts,
    refreshCheckoutsForRepair,
  };
}
