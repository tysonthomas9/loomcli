/**
 * useFileContent - React hook for fetching file content on demand.
 * Follows useIssueDetail pattern: requestIdRef for latest-wins, mountedRef for cleanup.
 */

import { useState, useCallback, useRef, useEffect } from "react";
import { readWorktreeFile } from "@/api/files";
import type { FileReadData } from "@/api/files";

export interface UseFileContentReturn {
  /** File content data, null if not loaded */
  fileData: FileReadData | null;
  /** Whether a fetch is currently in progress */
  isLoading: boolean;
  /** Error from the last fetch attempt, null if successful */
  error: string | null;
  /** Fetch content for a file by path */
  fetchFile: (path: string) => Promise<void>;
  /** Clear the current file content */
  clearFile: () => void;
}

export function useFileContent(
  workspaceId: string,
  agentName: string,
): UseFileContentReturn {
  const [fileData, setFileData] = useState<FileReadData | null>(null);
  const [isLoading, setIsLoading] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);

  const currentRequestIdRef = useRef<number>(0);
  const mountedRef = useRef<boolean>(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const fetchFile = useCallback(
    async (path: string): Promise<void> => {
      if (!path || !agentName) return;

      const requestId = ++currentRequestIdRef.current;

      setIsLoading(true);
      setError(null);

      try {
        const data = await readWorktreeFile(workspaceId, agentName, path);
        if (requestId === currentRequestIdRef.current && mountedRef.current) {
          setFileData(data);
          setError(null);
        }
      } catch (err) {
        if (requestId === currentRequestIdRef.current && mountedRef.current) {
          setError(err instanceof Error ? err.message : String(err));
        }
      } finally {
        if (requestId === currentRequestIdRef.current && mountedRef.current) {
          setIsLoading(false);
        }
      }
    },
    [workspaceId, agentName],
  );

  const clearFile = useCallback(() => {
    currentRequestIdRef.current++;
    setFileData(null);
    setError(null);
    setIsLoading(false);
  }, []);

  return { fileData, isLoading, error, fetchFile, clearFile };
}
