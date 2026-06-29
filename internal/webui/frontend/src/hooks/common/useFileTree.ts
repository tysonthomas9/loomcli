/**
 * useFileTree - React hook for managing file tree navigation state.
 * Handles directory expansion/collapse, entry caching, file selection,
 * and filter text with debounce.
 */

import { useState, useCallback, useRef, useEffect } from "react";
import { listWorktreeDir } from "@/api/workspace";
import type { FileEntry } from "@/api/workspace";
import { useDebounce } from "./useDebounce";
import { useWorkspaceContext } from "@/hooks/workspace";

export interface UseFileTreeOptions {
  /**
   * Browse the workspace primary repository instead of an agent worktree.
   * Used for lead agents, which have no local worktree; the API resolves
   * against the primary repo when the agent is a lead.
   */
  useWorkspaceTree?: boolean;
}

export interface UseFileTreeReturn {
  /** True when browsing the workspace primary repo (lead fallback). */
  isWorkspaceTree: boolean;
  /** Set of expanded directory paths */
  expanded: Set<string>;
  /** Cached directory entries keyed by path */
  treeData: Map<string, FileEntry[]>;
  /** Currently selected file path, null if none */
  selectedPath: string | null;
  /** Whether the root directory is currently loading */
  isLoading: boolean;
  /** Error from the last directory fetch, null if successful */
  error: string | null;
  /** Current filter text (raw, un-debounced) */
  filterText: string;
  /** Debounced filter text for use in rendering */
  debouncedFilterText: string;
  /** Toggle a directory open/closed. Fetches contents on first expand. */
  toggle: (dirPath: string) => Promise<void>;
  /** Load a directory's contents explicitly (e.g., refresh) */
  loadDir: (dirPath: string) => Promise<void>;
  /** Select a file by path */
  selectFile: (filePath: string | null) => void;
  /** Set the filter text */
  setFilterText: (text: string) => void;
}

export function useFileTree(
  agentName: string,
  options?: UseFileTreeOptions,
): UseFileTreeReturn {
  const useWorkspaceTree = options?.useWorkspaceTree ?? false;
  const { workspaceId } = useWorkspaceContext();
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [treeData, setTreeData] = useState<Map<string, FileEntry[]>>(new Map());
  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);
  const [filterText, setFilterText] = useState<string>("");

  const debouncedFilterText = useDebounce(filterText, 200);

  const mountedRef = useRef<boolean>(true);
  const rootRequestIdRef = useRef<number>(0);
  // Use refs for expanded/treeData so toggle callback is stable
  const expandedRef = useRef<Set<string>>(expanded);
  expandedRef.current = expanded;
  const treeDataRef = useRef<Map<string, FileEntry[]>>(treeData);
  treeDataRef.current = treeData;

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const loadDir = useCallback(
    async (dirPath: string): Promise<void> => {
      if (!agentName) return;
      try {
        const result = await listWorktreeDir(
          workspaceId,
          agentName,
          dirPath || undefined,
        );
        if (mountedRef.current) {
          setTreeData((prev) => {
            const next = new Map(prev);
            next.set(dirPath, result.entries);
            return next;
          });
          setError(null);
        }
      } catch (err) {
        if (mountedRef.current) {
          setError(err instanceof Error ? err.message : String(err));
        }
      }
    },
    [workspaceId, agentName],
  );

  const toggle = useCallback(
    async (dirPath: string): Promise<void> => {
      const wasExpanded = expandedRef.current.has(dirPath);

      setExpanded((prev) => {
        const next = new Set(prev);
        if (next.has(dirPath)) {
          next.delete(dirPath);
        } else {
          next.add(dirPath);
        }
        return next;
      });

      // If expanding and not already cached, fetch
      if (!wasExpanded && !treeDataRef.current.has(dirPath)) {
        await loadDir(dirPath);
      }
    },
    [loadDir],
  );

  const selectFile = useCallback((filePath: string | null) => {
    setSelectedPath(filePath);
  }, []);

  // Auto-load root on mount / agent change
  useEffect(() => {
    if (!agentName) return;
    const requestId = ++rootRequestIdRef.current;
    setIsLoading(true);
    setExpanded(new Set());
    setTreeData(new Map());
    setSelectedPath(null);
    setError(null);

    listWorktreeDir(workspaceId, agentName)
      .then((result) => {
        if (requestId === rootRequestIdRef.current && mountedRef.current) {
          setTreeData(new Map([["", result.entries]]));
          setExpanded(new Set([""]));
          setIsLoading(false);
        }
      })
      .catch((err) => {
        if (requestId === rootRequestIdRef.current && mountedRef.current) {
          setError(err instanceof Error ? err.message : String(err));
          setIsLoading(false);
        }
      });
  }, [workspaceId, agentName]);

  return {
    isWorkspaceTree: useWorkspaceTree,
    expanded,
    treeData,
    selectedPath,
    isLoading,
    error,
    filterText,
    debouncedFilterText,
    toggle,
    loadDir,
    selectFile,
    setFilterText,
  };
}
