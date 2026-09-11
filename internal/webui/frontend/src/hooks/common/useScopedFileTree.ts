import {
  useState,
  useCallback,
  useRef,
  useEffect,
  useMemo,
  useContext,
} from "react";
import { listScopedDir } from "@/api/workspace";
import type { FileEntry, FileScopeRef } from "@/api/workspace";
import { useWorkspaceContext } from "@/hooks/workspace";
import { useDebounce } from "./useDebounce";
import { QueryRecoveryContext } from "./queryRecovery";
import { ScopedQueryRequest } from "@/utils/scopedQueryRequest";

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

  const recovery = useContext(QueryRecoveryContext);
  const expansionRevisionRef = useRef(0);
  const strictPendingRef = useRef<Promise<void> | null>(null);
  const updateExpanded = useCallback((next: Set<string>) => {
    const previous = expandedRef.current;
    const changed =
      [...next].some((path) => path !== "" && !previous.has(path)) ||
      [...previous].some((path) => path !== "" && !next.has(path));
    expandedRef.current = next;
    if (changed) expansionRevisionRef.current++;
    setExpanded(next);
  }, []);
  const commitTree = useCallback((next: Map<string, FileEntry[]>) => {
    treeDataRef.current = next;
    setTreeData(next);
  }, []);
  const strictRead = useMemo(
    () =>
      new ScopedQueryRequest({
        load: async (signal) => {
          const revision = expansionRevisionRef.current;
          const paths = new Set([""]);
          for (const expandedPath of expandedRef.current) {
            const segments = expandedPath.split("/").filter(Boolean);
            for (let length = 1; length <= segments.length; length++)
              paths.add(segments.slice(0, length).join("/"));
          }
          const ordered = [...paths].sort(
            (left, right) => left.split("/").length - right.split("/").length,
          );
          const loaded = new Map<string, FileEntry[]>();
          for (const path of ordered) {
            signal.throwIfAborted();
            if (path) {
              const separator = path.lastIndexOf("/");
              const parent = separator < 0 ? "" : path.slice(0, separator);
              const name = path.slice(separator + 1);
              // A complete parent listing can prove a stale expanded subtree
              // no longer exists. An endpoint error cannot provide that proof.
              if (
                !loaded
                  .get(parent)
                  ?.some((entry) => entry.name === name && entry.is_dir)
              )
                continue;
            }
            loaded.set(path, await loadEntries(path, { signal }));
          }
          return { loaded, revision };
        },
        commit: ({ loaded, revision }) => {
          // The coordinator rechecks this revision before acknowledging. Do not
          // publish a partial tree while it schedules the new membership read.
          if (revision !== expansionRevisionRef.current) return;
          const initial = treeDataRef.current.size === 0;
          commitTree(loaded);
          const retained = new Set(
            [...expandedRef.current].filter((path) => loaded.has(path)),
          );
          if (initial) retained.add("");
          updateExpanded(retained);
          setError(null);
          setIsLoading(false);
        },
        onLoading: (loading) => {
          if (!loading) setIsLoading(false);
        },
        onError: (error) => {
          setError(error.message);
          setIsLoading(false);
        },
      }),
    [loadEntries, commitTree, updateExpanded],
  );
  useEffect(() => () => strictRead.cancel(), [strictRead]);

  useEffect(() => {
    if (!enabled || !recovery) return;
    return recovery.register(
      "expanded file tree",
      (signal) => {
        loaderGenerationRef.current++;
        for (const controller of controllersRef.current) controller.abort();
        controllersRef.current.clear();
        const pending = strictRead.run({ signal, fresh: true });
        strictPendingRef.current = pending;
        const finish = () => {
          if (strictPendingRef.current === pending)
            strictPendingRef.current = null;
        };
        void pending.then(finish, finish);
        return pending;
      },
      () => expansionRevisionRef.current,
    );
  }, [enabled, recovery, strictRead]);

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
      if (strictPendingRef.current) {
        await strictPendingRef.current.catch(() => {});
        return;
      }
      const generation = loaderGenerationRef.current;
      try {
        const entries = await loadWithSignal(dirPath);
        if (mountedRef.current && generation === loaderGenerationRef.current) {
          const next = new Map(treeDataRef.current);
          next.set(dirPath, entries);
          commitTree(next);
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
    [enabled, loadWithSignal, commitTree],
  );

  const toggle = useCallback(
    async (dirPath: string): Promise<void> => {
      const wasExpanded = expandedRef.current.has(dirPath);
      const next = new Set(expandedRef.current);
      if (wasExpanded) next.delete(dirPath);
      else next.add(dirPath);
      updateExpanded(next);
      if (!wasExpanded && !treeDataRef.current.has(dirPath)) {
        await loadDir(dirPath);
      }
    },
    [loadDir, updateExpanded],
  );

  const revealPath = useCallback(
    async (rawPath: string): Promise<void> => {
      if (!enabled) return;
      const generation = loaderGenerationRef.current;
      if (strictPendingRef.current)
        await strictPendingRef.current.catch(() => {});
      if (generation !== loaderGenerationRef.current || !mountedRef.current)
        return;
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
        const next = new Map(treeDataRef.current);
        for (const [p, entries] of loaded) next.set(p, entries);
        commitTree(next);
        updateExpanded(new Set([...expandedRef.current, ...expand]));
      }
    },
    [enabled, loadWithSignal, commitTree, updateExpanded],
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
    updateExpanded(new Set());
    commitTree(new Map());
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
          commitTree(new Map([["", entries]]));
          updateExpanded(new Set([""]));
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
  }, [enabled, loadEntries, loadWithSignal, commitTree, updateExpanded]);

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
