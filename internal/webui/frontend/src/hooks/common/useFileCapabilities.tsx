import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

import {
  getFileCapabilities,
  type FileCapabilitiesResponse,
} from "@/api/workspace";
import { ScopedQueryRequest } from "./scopedQueryRequest";
import { QueryRecoveryContext } from "./queryRecovery";
import { useEventContext } from "./useEventProvider";

export interface FileCapabilitiesState {
  capabilities: FileCapabilitiesResponse | null;
  isLoading: boolean;
  error: string | null;
  retry: () => void;
}

const unavailableState: FileCapabilitiesState = {
  capabilities: null,
  isLoading: true,
  error: null,
  retry: () => {},
};

// What a consumer sees when the surrounding section declared it has no scoped
// files: settled, not loading, not an error. Nothing is pending, so nothing
// should render a "checking permissions" or "permissions unavailable" notice.
const notApplicableState: FileCapabilitiesState = {
  capabilities: null,
  isLoading: false,
  error: null,
  retry: () => {},
};

const FileCapabilitiesContext =
  createContext<FileCapabilitiesState>(unavailableState);

export function FileCapabilitiesProvider({
  workspaceId,
  enabled = true,
  children,
}: {
  workspaceId: string;
  /** False for a section with no scoped files: never fetch /files/capabilities. */
  enabled?: boolean | undefined;
  children: ReactNode;
}): JSX.Element {
  const [capabilities, setCapabilities] =
    useState<FileCapabilitiesResponse | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const recovery = useContext(QueryRecoveryContext);
  const { connectionEpoch } = useEventContext();
  const request = useMemo(
    () =>
      new ScopedQueryRequest({
        load: (signal) => {
          if (!enabled || !workspaceId)
            return Promise.reject(new Error("File capabilities disabled"));
          return getFileCapabilities(workspaceId, { signal });
        },
        commit: (next) => {
          setCapabilities(next);
          setError(null);
        },
        onError: (reason) => {
          setCapabilities(null);
          setError(reason.message);
        },
        onLoading: (loading) => {
          if (loading) {
            setCapabilities(null);
            setError(null);
          }
          setIsLoading(loading);
        },
      }),
    [workspaceId, enabled],
  );
  const retry = useCallback(() => {
    void request.run({ fresh: true }).catch(() => {});
  }, [request]);

  useEffect(() => {
    setCapabilities(null);
    setError(null);
    setIsLoading(enabled);
    return () => request.cancel();
  }, [request, enabled]);
  useEffect(() => {
    if (enabled && workspaceId) retry();
  }, [enabled, workspaceId, retry, connectionEpoch]);
  useEffect(() => {
    if (!enabled || !workspaceId || !recovery) return;
    return recovery.register("file capabilities", (signal) =>
      request.run({ signal, fresh: true }),
    );
  }, [enabled, workspaceId, recovery, request]);

  const value = useMemo(
    () =>
      enabled ? { capabilities, isLoading, error, retry } : notApplicableState,
    [capabilities, enabled, error, isLoading, retry],
  );
  return (
    <FileCapabilitiesContext.Provider value={value}>
      {children}
    </FileCapabilitiesContext.Provider>
  );
}

export function useFileCapabilities(): FileCapabilitiesState {
  return useContext(FileCapabilitiesContext);
}
