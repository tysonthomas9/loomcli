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
  const [requestToken, setRequestToken] = useState(0);
  const retry = useCallback(() => setRequestToken((token) => token + 1), []);

  useEffect(() => {
    if (!enabled) return;
    let canceled = false;
    setCapabilities(null);
    setIsLoading(true);
    setError(null);
    getFileCapabilities(workspaceId)
      .then((next) => {
        if (!canceled) setCapabilities(next);
      })
      .catch((reason) => {
        if (!canceled) {
          setError(reason instanceof Error ? reason.message : String(reason));
        }
      })
      .finally(() => {
        if (!canceled) setIsLoading(false);
      });
    return () => {
      canceled = true;
    };
  }, [enabled, requestToken, workspaceId]);

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
