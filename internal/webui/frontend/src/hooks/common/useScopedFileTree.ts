import { useState, useCallback, useRef, useEffect } from "react";
import { listScopedDir } from "@/api/workspace";
import type { FileEntry, FileScopeRef } from "@/api/workspace";
import { useWorkspaceContext } from "@/hooks/workspace";
import { useDebounce } from "./useDebounce";

export interface UseScopedFileTreeReturn {
  isWorkspaceTree: boolean;
  expanded: Set<string>;
  treeData: Map<string, FileEntry[]>;
  selectedPath: string | null;
  isLoading: boolean;
  error: string | null;
  filterText: string;
  debouncedFilterText: string;
  toggle: (dirPath: string) => Promise<void>;
  loadDir: (dirPath: string) => Promise<void>;
  revealPath: (path: string) => Promise<void>;
  selectFile: (filePath: string | null) => void;
  setFilterText: (text: string) => void;
}

type DirLoader = (path: string) => Promise<FileEntry[]>;

const HIDDEN_SEGMENTS = new Set([".git"]);

function useScopedFileTreeCore(
  loadEntries: DirLoader,
  enabled: boolean,
  isWorkspaceTree: boolean,
): UseScopedFileTreeReturn {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [treeData, setTreeData] = useState<Map<string, FileEntry[]>>(new Map());
  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [filterText, setFilterText] = useState("");

  const debouncedFilterText = useDebounce(filterText, 200);
  const mountedRef = useRef(true);
  const rootRequestIdRef = useRef(0);
  const expandedRef = useRef<Set<string>>(expanded);
  const treeDataRef = useRef<Map<string, FileEntry[]>>(treeData);

  expandedRef.current = expanded;
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
    [enabled, loadEntries],
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
      if (!wasExpanded && !treeDataRef.current.has(dirPath)) {
        await loadDir(dirPath);
      }
    },
    [loadDir],
  );

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
          if (!entry || !entry.is_dir) break;
          acc = acc ? `${acc}/${seg}` : seg;
          entries = await loadEntries(acc);
          loaded.push([acc, entries]);
          expand.push(acc);
        }
        if (mountedRef.current) setError(null);
      } catch {
        // Land as deep as possible if an intermediate request fails.
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
    [enabled, loadEntries],
  );

  const selectFile = useCallback((filePath: string | null) => {
    setSelectedPath(filePath);
  }, []);

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
  }, [enabled, loadEntries]);

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

export function useScopedFileTree(
  scopeRef: FileScopeRef,
): UseScopedFileTreeReturn {
  const { workspaceId } = useWorkspaceContext();
  const scope = scopeRef.scope;
  const target = scopeRef.target ?? null;
  const repo = scopeRef.repo ?? null;
  const loadEntries = useCallback<DirLoader>(
    (path) =>
      listScopedDir(
        workspaceId,
        target ? { scope, target, repo } : { scope, repo },
        path,
      ).then((r) => r.entries),
    [workspaceId, scope, target, repo],
  );
  const enabled = scope === "workspace" || !!target;
  return useScopedFileTreeCore(loadEntries, enabled, scope === "workspace");
}
