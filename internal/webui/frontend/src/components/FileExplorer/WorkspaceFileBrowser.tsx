import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type MouseEvent,
} from "react";
import { useStore } from "zustand";

import { ResizeHandle } from "@/components/ResizeHandle";
import type { FileCheckout } from "@/api/workspace";
import {
  deleteScopedPath,
  gitStatusScoped,
  indexScopedFiles,
  listScopedDir,
  listFileCheckouts,
  mkdirScoped,
  moveScopedPath,
  repairFileCheckout,
  readScopedFile,
  writeScopedFile,
} from "@/hooks/api";
import {
  useToast,
  useWorkspaceContext,
  useEventContext,
  agentFileBrowserTabsStorageKey,
  FileBrowserStoreProvider,
  fileBrowserTabsStorageKey,
  useFileBrowserStoreInstance,
  type FileBrowserTab,
} from "@/hooks";
import {
  checkoutLabel,
  checkoutRefKey,
  checkoutTitle,
  mapWorkspaceIndexPathToCheckout,
  sameCheckoutRef,
  tabIdentityKey,
  type CheckoutRef,
} from "@/utils/fileExplorerRefs";
import { wsGet, wsSet } from "@/utils/scopedStorage";

import type { FileTreeNodeInfo } from "./FileTree";
import {
  ContextMenu,
  DeleteConfirmDialog,
  MoveToDialog,
  RepairCheckoutConfirmDialog,
} from "./FileExplorerDialogs";
import { FileExplorerEditorGroup } from "./FileExplorerEditorGroup";
import { FileExplorerTreePanel } from "./FileExplorerTreePanel";
import { FileSearchPanel } from "./FileSearchPanel";
import { QuickOpenPalette } from "./QuickOpenPalette";
import {
  hasAvailableCheckoutStatus,
  unavailableCheckoutLabels,
} from "./checkoutAvailability";
import {
  basename,
  clampTreeWidth,
  DEFAULT_GROUP_WIDTH,
  DEFAULT_TREE_WIDTH,
  DELETE_FILE_SKIP_KEY,
  dirname,
  duplicateName,
  getStoredLens,
  getStoredTreeWidth,
  isConflictError,
  joinPath,
  MAX_GROUP_WIDTH,
  MAX_TREE_WIDTH,
  MIN_GROUP_WIDTH,
  MIN_TREE_WIDTH,
  pathMatchesPrefix,
  QUICK_OPEN_STALE_MS,
  resolveMoveToTarget,
  shallowRecordEqual,
  sortedEntries,
  storeLens,
  storeTreeWidth,
} from "./fileExplorerLocalUtils";
import { buildFileTreeSections, existingCheckoutRefs } from "./treeRoots";
import {
  buildChangeGroups,
  checkoutRefFromCheckout,
  type ChangeCheckoutGroup,
} from "./changesLens";
import type { QuickOpenItem } from "./quickOpen";
import styles from "./FileExplorer.module.css";
import type {
  ContextMenuState,
  CheckoutRepairMenuState,
  DeleteConfirmState,
  DiffViewState,
  ExplorerLens,
  FileBrowserProps,
  LineTarget,
  MoveDialogState,
  OpenDiffRequest,
  OpenRevisionRequest,
  RepairConfirmState,
  RevisionViewState,
  ScopedInlineEdit,
  TreeRefreshRequest,
  TreeRevealRequest,
} from "./workspaceFileBrowserTypes";

function checkoutRepairRequest(ref: CheckoutRef, force = false) {
  if (ref.scope === "agent" && ref.target) {
    const request = {
      scope: "agent" as const,
      target: ref.target,
      force,
    };
    if (ref.repo) {
      return { ...request, repo: ref.repo };
    }
    return {
      ...request,
    };
  }
  if (ref.scope === "repo" && ref.target) {
    return { scope: "repo" as const, target: ref.target, force };
  }
  return null;
}

function FileBrowserInner({
  mode = "workspace",
  agentName,
  isActive = true,
}: FileBrowserProps) {
  const { workspaceId, agents, repos } = useWorkspaceContext();
  const eventContext = useEventContext();
  const { showToast } = useToast();
  const store = useFileBrowserStoreInstance();
  const groups = useStore(store, (s) => s.groups);
  const activeGroup = useStore(store, (s) => s.activeGroup);
  const dirty = useStore(store, (s) => s.dirty);
  const mru = useStore(store, (s) => s.mru);

  const [treeWidth, setTreeWidth] = useState<number>(getStoredTreeWidth);
  const [lens, setLens] = useState<ExplorerLens>(() =>
    getStoredLens(workspaceId),
  );
  const [splitLeftWidth, setSplitLeftWidth] = useState(DEFAULT_GROUP_WIDTH);
  const [lineTargets, setLineTargets] = useState<Record<string, LineTarget>>(
    {},
  );
  const [fileReloadTokens, setFileReloadTokens] = useState<
    Record<string, number>
  >({});
  const [historyRefreshKey, setHistoryRefreshKey] = useState(0);
  const [diffViews, setDiffViews] = useState<
    Record<number, DiffViewState | null>
  >({});
  const [revisionViews, setRevisionViews] = useState<
    Record<number, RevisionViewState | null>
  >({});
  const [quickOpenOpen, setQuickOpenOpen] = useState(false);
  const [quickOpenItems, setQuickOpenItems] = useState<QuickOpenItem[]>([]);
  const [quickOpenTruncated, setQuickOpenTruncated] = useState(false);
  const [quickOpenFetchedAt, setQuickOpenFetchedAt] = useState(0);
  const [quickOpenLoading, setQuickOpenLoading] = useState(false);
  const [quickOpenError, setQuickOpenError] = useState<string | null>(null);
  const [gitStatusByRef, setGitStatusByRef] = useState<
    Record<string, Record<string, string>>
  >({});
  const [checkouts, setCheckouts] = useState<FileCheckout[]>([]);
  const [checkoutError, setCheckoutError] = useState<string | null>(null);
  const [repairError, setRepairError] = useState<string | null>(null);
  const [repairingCheckoutKey, setRepairingCheckoutKey] = useState<
    string | null
  >(null);
  const [repairConfirm, setRepairConfirm] = useState<RepairConfirmState | null>(
    null,
  );
  const [searchPanelOpen, setSearchPanelOpen] = useState(false);
  const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(null);
  const [repairMenu, setRepairMenu] = useState<CheckoutRepairMenuState | null>(
    null,
  );
  const [inlineEdit, setInlineEdit] = useState<ScopedInlineEdit | null>(null);
  const [deleteConfirm, setDeleteConfirm] = useState<DeleteConfirmState | null>(
    null,
  );
  const [moveDialog, setMoveDialog] = useState<MoveDialogState | null>(null);
  const [expandedRoots, setExpandedRoots] = useState<Set<string>>(new Set());
  const [isCompactLayout, setIsCompactLayout] = useState(false);
  const [treeRevealRequests, setTreeRevealRequests] = useState<
    Record<string, TreeRevealRequest>
  >({});
  const [treeRefreshRequests, setTreeRefreshRequests] = useState<
    Record<string, TreeRefreshRequest>
  >({});
  const inlineCommitKeyRef = useRef<string | null>(null);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const reconnectAttemptsRef = useRef(eventContext.reconnectAttempts);
  const lastLoadedChangeGroupsRef = useRef<Map<string, ChangeCheckoutGroup>>(
    new Map(),
  );

  const visibleCheckouts = useMemo(
    () =>
      mode === "agent" && agentName
        ? checkouts.filter(
            (checkout) =>
              checkout.kind === "agent" && checkout.agent === agentName,
          )
        : checkouts,
    [mode, agentName, checkouts],
  );
  const sections = useMemo(
    () =>
      buildFileTreeSections({
        mode,
        agentName,
        agents,
        repos,
        checkouts: visibleCheckouts,
      }),
    [mode, agentName, agents, repos, visibleCheckouts],
  );
  const allSections = useMemo(
    () =>
      buildFileTreeSections({
        mode: "workspace",
        agents,
        repos,
        checkouts,
      }),
    [agents, repos, checkouts],
  );
  const computedVisibleRefs = useMemo(
    () => existingCheckoutRefs(sections),
    [sections],
  );
  const visibleRefsKey = computedVisibleRefs.map(checkoutRefKey).join("|");
  const visibleRefsRef = useRef<{ key: string; refs: CheckoutRef[] }>({
    key: "",
    refs: [],
  });
  if (visibleRefsRef.current.key !== visibleRefsKey) {
    visibleRefsRef.current = {
      key: visibleRefsKey,
      refs: computedVisibleRefs,
    };
  }
  const visibleRefs = visibleRefsRef.current.refs;
  const computedStoreValidRefs = useMemo(
    () => existingCheckoutRefs(allSections),
    [allSections],
  );
  const storeValidRefsKey = computedStoreValidRefs
    .map(checkoutRefKey)
    .join("|");
  const storeValidRefsRef = useRef<{ key: string; refs: CheckoutRef[] }>({
    key: "",
    refs: [],
  });
  if (storeValidRefsRef.current.key !== storeValidRefsKey) {
    storeValidRefsRef.current = {
      key: storeValidRefsKey,
      refs: computedStoreValidRefs,
    };
  }
  const storeValidRefs = storeValidRefsRef.current.refs;
  const knownRefs = storeValidRefs;
  const checkoutChangeCount = useMemo(
    () =>
      visibleCheckouts.reduce(
        (sum, checkout) =>
          hasAvailableCheckoutStatus(checkout)
            ? sum + checkout.change_count
            : sum,
        0,
      ),
    [visibleCheckouts],
  );
  const unavailableChangeCheckoutLabels = useMemo(
    () => unavailableCheckoutLabels(visibleCheckouts),
    [visibleCheckouts],
  );
  const changesRefs = useMemo(() => {
    const seen = new Set<string>();
    return visibleCheckouts
      .filter(
        (checkout) =>
          hasAvailableCheckoutStatus(checkout) && checkout.change_count > 0,
      )
      .map(checkoutRefFromCheckout)
      .filter((ref) => {
        const key = checkoutRefKey(ref);
        if (seen.has(key)) return false;
        seen.add(key);
        return true;
      });
  }, [visibleCheckouts]);
  const statusRefs = lens === "changes" ? changesRefs : visibleRefs;
  const statusRefsKey = statusRefs.map(checkoutRefKey).join("|");
  const stableStatusRefsRef = useRef<{
    key: string;
    refs: CheckoutRef[];
  }>({ key: "", refs: [] });
  if (stableStatusRefsRef.current.key !== statusRefsKey) {
    stableStatusRefsRef.current = { key: statusRefsKey, refs: statusRefs };
  }
  const stableStatusRefs = stableStatusRefsRef.current.refs;
  const changeGroups = useMemo(
    () => buildChangeGroups(visibleCheckouts, gitStatusByRef),
    [visibleCheckouts, gitStatusByRef],
  );
  const quickOpenIndexRefs = useMemo<CheckoutRef[]>(() => {
    if (mode !== "agent") return [];
    const agentRefs = visibleRefs.filter((ref) => ref.scope === "agent");
    if (agentRefs.length > 0) return agentRefs;
    return agentName ? [{ scope: "agent", target: agentName }] : [];
  }, [mode, agentName, visibleRefs]);
  const visibleChangeGroups = useMemo(
    () =>
      changeGroups.map((group) => {
        if (group.loaded) return group;
        const previous = lastLoadedChangeGroupsRef.current.get(group.id);
        return previous
          ? {
              ...previous,
              label: group.label,
              changeCount: group.changeCount,
            }
          : group;
      }),
    [changeGroups],
  );
  const activeTab =
    groups[activeGroup]?.tabs.find(
      (tab) => tabIdentityKey(tab) === groups[activeGroup]?.active,
    ) ??
    groups[0]?.tabs.find((tab) => tabIdentityKey(tab) === groups[0]?.active) ??
    null;
  const workspaceRef = useMemo<CheckoutRef>(() => ({ scope: "workspace" }), []);

  useEffect(() => {
    for (const group of visibleChangeGroups) {
      if (group.loaded) lastLoadedChangeGroupsRef.current.set(group.id, group);
    }
  }, [visibleChangeGroups]);

  useEffect(() => {
    store.getState().pruneUnavailableRefs(storeValidRefs);
  }, [store, storeValidRefs]);

  useEffect(() => {
    const node = containerRef.current;
    if (!node) return;
    const update = (width: number) => setIsCompactLayout(width <= 700);
    update(node.getBoundingClientRect().width);

    if (typeof ResizeObserver === "undefined") {
      const onResize = () => update(node.getBoundingClientRect().width);
      window.addEventListener("resize", onResize);
      return () => window.removeEventListener("resize", onResize);
    }

    const observer = new ResizeObserver((entries) => {
      const entry = entries[0];
      if (entry) update(entry.contentRect.width);
    });
    observer.observe(node);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    setLens(getStoredLens(workspaceId));
  }, [workspaceId]);

  const changeLens = useCallback(
    (nextLens: ExplorerLens) => {
      setLens(nextLens);
      storeLens(workspaceId, nextLens);
    },
    [workspaceId],
  );

  const markIndexStale = useCallback(() => {
    setQuickOpenFetchedAt(0);
  }, []);

  const refreshCheckouts = useCallback(async () => {
    try {
      const data = await listFileCheckouts(workspaceId);
      setCheckouts(data.checkouts);
      setCheckoutError(null);
    } catch (err) {
      setCheckoutError(err instanceof Error ? err.message : String(err));
    }
  }, [workspaceId]);

  const refreshGitStatus = useCallback(async () => {
    const next: Record<string, Record<string, string>> = {};
    await Promise.all(
      stableStatusRefs.map(async (ref) => {
        const key = checkoutRefKey(ref);
        try {
          next[key] = await gitStatusScoped(workspaceId, ref);
        } catch {
          next[key] = {};
        }
      }),
    );
    setGitStatusByRef((prev) => {
      let changed = false;
      const merged = { ...prev };
      for (const [key, value] of Object.entries(next)) {
        if (!shallowRecordEqual(prev[key], value)) {
          merged[key] = value;
          changed = true;
        }
      }
      return changed ? merged : prev;
    });
  }, [stableStatusRefs, workspaceId]);

  const fetchQuickOpenIndex = useCallback(
    async (force = false) => {
      const now = Date.now();
      if (
        !force &&
        quickOpenFetchedAt > 0 &&
        now - quickOpenFetchedAt < QUICK_OPEN_STALE_MS
      ) {
        return;
      }
      setQuickOpenLoading(true);
      setQuickOpenError(null);
      try {
        const indexes =
          mode === "agent"
            ? await Promise.all(
                quickOpenIndexRefs.map(async (ref) => ({
                  ref,
                  index: await indexScopedFiles(workspaceId, ref),
                })),
              )
            : [
                {
                  ref: { scope: "workspace" } as CheckoutRef,
                  index: await indexScopedFiles(workspaceId, {
                    scope: "workspace",
                  }),
                },
              ];
        const items = indexes.flatMap(({ ref, index }) =>
          index.paths.map((rawPath) => {
            const mapped =
              mode === "agent"
                ? { ref, path: rawPath }
                : mapWorkspaceIndexPathToCheckout(rawPath, knownRefs);
            return {
              id: tabIdentityKey({ ref: mapped.ref, path: mapped.path }),
              ref: mapped.ref,
              path: mapped.path,
              checkoutLabel: checkoutLabel(mapped.ref),
            };
          }),
        );
        setQuickOpenItems(items);
        setQuickOpenTruncated(
          indexes.some(({ index }) => Boolean(index.truncated)),
        );
        setQuickOpenFetchedAt(Date.now());
      } catch (err) {
        setQuickOpenError(err instanceof Error ? err.message : String(err));
      } finally {
        setQuickOpenLoading(false);
      }
    },
    [knownRefs, mode, quickOpenFetchedAt, quickOpenIndexRefs, workspaceId],
  );

  useEffect(() => {
    if (!contextMenu && !repairMenu) return;
    const close = () => setContextMenu(null);
    const closeRepair = () => setRepairMenu(null);
    window.addEventListener("click", close);
    window.addEventListener("click", closeRepair);
    window.addEventListener("keydown", close);
    window.addEventListener("keydown", closeRepair);
    return () => {
      window.removeEventListener("click", close);
      window.removeEventListener("click", closeRepair);
      window.removeEventListener("keydown", close);
      window.removeEventListener("keydown", closeRepair);
    };
  }, [contextMenu, repairMenu]);

  useEffect(() => {
    if (quickOpenOpen) {
      void fetchQuickOpenIndex();
    }
  }, [fetchQuickOpenIndex, quickOpenOpen]);

  useEffect(() => {
    void refreshCheckouts();
  }, [refreshCheckouts]);

  useEffect(() => {
    void refreshGitStatus();
  }, [refreshGitStatus]);

  useEffect(() => {
    const handleFocus = () => {
      void refreshCheckouts();
      void refreshGitStatus();
    };
    window.addEventListener("focus", handleFocus);
    return () => window.removeEventListener("focus", handleFocus);
  }, [refreshCheckouts, refreshGitStatus]);

  useEffect(() => {
    const previous = reconnectAttemptsRef.current;
    reconnectAttemptsRef.current = eventContext.reconnectAttempts;
    if (
      eventContext.reconnectAttempts > 0 ||
      (previous > 0 && eventContext.state === "connected")
    ) {
      void refreshCheckouts();
      void refreshGitStatus();
    }
  }, [
    eventContext.reconnectAttempts,
    eventContext.state,
    refreshCheckouts,
    refreshGitStatus,
  ]);

  useEffect(() => {
    if (!isActive) return;
    const handleKeyDown = (event: globalThis.KeyboardEvent) => {
      const key = event.key.toLowerCase();
      const mod = event.metaKey || event.ctrlKey;
      if (mod && !event.shiftKey && key === "p") {
        event.preventDefault();
        setQuickOpenOpen(true);
      } else if (mod && event.shiftKey && key === "f") {
        event.preventDefault();
        setSearchPanelOpen(true);
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [isActive]);

  const expandForRef = useCallback((ref: CheckoutRef) => {
    setExpandedRoots((prev) => {
      const next = new Set(prev);
      next.add(checkoutRefKey(ref));
      if (ref.scope === "agent" && ref.target) {
        next.add(`agent:${ref.target}`);
      }
      return next;
    });
  }, []);

  const revealInTree = useCallback(
    (ref: CheckoutRef, path: string) => {
      expandForRef(ref);
      const key = checkoutRefKey(ref);
      setTreeRevealRequests((prev) => ({
        ...prev,
        [key]: { path, token: (prev[key]?.token ?? 0) + 1 },
      }));
    },
    [expandForRef],
  );

  const refreshParents = useCallback((ref: CheckoutRef, ...paths: string[]) => {
    const key = checkoutRefKey(ref);
    setTreeRefreshRequests((prev) => ({
      ...prev,
      [key]: { paths, token: (prev[key]?.token ?? 0) + 1 },
    }));
  }, []);

  const requestFileReload = useCallback((ref: CheckoutRef, path: string) => {
    const key = tabIdentityKey({ ref, path });
    setFileReloadTokens((prev) => ({
      ...prev,
      [key]: (prev[key] ?? 0) + 1,
    }));
  }, []);

  const discardActiveIfNeeded = useCallback(
    (groupIndex: number, nextKey?: string): boolean => {
      const state = store.getState();
      const current = state.groups[groupIndex]?.active;
      if (!current || current === nextKey || !state.dirty[current]) return true;
      const tab = state.groups[groupIndex]?.tabs.find(
        (candidate) => tabIdentityKey(candidate) === current,
      );
      const ok = window.confirm(`Discard unsaved changes in ${current}?`);
      if (!ok) return false;
      state.setDirty(current, false);
      if (tab) state.setDirty(tabIdentityKey(tab), false);
      return true;
    },
    [store],
  );

  const openFile = useCallback(
    (
      ref: CheckoutRef,
      path: string,
      groupIndex = store.getState().activeGroup,
      lineNumber?: number,
    ) => {
      const tab = { ref, path };
      const key = tabIdentityKey(tab);
      if (!discardActiveIfNeeded(groupIndex, key)) return;
      setDiffViews((prev) => ({ ...prev, [groupIndex]: null }));
      setRevisionViews((prev) => ({ ...prev, [groupIndex]: null }));
      store.getState().openTab(tab, groupIndex);
      revealInTree(ref, path);
      if (lineNumber && lineNumber > 0) {
        setLineTargets((prev) => ({
          ...prev,
          [key]: { line: lineNumber, token: (prev[key]?.token ?? 0) + 1 },
        }));
      }
    },
    [discardActiveIfNeeded, revealInTree, store],
  );

  const handleLineTargetApplied = useCallback((key: string, token: number) => {
    setLineTargets((prev) => {
      if (prev[key]?.token !== token) return prev;
      const next = { ...prev };
      delete next[key];
      return next;
    });
  }, []);

  const selectTab = useCallback(
    (groupIndex: number, key: string) => {
      if (!discardActiveIfNeeded(groupIndex, key)) return;
      setDiffViews((prev) => ({ ...prev, [groupIndex]: null }));
      setRevisionViews((prev) => ({ ...prev, [groupIndex]: null }));
      store.getState().activateTab(groupIndex, key);
      const tab = store
        .getState()
        .groups[
          groupIndex
        ]?.tabs.find((candidate) => tabIdentityKey(candidate) === key);
      if (tab) revealInTree(tab.ref, tab.path);
    },
    [discardActiveIfNeeded, revealInTree, store],
  );

  const closeTab = useCallback(
    (groupIndex: number, key: string) => {
      if (store.getState().dirty[key]) {
        const tab = store
          .getState()
          .groups[
            groupIndex
          ]?.tabs.find((candidate) => tabIdentityKey(candidate) === key);
        const label = tab ? checkoutTitle(tab.ref, tab.path) : key;
        const ok = window.confirm(`Discard unsaved changes in ${label}?`);
        if (!ok) return;
        store.getState().setDirty(key, false);
      }
      setDiffViews((prev) => ({ ...prev, [groupIndex]: null }));
      setRevisionViews((prev) => ({ ...prev, [groupIndex]: null }));
      store.getState().closeTab(groupIndex, key);
    },
    [store],
  );

  const splitRight = useCallback(
    (groupIndex: number) => {
      const state = store.getState();
      const key = state.groups[groupIndex]?.active;
      const tab = state.groups[groupIndex]?.tabs.find(
        (candidate) => tabIdentityKey(candidate) === key,
      );
      if (!key || !tab) return;
      if (state.dirty[key]) {
        const ok = window.confirm(
          `Discard unsaved changes in ${checkoutTitle(tab.ref, tab.path)} before splitting?`,
        );
        if (!ok) return;
        state.setDirty(key, false);
      }
      state.splitRight(tab);
    },
    [store],
  );

  const handleSaved = useCallback(
    (tab: FileBrowserTab) => {
      markIndexStale();
      void refreshCheckouts();
      void refreshGitStatus();
      setHistoryRefreshKey((key) => key + 1);
      showToast("File saved", { type: "success" });
      refreshParents(tab.ref, tab.path);
    },
    [
      markIndexStale,
      refreshCheckouts,
      refreshGitStatus,
      refreshParents,
      showToast,
    ],
  );

  const openDiff = useCallback(
    (groupIndex: number, request: OpenDiffRequest) => {
      const title =
        request.title ??
        (request.to
          ? `${request.from ?? "HEAD"}..${request.to}`
          : `${request.from ?? "HEAD"} vs working tree`);
      setDiffViews((prev) => ({
        ...prev,
        [groupIndex]: { ...request, title },
      }));
      setRevisionViews((prev) => ({ ...prev, [groupIndex]: null }));
    },
    [],
  );

  const closeDiff = useCallback((groupIndex: number) => {
    setDiffViews((prev) => ({ ...prev, [groupIndex]: null }));
  }, []);

  const openRevision = useCallback(
    (groupIndex: number, request: OpenRevisionRequest) => {
      setRevisionViews((prev) => ({
        ...prev,
        [groupIndex]: { ...request },
      }));
      setDiffViews((prev) => ({ ...prev, [groupIndex]: null }));
    },
    [],
  );

  const closeRevision = useCallback((groupIndex: number) => {
    setRevisionViews((prev) => ({ ...prev, [groupIndex]: null }));
  }, []);

  const restoreSnapshot = useCallback(
    async (ref: CheckoutRef, path: string, content: string) => {
      const key = tabIdentityKey({ ref, path });
      const state = store.getState();
      const isOpen = state.groups.some((group) =>
        group.tabs.some((tab) => tabIdentityKey(tab) === key),
      );
      const unsavedWarning =
        isOpen && state.dirty[key]
          ? "\n\nUnsaved edits in the open tab will be replaced."
          : "";
      const ok = window.confirm(
        `Restore ${checkoutTitle(ref, path)}?${unsavedWarning}`,
      );
      if (!ok) return;
      await writeScopedFile(workspaceId, ref, path, content);
      store.getState().setDirty(key, false);
      if (isOpen) requestFileReload(ref, path);
      setDiffViews({});
      setRevisionViews({});
      markIndexStale();
      void refreshCheckouts();
      void refreshGitStatus();
      setHistoryRefreshKey((key) => key + 1);
      refreshParents(ref, path);
      showToast(`Restored ${basename(path)}`, { type: "success" });
      openFile(ref, path);
    },
    [
      workspaceId,
      store,
      requestFileReload,
      markIndexStale,
      refreshCheckouts,
      refreshGitStatus,
      refreshParents,
      showToast,
      openFile,
    ],
  );

  const handleResizeDelta = useCallback((deltaPx: number) => {
    setTreeWidth((w) => {
      const next = clampTreeWidth(w + deltaPx);
      storeTreeWidth(next);
      return next;
    });
  }, []);

  const handleResizeReset = useCallback(() => {
    setTreeWidth(DEFAULT_TREE_WIDTH);
    storeTreeWidth(DEFAULT_TREE_WIDTH);
  }, []);

  const handleSplitResizeDelta = useCallback((deltaPx: number) => {
    setSplitLeftWidth((w) =>
      Math.min(MAX_GROUP_WIDTH, Math.max(MIN_GROUP_WIDTH, w + deltaPx)),
    );
  }, []);

  const navigateToDir = useCallback(
    (ref: CheckoutRef, dirPath: string) => {
      revealInTree(ref, dirPath);
    },
    [revealInTree],
  );

  const beginCreate = useCallback(
    (
      ref: CheckoutRef,
      kind: "create-file" | "create-folder",
      node: FileTreeNodeInfo,
    ) => {
      setContextMenu(null);
      const parentPath = node.isDir ? node.path : dirname(node.path);
      setInlineEdit({
        ref,
        edit: {
          kind,
          parentPath,
          value: kind === "create-file" ? "untitled.txt" : "new-folder",
          isDir: kind === "create-folder",
        },
      });
    },
    [],
  );

  const beginRename = useCallback(
    (ref: CheckoutRef, node: FileTreeNodeInfo) => {
      setContextMenu(null);
      setInlineEdit({
        ref,
        edit: {
          kind: "rename",
          parentPath: dirname(node.path),
          path: node.path,
          value: node.name,
          isDir: node.isDir,
        },
      });
    },
    [],
  );

  const commitInlineEdit = useCallback(async () => {
    if (!inlineEdit) return;
    const edit = inlineEdit.edit;
    const ref = inlineEdit.ref;
    const value = edit.value.trim();
    if (!value) {
      setInlineEdit(null);
      return;
    }
    const commitKey = `${checkoutRefKey(ref)}:${edit.kind}:${edit.path ?? edit.parentPath}:${value}`;
    if (inlineCommitKeyRef.current === commitKey) return;
    inlineCommitKeyRef.current = commitKey;
    setInlineEdit(null);
    try {
      if (edit.kind === "create-file") {
        const path = joinPath(edit.parentPath, value);
        await writeScopedFile(workspaceId, ref, path, "");
        markIndexStale();
        void refreshCheckouts();
        void refreshGitStatus();
        refreshParents(ref, path);
        openFile(ref, path);
        revealInTree(ref, path);
      } else if (edit.kind === "create-folder") {
        const path = joinPath(edit.parentPath, value);
        await mkdirScoped(workspaceId, ref, path);
        markIndexStale();
        void refreshCheckouts();
        void refreshGitStatus();
        refreshParents(ref, path);
        revealInTree(ref, path);
      } else if (edit.path) {
        const nextPath = joinPath(edit.parentPath, value);
        if (nextPath !== edit.path) {
          await moveScopedPath(workspaceId, ref, edit.path, nextPath);
          store.getState().retargetPathPrefix(ref, edit.path, nextPath);
          markIndexStale();
          void refreshCheckouts();
          void refreshGitStatus();
          refreshParents(ref, edit.path, nextPath);
          revealInTree(ref, nextPath);
        }
      }
    } catch (err) {
      showToast(err instanceof Error ? err.message : String(err), {
        type: "error",
      });
    } finally {
      inlineCommitKeyRef.current = null;
    }
  }, [
    inlineEdit,
    workspaceId,
    refreshParents,
    openFile,
    revealInTree,
    store,
    showToast,
    markIndexStale,
    refreshCheckouts,
    refreshGitStatus,
  ]);

  const dirtyTabsForPath = useCallback(
    (ref: CheckoutRef, path: string): string[] => {
      const state = store.getState();
      const dirtyKeys = new Set(Object.keys(state.dirty));
      return state.groups
        .flatMap((group) => group.tabs)
        .filter(
          (tab) =>
            dirtyKeys.has(tabIdentityKey(tab)) &&
            sameCheckoutRef(tab.ref, ref) &&
            pathMatchesPrefix(tab.path, path),
        )
        .map(tabIdentityKey);
    },
    [store],
  );

  const performDelete = useCallback(
    async (
      ref: CheckoutRef,
      node: FileTreeNodeInfo,
      skipFutureFileConfirms = false,
    ) => {
      const dirtyTabs = dirtyTabsForPath(ref, node.path);
      if (dirtyTabs.length > 0) {
        const ok = window.confirm(
          `Discard unsaved changes in ${dirtyTabs.length} open file${dirtyTabs.length === 1 ? "" : "s"}?`,
        );
        if (!ok) return;
        for (const path of dirtyTabs) {
          store.getState().setDirty(path, false);
        }
      }
      try {
        await deleteScopedPath(workspaceId, ref, node.path, node.isDir);
        markIndexStale();
        void refreshCheckouts();
        void refreshGitStatus();
        if (!node.isDir && skipFutureFileConfirms) {
          wsSet(workspaceId, DELETE_FILE_SKIP_KEY, "1");
        }
        store.getState().closePathPrefix(ref, node.path);
        refreshParents(ref, node.path);
        showToast("Deleted", { type: "success" });
      } catch (err) {
        showToast(err instanceof Error ? err.message : String(err), {
          type: "error",
        });
      } finally {
        setDeleteConfirm(null);
      }
    },
    [
      dirtyTabsForPath,
      workspaceId,
      store,
      refreshParents,
      showToast,
      markIndexStale,
      refreshCheckouts,
      refreshGitStatus,
    ],
  );

  const requestDelete = useCallback(
    (ref: CheckoutRef, node: FileTreeNodeInfo) => {
      setContextMenu(null);
      const skipFileConfirm =
        !node.isDir && wsGet(workspaceId, DELETE_FILE_SKIP_KEY) === "1";
      if (skipFileConfirm) {
        void performDelete(ref, node);
      } else {
        setDeleteConfirm({ ref, node });
      }
    },
    [performDelete, workspaceId],
  );

  const duplicateFile = useCallback(
    async (ref: CheckoutRef, node: FileTreeNodeInfo) => {
      setContextMenu(null);
      if (node.isDir) return;
      try {
        const data = await readScopedFile(workspaceId, ref, node.path);
        if (data.binary || data.truncated) {
          showToast("Only complete text files can be duplicated", {
            type: "error",
          });
          return;
        }
        const parent = dirname(node.path);
        const entries = sortedEntries(
          (await listScopedDir(workspaceId, ref, parent)).entries,
        );
        const nextName = duplicateName(basename(node.path), entries);
        const nextPath = joinPath(parent, nextName);
        await writeScopedFile(workspaceId, ref, nextPath, data.content ?? "");
        markIndexStale();
        void refreshCheckouts();
        void refreshGitStatus();
        refreshParents(ref, nextPath);
        openFile(ref, nextPath);
      } catch (err) {
        showToast(err instanceof Error ? err.message : String(err), {
          type: "error",
        });
      }
    },
    [
      workspaceId,
      refreshParents,
      openFile,
      showToast,
      markIndexStale,
      refreshCheckouts,
      refreshGitStatus,
    ],
  );

  const copyPath = useCallback(
    (node: FileTreeNodeInfo) => {
      setContextMenu(null);
      void navigator.clipboard?.writeText(node.path).catch(() => {
        showToast("Failed to copy path", { type: "error" });
      });
    },
    [showToast],
  );

  const handleContextMenu = useCallback(
    (
      ref: CheckoutRef,
      node: FileTreeNodeInfo,
      event: MouseEvent<HTMLDivElement>,
    ) => {
      setContextMenu({ ref, node, x: event.clientX, y: event.clientY });
    },
    [],
  );

  const performCheckoutRepair = useCallback(
    async (ref: CheckoutRef, label: string, force = false) => {
      const request = checkoutRepairRequest(ref, force);
      if (!request) {
        setRepairError("Only agent and repo checkouts can be repaired.");
        return;
      }
      const key = checkoutRefKey(ref);
      if (repairingCheckoutKey) return;
      setRepairingCheckoutKey(key);
      setRepairError(null);
      try {
        const result = await repairFileCheckout(workspaceId, request);
        if (result.requires_force) {
          setRepairConfirm({ ref, label });
          return;
        }
        if (!result.repaired) {
          setRepairError(result.message || `Could not repair ${label}.`);
          return;
        }
        markIndexStale();
        await refreshCheckouts();
        void refreshGitStatus();
        refreshParents(ref, "");
        showToast(result.message || `Repaired ${label}.`, {
          type: "success",
        });
      } catch {
        const message = `Repair failed for ${label}.`;
        setRepairError(message);
        showToast(message, { type: "error" });
      } finally {
        setRepairingCheckoutKey(null);
      }
    },
    [
      workspaceId,
      repairingCheckoutKey,
      markIndexStale,
      refreshCheckouts,
      refreshGitStatus,
      refreshParents,
      showToast,
    ],
  );

  const handleCheckoutContextMenu = useCallback(
    (ref: CheckoutRef, label: string, event: MouseEvent<HTMLDivElement>) => {
      setContextMenu(null);
      setRepairMenu({ ref, label, x: event.clientX, y: event.clientY });
    },
    [],
  );

  const performMove = useCallback(
    async (
      ref: CheckoutRef,
      node: FileTreeNodeInfo,
      targetFolderPath: string,
    ) => {
      const move = resolveMoveToTarget(node.path, targetFolderPath);
      if (!move) return;
      const applyMove = async (overwrite: boolean) => {
        await moveScopedPath(workspaceId, ref, move.from, move.to, overwrite);
      };
      try {
        await applyMove(false);
      } catch (err) {
        if (!isConflictError(err)) {
          showToast(err instanceof Error ? err.message : String(err), {
            type: "error",
          });
          return;
        }
        const ok = window.confirm(`Overwrite ${move.to}?`);
        if (!ok) return;
        try {
          await applyMove(true);
        } catch (overwriteErr) {
          showToast(
            overwriteErr instanceof Error
              ? overwriteErr.message
              : String(overwriteErr),
            { type: "error" },
          );
          return;
        }
      }

      store.getState().retargetPathPrefix(ref, move.from, move.to);
      markIndexStale();
      void refreshCheckouts();
      void refreshGitStatus();
      refreshParents(ref, move.from, move.to);
      revealInTree(ref, move.to);
      showToast("Moved", { type: "success" });
      setMoveDialog(null);
    },
    [
      workspaceId,
      store,
      markIndexStale,
      refreshCheckouts,
      refreshGitStatus,
      refreshParents,
      revealInTree,
      showToast,
    ],
  );

  const selectedTab = activeTab;
  const mruKeys = useMemo(() => mru.map(tabIdentityKey), [mru]);

  const toggleRoot = useCallback((key: string) => {
    setExpandedRoots((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }, []);

  return (
    <div
      ref={containerRef}
      className={styles.container}
      data-compact={isCompactLayout || undefined}
    >
      <div
        className={styles.treePanel}
        style={{ ["--tree-width"]: `${treeWidth}px` } as CSSProperties}
      >
        {searchPanelOpen ? (
          <FileSearchPanel
            workspaceId={workspaceId}
            scopeRef={workspaceRef}
            onOpenResult={(path, line) =>
              openFile(workspaceRef, path, undefined, line)
            }
            onFilesChanged={(paths) => {
              markIndexStale();
              void refreshCheckouts();
              void refreshGitStatus();
              refreshParents(workspaceRef, ...paths);
              showToast("Replace applied", { type: "success" });
            }}
            onClose={() => setSearchPanelOpen(false)}
          />
        ) : (
          <FileExplorerTreePanel
            lens={lens}
            changeCount={checkoutChangeCount}
            checkoutError={checkoutError}
            repairError={repairError}
            sections={sections}
            changeGroups={visibleChangeGroups}
            unavailableCheckoutLabels={unavailableChangeCheckoutLabels}
            expandedRoots={expandedRoots}
            repairingCheckoutKey={repairingCheckoutKey}
            selectedTab={selectedTab}
            inlineEdit={inlineEdit}
            gitStatusByRef={gitStatusByRef}
            treeRevealRequests={treeRevealRequests}
            treeRefreshRequests={treeRefreshRequests}
            hideAgentSectionHeading={mode === "agent"}
            onLensChange={changeLens}
            onQuickOpen={() => setQuickOpenOpen(true)}
            onOpenDiff={(request) =>
              openDiff(store.getState().activeGroup, request)
            }
            onToggleRoot={toggleRoot}
            onRepairCheckout={(ref, label) =>
              void performCheckoutRepair(ref, label)
            }
            onCheckoutContextMenu={handleCheckoutContextMenu}
            onOpenFile={openFile}
            onContextMenu={handleContextMenu}
            onRequestRename={beginRename}
            onRequestDelete={requestDelete}
            onInlineEditChange={(value) =>
              setInlineEdit((prev) =>
                prev ? { ...prev, edit: { ...prev.edit, value } } : prev,
              )
            }
            onInlineEditCommit={() => void commitInlineEdit()}
            onInlineEditCancel={() => setInlineEdit(null)}
          />
        )}
      </div>
      <ResizeHandle
        width={treeWidth}
        minWidth={MIN_TREE_WIDTH}
        maxWidth={MAX_TREE_WIDTH}
        edge="right"
        onDelta={handleResizeDelta}
        onReset={handleResizeReset}
        ariaLabel="Resize file tree"
        testId="file-tree-resize-handle"
        className={styles.resizeHandle}
      />
      <div className={styles.mainColumn}>
        <div
          className={styles.editorGroups}
          data-split={groups.length > 1 || undefined}
        >
          <div
            className={styles.editorGroupSlot}
            style={
              groups.length > 1
                ? ({ "--group-width": `${splitLeftWidth}px` } as CSSProperties)
                : undefined
            }
          >
            <FileExplorerEditorGroup
              groupIndex={0}
              group={groups[0] ?? { tabs: [], active: null }}
              diffView={diffViews[0] ?? null}
              revisionView={revisionViews[0] ?? null}
              isActiveGroup={isActive && activeGroup === 0}
              dirty={dirty}
              onSelectTab={selectTab}
              onCloseTab={closeTab}
              onSplitRight={splitRight}
              onNavigate={navigateToDir}
              onSaved={handleSaved}
              onOpenDiff={openDiff}
              onCloseDiff={closeDiff}
              onOpenRevision={openRevision}
              onCloseRevision={closeRevision}
              onOpenEditableFile={(groupIndex, ref, path) =>
                openFile(ref, path, groupIndex)
              }
              onRestoreSnapshot={restoreSnapshot}
              historyRefreshKey={historyRefreshKey}
              reloadToken={
                groups[0]?.active
                  ? fileReloadTokens[groups[0].active]
                  : undefined
              }
              onLineTargetApplied={handleLineTargetApplied}
              lineTarget={
                groups[0]?.active ? lineTargets[groups[0].active] : undefined
              }
            />
          </div>
          {groups[1] && (
            <>
              <ResizeHandle
                width={splitLeftWidth}
                minWidth={MIN_GROUP_WIDTH}
                maxWidth={MAX_GROUP_WIDTH}
                edge="right"
                onDelta={handleSplitResizeDelta}
                onReset={() => setSplitLeftWidth(DEFAULT_GROUP_WIDTH)}
                ariaLabel="Resize editor groups"
                testId="file-editor-group-resize-handle"
                className={styles.resizeHandle}
              />
              <div className={styles.editorGroupSlot}>
                <FileExplorerEditorGroup
                  groupIndex={1}
                  group={groups[1]}
                  diffView={diffViews[1] ?? null}
                  revisionView={revisionViews[1] ?? null}
                  isActiveGroup={isActive && activeGroup === 1}
                  dirty={dirty}
                  onSelectTab={selectTab}
                  onCloseTab={closeTab}
                  onSplitRight={splitRight}
                  onNavigate={navigateToDir}
                  onSaved={handleSaved}
                  onOpenDiff={openDiff}
                  onCloseDiff={closeDiff}
                  onOpenRevision={openRevision}
                  onCloseRevision={closeRevision}
                  onOpenEditableFile={(groupIndex, ref, path) =>
                    openFile(ref, path, groupIndex)
                  }
                  onRestoreSnapshot={restoreSnapshot}
                  historyRefreshKey={historyRefreshKey}
                  reloadToken={
                    groups[1]?.active
                      ? fileReloadTokens[groups[1].active]
                      : undefined
                  }
                  onLineTargetApplied={handleLineTargetApplied}
                  lineTarget={
                    groups[1]?.active
                      ? lineTargets[groups[1].active]
                      : undefined
                  }
                />
              </div>
            </>
          )}
        </div>
      </div>
      {contextMenu && (
        <ContextMenu
          state={contextMenu}
          onNewFile={(node) =>
            beginCreate(contextMenu.ref, "create-file", node)
          }
          onNewFolder={(node) =>
            beginCreate(contextMenu.ref, "create-folder", node)
          }
          onRename={(node) => beginRename(contextMenu.ref, node)}
          onDelete={(node) => requestDelete(contextMenu.ref, node)}
          onMove={(node) => {
            setContextMenu(null);
            setMoveDialog({ ref: contextMenu.ref, node });
          }}
          onDuplicate={(node) => duplicateFile(contextMenu.ref, node)}
          onCopyPath={copyPath}
        />
      )}
      {repairMenu && (
        <div
          className={styles.contextMenu}
          style={{ left: repairMenu.x, top: repairMenu.y }}
          role="menu"
        >
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              const { ref, label } = repairMenu;
              setRepairMenu(null);
              void performCheckoutRepair(ref, label);
            }}
          >
            Repair checkout
          </button>
        </div>
      )}
      {repairConfirm && (
        <RepairCheckoutConfirmDialog
          label={repairConfirm.label}
          onCancel={() => setRepairConfirm(null)}
          onConfirm={() => {
            const { ref, label } = repairConfirm;
            setRepairConfirm(null);
            void performCheckoutRepair(ref, label, true);
          }}
        />
      )}
      {deleteConfirm && (
        <DeleteConfirmDialog
          node={deleteConfirm.node}
          onCancel={() => setDeleteConfirm(null)}
          onConfirm={(skip) =>
            void performDelete(deleteConfirm.ref, deleteConfirm.node, skip)
          }
        />
      )}
      {moveDialog && (
        <MoveToDialog
          state={moveDialog}
          onCancel={() => setMoveDialog(null)}
          onConfirm={(target) =>
            void performMove(moveDialog.ref, moveDialog.node, target)
          }
        />
      )}
      <QuickOpenPalette
        isOpen={quickOpenOpen}
        items={quickOpenItems}
        mruKeys={mruKeys}
        isLoading={quickOpenLoading}
        error={quickOpenError}
        truncated={quickOpenTruncated}
        onClose={() => setQuickOpenOpen(false)}
        onOpen={(item) => openFile(item.ref, item.path)}
      />
    </div>
  );
}

export function FileBrowser({
  mode = "workspace",
  agentName,
  isActive = true,
}: FileBrowserProps) {
  const { workspaceId } = useWorkspaceContext();
  const storageKey =
    mode === "agent" && agentName
      ? agentFileBrowserTabsStorageKey(agentName)
      : fileBrowserTabsStorageKey();
  return (
    <FileBrowserStoreProvider
      key={`${workspaceId}:${storageKey}`}
      workspaceId={workspaceId}
      storageKey={storageKey}
    >
      <FileBrowserInner mode={mode} agentName={agentName} isActive={isActive} />
    </FileBrowserStoreProvider>
  );
}

/**
 * WorkspaceFileBrowser remains the exported compatibility wrapper for the
 * Files page lazy import.
 */
export function WorkspaceFileBrowser({
  mode = "workspace",
  agentName,
  isActive = true,
}: FileBrowserProps) {
  return <FileBrowser mode={mode} agentName={agentName} isActive={isActive} />;
}
