import { useEffect, useMemo, useState } from "react";
import { getHarness } from "@/api/agentsv1";
import type { HarnessInfo } from "@/api/agentsv1";

/**
 * The harness's health (why it is unavailable, or that it is newer than
 * tested), reread when the agent's Attention changes; none until that
 * reread answers, so an earlier reason never stands for a new Attention.
 */
export function useHarnessHealth(
  ws: string,
  harness: string | undefined,
  attention: string | null | undefined,
): HarnessInfo["health"] | null {
  // A new token on every change, even back to an earlier Attention.
  const token = useMemo(() => ({}), [ws, harness, attention]); // eslint-disable-line react-hooks/exhaustive-deps
  const [got, setGot] = useState<{ token: object; h: HarnessInfo["health"] }>();
  useEffect(() => {
    if (!harness) return;
    let live = true;
    getHarness(ws, harness).then(
      (r) => live && setGot({ token, h: r.health }),
      () => {},
    );
    return () => {
      live = false;
    };
  }, [ws, harness, token]);
  return got?.token === token ? got.h : null;
}
