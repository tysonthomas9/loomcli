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

const FileCapabilitiesContext =
  createContext<FileCapabilitiesState>(unavailableState);

export function FileCapabilitiesProvider({
  workspaceId,
  children,
}: {
  workspaceId: string;
  children: ReactNode;
}): JSX.Element {
  const [capabilities, setCapabilities] =
    useState<FileCapabilitiesResponse | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [requestToken, setRequestToken] = useState(0);
  const retry = useCallback(() => setRequestToken((token) => token + 1), []);

  useEffect(() => {
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
  }, [requestToken, workspaceId]);

  const value = useMemo(
    () => ({ capabilities, isLoading, error, retry }),
    [capabilities, error, isLoading, retry],
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
