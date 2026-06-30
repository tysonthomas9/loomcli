/**
 * useFileContent - React hook for fetching file content on demand.
 * Follows useIssueDetail pattern: requestIdRef for latest-wins, mountedRef for cleanup.
 *
 * The stateful core (useFileContentCore) is shared by two sources: an agent
 * worktree (useFileContent) and the workspace folder (useWorkspaceFileContent).
 */

import { useState, useCallback, useRef, useEffect } from "react";
import { readWorktreeFile, readWorkspaceFile } from "@/api/workspace";
import type { FileReadData } from "@/api/workspace";
import { useWorkspaceContext } from "@/hooks/workspace";

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

/** FileReader fetches a single file's content + metadata by path. */
type FileReader = (path: string) => Promise<FileReadData>;

function useFileContentCore(
  readFile: FileReader,
  enabled: boolean,
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
      if (!path || !enabled) return;

      const requestId = ++currentRequestIdRef.current;

      setIsLoading(true);
      setError(null);

      try {
        const data = await readFile(path);
        if (requestId === currentRequestIdRef.current && mountedRef.current) {
          setFileData(data);
          setError(null);
        }
      } catch (err) {
        if (requestId === currentRequestIdRef.current && mountedRef.current) {
          // Clear stale content so the viewer shows the error, not the
          // previously-open file's contents.
          setFileData(null);
          setError(err instanceof Error ? err.message : String(err));
        }
      } finally {
        if (requestId === currentRequestIdRef.current && mountedRef.current) {
          setIsLoading(false);
        }
      }
    },
    [readFile, enabled],
  );

  const clearFile = useCallback(() => {
    currentRequestIdRef.current++;
    setFileData(null);
    setError(null);
    setIsLoading(false);
  }, []);

  return { fileData, isLoading, error, fetchFile, clearFile };
}

export function useFileContent(agentName: string): UseFileContentReturn {
  const { workspaceId } = useWorkspaceContext();
  const readFile = useCallback<FileReader>(
    (path) => readWorktreeFile(workspaceId, agentName, path),
    [workspaceId, agentName],
  );
  return useFileContentCore(readFile, !!agentName);
}

/**
 * useWorkspaceFileContent reads files from the workspace folder root
 * (read-only), used by the dedicated file browser.
 */
export function useWorkspaceFileContent(): UseFileContentReturn {
  const { workspaceId } = useWorkspaceContext();
  const readFile = useCallback<FileReader>(
    (path) => readWorkspaceFile(workspaceId, path),
    [workspaceId],
  );
  return useFileContentCore(readFile, true);
}
