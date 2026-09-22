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

export type DirLoader = (
  path: string,
  options?: { signal?: AbortSignal },
) => Promise<FileEntry[]>;

const HIDDEN_SEGMENTS = new Set([".git"]);

export function useScopedFileTreeCore(
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
  const loaderGenerationRef = useRef(0);
  const controllersRef = useRef<Set<AbortController>>(new Set());
  const expandedRef = useRef<Set<string>>(expanded);
  const treeDataRef = useRef<Map<string, FileEntry[]>>(treeData);

  expandedRef.current = expanded;
  treeDataRef.current = treeData;

  useEffect(() => {
    mountedRef.current = true;
    const controllers = controllersRef.current;
    return () => {
      mountedRef.current = false;
      for (const controller of controllers) controller.abort();
      controllers.clear();
    };
  }, []);

  const loadWithSignal = useCallback(
    async (path: string): Promise<FileEntry[]> => {
      const controller = new AbortController();
      controllersRef.current.add(controller);
      try {
        return await loadEntries(path, { signal: controller.signal });
      } finally {
        controllersRef.current.delete(controller);
      }
    },
    [loadEntries],
  );

  const loadDir = useCallback(
    async (dirPath: string): Promise<void> => {
      if (!enabled) return;
      const generation = loaderGenerationRef.current;
      try {
        const entries = await loadWithSignal(dirPath);
        if (mountedRef.current && generation === loaderGenerationRef.current) {
          setTreeData((prev) => {
            const next = new Map(prev);
            next.set(dirPath, entries);
            return next;
          });
          setError(null);
        }
      } catch (err) {
        if (
          mountedRef.current &&
          generation === loaderGenerationRef.current &&
          !(err instanceof DOMException && err.name === "AbortError")
        ) {
          setError(err instanceof Error ? err.message : String(err));
        }
      }
    },
    [enabled, loadWithSignal],
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
      const generation = loaderGenerationRef.current;
      const clean = rawPath.replace(/^\/+|\/+$/g, "").trim();
      const segments = clean ? clean.split("/").filter(Boolean) : [];
      const loaded: Array<[string, FileEntry[]]> = [];
      const expand: string[] = [""];
      try {
        let entries = await loadWithSignal("");
        loaded.push(["", entries]);
        let acc = "";
        for (const seg of segments) {
          if (HIDDEN_SEGMENTS.has(seg)) break;
          const entry = entries.find((e) => e.name === seg);
          if (!entry || !entry.is_dir) break;
          acc = acc ? `${acc}/${seg}` : seg;
          entries = await loadWithSignal(acc);
          loaded.push([acc, entries]);
          expand.push(acc);
        }
        if (mountedRef.current && generation === loaderGenerationRef.current) {
          setError(null);
        }
      } catch {
        // Land as deep as possible if an intermediate request fails.
      }
      if (
        mountedRef.current &&
        generation === loaderGenerationRef.current &&
        loaded.length > 0
      ) {
        setTreeData((prev) => {
          const next = new Map(prev);
          for (const [p, entries] of loaded) next.set(p, entries);
          return next;
        });
        setExpanded((prev) => new Set([...prev, ...expand]));
      }
    },
    [enabled, loadWithSignal],
  );

  const selectFile = useCallback((filePath: string | null) => {
    setSelectedPath(filePath);
  }, []);

  useEffect(() => {
    loaderGenerationRef.current += 1;
    for (const controller of controllersRef.current) controller.abort();
    controllersRef.current.clear();
    const generation = loaderGenerationRef.current;
    const requestId = ++rootRequestIdRef.current;
    setExpanded(new Set());
    setTreeData(new Map());
    setSelectedPath(null);
    setError(null);
    if (!enabled) {
      setIsLoading(false);
      return;
    }
    setIsLoading(true);

    loadWithSignal("")
      .then((entries) => {
        if (
          requestId === rootRequestIdRef.current &&
          generation === loaderGenerationRef.current &&
          mountedRef.current
        ) {
          setTreeData(new Map([["", entries]]));
          setExpanded(new Set([""]));
          setIsLoading(false);
        }
      })
      .catch((err) => {
        if (
          requestId === rootRequestIdRef.current &&
          generation === loaderGenerationRef.current &&
          mountedRef.current &&
          !(err instanceof DOMException && err.name === "AbortError")
        ) {
          setError(err instanceof Error ? err.message : String(err));
          setIsLoading(false);
        }
      });
  }, [enabled, loadEntries, loadWithSignal]);

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
    (path, options) =>
      listScopedDir(
        workspaceId,
        target ? { scope, target, repo } : { scope, repo },
        path,
        options,
      ).then((r) => r.entries),
    [workspaceId, scope, target, repo],
  );
  const enabled = scope === "workspace" || !!target;
  return useScopedFileTreeCore(loadEntries, enabled, scope === "workspace");
}
