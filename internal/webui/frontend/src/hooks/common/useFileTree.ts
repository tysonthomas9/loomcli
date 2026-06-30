/**
 * useFileTree - React hook for managing file tree navigation state.
 * Handles directory expansion/collapse, entry caching, file selection,
 * and filter text with debounce.
 *
 * The stateful core (useFileTreeCore) is shared by two sources: an agent
 * worktree (useFileTree) and the workspace folder (useWorkspaceFileTree). The
 * only difference is the directory loader, so behavior stays identical.
 */

import { useState, useCallback, useRef, useEffect } from "react";
import { listWorktreeDir, listWorkspaceDir } from "@/api/workspace";
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
  /** Load + expand every directory along a path so the UI can jump to it. */
  revealPath: (path: string) => Promise<void>;
  /** Select a file by path */
  selectFile: (filePath: string | null) => void;
  /** Set the filter text */
  setFilterText: (text: string) => void;
}

/** DirLoader fetches one directory level's entries ("" = root). */
type DirLoader = (path: string) => Promise<FileEntry[]>;

/** Segments never worth requesting on a reveal (the API hides/denies them). */
const HIDDEN_SEGMENTS = new Set([".git", "node_modules"]);

function useFileTreeCore(
  loadEntries: DirLoader,
  enabled: boolean,
  isWorkspaceTree: boolean,
): UseFileTreeReturn {
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
      if (!enabled) return;
      try {
        const entries = await loadEntries(dirPath);
        if (mountedRef.current) {
          setTreeData((prev) => {
            const next = new Map(prev);
            next.set(dirPath, entries);
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
    [loadEntries, enabled],
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

  // revealPath loads and expands the root plus every directory along path so the
  // browser can jump straight to a nested folder. Descent stops at the first
  // segment that is not a directory; whatever loaded so far is still expanded.
  const revealPath = useCallback(
    async (rawPath: string): Promise<void> => {
      if (!enabled) return;
      const clean = rawPath.replace(/^\/+|\/+$/g, "").trim();
      const segments = clean ? clean.split("/").filter(Boolean) : [];
      const loaded: Array<[string, FileEntry[]]> = [];
      const expand: string[] = [""];
      try {
        let entries = await loadEntries("");
        loaded.push(["", entries]);
        let acc = "";
        for (const seg of segments) {
          if (HIDDEN_SEGMENTS.has(seg)) break;
          const entry = entries.find((e) => e.name === seg);
          // Stop at a missing segment or a file: the parent is already loaded
          // and the target is visible, so we avoid firing a predictable 4xx.
          if (!entry || !entry.is_dir) break;
          acc = acc ? `${acc}/${seg}` : seg;
          entries = await loadEntries(acc);
          loaded.push([acc, entries]);
          expand.push(acc);
        }
        if (mountedRef.current) setError(null);
      } catch {
        // Network error — land as deep as we got rather than failing the jump.
      }
      if (mountedRef.current && loaded.length > 0) {
        setTreeData((prev) => {
          const next = new Map(prev);
          for (const [p, entries] of loaded) next.set(p, entries);
          return next;
        });
        setExpanded((prev) => new Set([...prev, ...expand]));
      }
    },
    [loadEntries, enabled],
  );

  const selectFile = useCallback((filePath: string | null) => {
    setSelectedPath(filePath);
  }, []);

  // Auto-load root on mount / source change. loadEntries identity encodes the
  // source (agent or workspace), so a source change resets and reloads.
  useEffect(() => {
    if (!enabled) return;
    const requestId = ++rootRequestIdRef.current;
    setIsLoading(true);
    setExpanded(new Set());
    setTreeData(new Map());
    setSelectedPath(null);
    setError(null);

    loadEntries("")
      .then((entries) => {
        if (requestId === rootRequestIdRef.current && mountedRef.current) {
          setTreeData(new Map([["", entries]]));
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
  }, [loadEntries, enabled]);

  return {
    isWorkspaceTree,
    expanded,
    treeData,
    selectedPath,
    isLoading,
    error,
    filterText,
    debouncedFilterText,
    toggle,
    loadDir,
    revealPath,
    selectFile,
    setFilterText,
  };
}

export function useFileTree(
  agentName: string,
  options?: UseFileTreeOptions,
): UseFileTreeReturn {
  const { workspaceId } = useWorkspaceContext();
  const loadEntries = useCallback<DirLoader>(
    (path) =>
      (path
        ? listWorktreeDir(workspaceId, agentName, path)
        : listWorktreeDir(workspaceId, agentName)
      ).then((r) => r.entries),
    [workspaceId, agentName],
  );
  return useFileTreeCore(
    loadEntries,
    !!agentName,
    options?.useWorkspaceTree ?? false,
  );
}

/**
 * useWorkspaceFileTree browses the workspace folder root (read-only), which
 * spans every repo checkout and agent worktree. Same navigation behavior as
 * useFileTree, sourced from the scope=workspace file endpoint.
 */
export function useWorkspaceFileTree(): UseFileTreeReturn {
  const { workspaceId } = useWorkspaceContext();
  const loadEntries = useCallback<DirLoader>(
    (path) =>
      (path
        ? listWorkspaceDir(workspaceId, path)
        : listWorkspaceDir(workspaceId)
      ).then((r) => r.entries),
    [workspaceId],
  );
  return useFileTreeCore(loadEntries, true, false);
}
