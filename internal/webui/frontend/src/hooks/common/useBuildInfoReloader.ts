import { useCallback, useEffect, useRef } from "react";

import {
  fetchBuildInfo,
  type BuildInfo,
  type ConnectionState,
} from "@/api/common";

const RELOAD_GUARD_KEY = "loom:build-info-reload:v1";
const DEFAULT_CHECK_INTERVAL_MS = 60_000;

type FetchBuildInfo = (signal?: AbortSignal) => Promise<BuildInfo>;

export interface UseBuildInfoReloaderOptions {
  connectionState: ConnectionState;
  intervalMs?: number;
  fetcher?: FetchBuildInfo;
  reload?: () => void;
  storage?: Pick<Storage, "getItem" | "setItem"> | null;
}

function reloadWindow(): void {
  window.location.reload();
}

function sessionStorageOrNull(): Pick<Storage, "getItem" | "setItem"> | null {
  if (typeof window === "undefined") return null;
  try {
    return window.sessionStorage;
  } catch {
    return null;
  }
}

function guardedGet(
  storage: Pick<Storage, "getItem" | "setItem"> | null,
): string | null {
  if (!storage) return null;
  try {
    return storage.getItem(RELOAD_GUARD_KEY);
  } catch {
    return null;
  }
}

function guardedSet(
  storage: Pick<Storage, "getItem" | "setItem"> | null,
  value: string,
): void {
  if (!storage) return;
  try {
    storage.setItem(RELOAD_GUARD_KEY, value);
  } catch {
    // Storage can be disabled; reloading once is still the safer recovery.
  }
}

export function useBuildInfoReloader({
  connectionState,
  intervalMs = DEFAULT_CHECK_INTERVAL_MS,
  fetcher = fetchBuildInfo,
  reload = reloadWindow,
  storage = sessionStorageOrNull(),
}: UseBuildInfoReloaderOptions): void {
  const initialHashRef = useRef<string | null>(null);
  const inFlightRef = useRef(false);
  const abortRef = useRef<AbortController | null>(null);
  const previousConnectionStateRef = useRef<ConnectionState>(connectionState);

  const checkBuildInfo = useCallback(() => {
    if (inFlightRef.current) return;
    inFlightRef.current = true;

    const controller = new AbortController();
    abortRef.current = controller;

    void fetcher(controller.signal)
      .then((info) => {
        const nextHash = info.frontend_hash;
        if (!nextHash) return;

        const initialHash = initialHashRef.current;
        if (!initialHash) {
          initialHashRef.current = nextHash;
          return;
        }
        if (nextHash === initialHash) return;
        if (guardedGet(storage) === nextHash) return;

        guardedSet(storage, nextHash);
        reload();
      })
      .catch(() => {
        // Build drift detection is recovery-only. Network errors are handled by
        // the existing connection stores and stale-data banner.
      })
      .finally(() => {
        if (abortRef.current === controller) {
          abortRef.current = null;
        }
        inFlightRef.current = false;
      });
  }, [fetcher, reload, storage]);

  useEffect(() => {
    checkBuildInfo();
    const interval = setInterval(checkBuildInfo, intervalMs);

    const handleVisibilityChange = () => {
      if (document.visibilityState === "visible") {
        checkBuildInfo();
      }
    };
    document.addEventListener("visibilitychange", handleVisibilityChange);

    return () => {
      clearInterval(interval);
      document.removeEventListener("visibilitychange", handleVisibilityChange);
      abortRef.current?.abort();
      abortRef.current = null;
      inFlightRef.current = false;
    };
  }, [checkBuildInfo, intervalMs]);

  useEffect(() => {
    const previous = previousConnectionStateRef.current;
    previousConnectionStateRef.current = connectionState;
    if (connectionState === "connected" && previous !== "connected") {
      checkBuildInfo();
    }
  }, [checkBuildInfo, connectionState]);
}
