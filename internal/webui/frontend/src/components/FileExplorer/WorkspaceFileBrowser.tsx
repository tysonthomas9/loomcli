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
import type { DiffFile } from "@/api/issues";
import type { FileCheckout } from "@/api/workspace";
import {
  deleteScopedPath,
  fetchDiffFiles,
  gitStatusScoped,
  indexScopedFiles,
  listScopedDir,
  listFileCheckouts,
  mkdirScoped,
  moveScopedPath,
  repairFileCheckout,
  readScopedFile,
  statScopedPath,
  writeScopedFile,
} from "@/hooks/api";
import {
  useToast,
  useWorkspaceContext,
  useEventContext,
  agentFileBrowserTabsStorageKey,
  FileDocumentRegistryProvider,
  FileCapabilitiesProvider,
  FileBrowserStoreProvider,
  fileBrowserTabsStorageKey,
  useFileDocumentRegistry,
  useFileDocumentRegistryRevision,
  useFileCapabilities,
  useFileBrowserStoreInstance,
  useSkillsActions,
  useSkillsCatalog,
  type FileBrowserTab,
} from "@/hooks";
import {
  checkoutLabel,
  checkoutRefKey,
  mapWorkspaceIndexPathToCheckout,
  sameCheckoutRef,
  type CheckoutRef,
} from "@/utils/fileExplorerRefs";
import {
  asCheckoutRef,
  checkoutExplorerRef,
  explorerLabel,
  explorerRefKey,
  explorerTitle,
  sameExplorerRef,
  tabIdentityKey,
  type ExplorerRef,
  type SkillsExplorerRef,
} from "@/utils/explorerRefs";
import {
  parseSkillPath,
  SKILL_MD,
  validateSkillFilePath,
} from "@/utils/skillsPaths";
import { wsGet, wsSet } from "@/utils/scopedStorage";

import type { FileTreeNodeInfo } from "./FileTree";
import {
  ContextMenu,
  DeleteConfirmDialog,
  MoveToDialog,
} from "./FileExplorerDialogs";
import { SkillsBrowserOverlays } from "./skills";
import { CapabilityNotices, CheckoutRepairOverlays } from "./overlays";
import { FileExplorerEditorGroup } from "./FileExplorerEditorGroup";
import { FileExplorerTreePanel } from "./FileExplorerTreePanel";
import type {
  HistoryOpenDiffRequest,
  HistoryOpenRevisionRequest,
} from "./FileHistoryPanel";
import { FileSearchPanel } from "./FileSearchPanel";
import { QuickOpenPalette } from "./QuickOpenPalette";
import {
  checkoutRepairRequest,
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
  getStoredCompareMode,
  getStoredLens,
  getStoredTreeWidth,
  isConflictError,
  isPreconditionError,
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
  storeCompareMode,
  storeLens,
  storeTreeWidth,
} from "./fileExplorerLocalUtils";
import {
  buildFileTreeSections,
  existingExplorerRefs,
  gitStatusRefs,
} from "./treeRoots";
import {
  buildBranchChangeGroups,
  buildChangeGroups,
  checkoutRefFromCheckout,
  type ChangeCheckoutGroup,
} from "./changesLens";
import type { QuickOpenItem } from "./quickOpen";
import styles from "./FileExplorer.module.css";
import type {
  ContextMenuState,
  CheckoutRepairMenuState,
  CompareMode,
  DeleteConfirmState,
  DeleteSkillState,
  DiffViewState,
  ExplorerLens,
  FileBrowserProps,
  LineTarget,
  MoveDialogState,
  RepairConfirmState,
  RevisionViewState,
  ScopedInlineEdit,
  SkillGroupMenuState,
  TreeRefreshRequest,
  TreeRevealRequest,
} from "./workspaceFileBrowserTypes";

interface BranchDiffRequest {
  key: string;
  agent: string;
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
  const documentRegistry = useFileDocumentRegistry();
  const documentRevision = useFileDocumentRegistryRevision();
  const {
    capabilities,
    isLoading: capabilitiesLoading,
    error: capabilitiesError,
    retry: retryCapabilities,
  } = useFileCapabilities();
  const canWrite = capabilities?.write === true;
  const skillsCatalog = useSkillsCatalog(workspaceId);
  const invalidateSkillsCatalog = skillsCatalog.invalidate;
  const skillActions = useSkillsActions(workspaceId);
  const canEditExplorer = useCallback(
    (ref: ExplorerRef) =>
      ref.kind === "checkout" ? canWrite : skillActions.canEdit(ref.group),
    [canWrite, skillActions],
  );
  const groups = useStore(store, (s) => s.groups);
  const activeGroup = useStore(store, (s) => s.activeGroup);
  const dirty = useStore(store, (s) => s.dirty);
  const mru = useStore(store, (s) => s.mru);

  useEffect(() => {
    for (const tab of groups.flatMap((group) => group.tabs)) {
      const state = documentRegistry.get({
        workspaceId,
        ref: tab.ref,
        path: tab.path,
      });
      const key = tabIdentityKey(tab);
      if (!!store.getState().dirty[key] !== state.dirty) {
        store.getState().setDirty(key, state.dirty);
      }
    }
  }, [documentRegistry, documentRevision, groups, store, workspaceId]);

  const [treeWidth, setTreeWidth] = useState<number>(getStoredTreeWidth);
  const [lens, setLens] = useState<ExplorerLens>(() =>
    getStoredLens(workspaceId),
  );
  const [compareMode, setCompareMode] = useState<CompareMode>(() =>
    getStoredCompareMode(workspaceId),
  );
  const [splitLeftWidth, setSplitLeftWidth] = useState(DEFAULT_GROUP_WIDTH);
  const [lineTargets, setLineTargets] = useState<Record<string, LineTarget>>(
    {},
  );
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
  const [quickOpenLoading, setQuickOpenLoading] = useState(false);
  const [quickOpenError, setQuickOpenError] = useState<string | null>(null);
  const [gitStatusByRef, setGitStatusByRef] = useState<
    Record<string, Record<string, string>>
  >({});
  const [branchDiffsByRef, setBranchDiffsByRef] = useState<
    Record<string, DiffFile[] | undefined>
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
  const [skillGroupMenu, setSkillGroupMenu] =
    useState<SkillGroupMenuState | null>(null);
  const [newSkillRef, setNewSkillRef] = useState<SkillsExplorerRef | null>(
    null,
  );
  const [deleteSkillConfirm, setDeleteSkillConfirm] =
    useState<DeleteSkillState | null>(null);
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
  const branchDiffsInFlightRef = useRef<Set<string>>(new Set());
  const quickOpenRequestRef = useRef(0);
  const fetchQuickOpenIndexRef = useRef<(force?: boolean) => Promise<void>>(
    async () => undefined,
  );
  const quickOpenFetchedAtRef = useRef(0);
  const quickOpenSkillsRevisionRef = useRef<number | null>(null);

  useEffect(() => {
    if (canWrite) return;
    setInlineEdit((current) =>
      current?.ref.kind === "checkout" ? null : current,
    );
    setDeleteConfirm((current) =>
      current?.ref.kind === "checkout" ? null : current,
    );
    setMoveDialog(null);
    setRepairConfirm(null);
    setRepairMenu(null);
  }, [canWrite]);

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
  const branchBaseName = useMemo(() => {
    const defaultBranches = new Set<string>();
    const repoByName = new Map(repos.map((repo) => [repo.name, repo]));
    for (const checkout of visibleCheckouts) {
      if (checkout.kind !== "agent" || !checkout.repo) continue;
      const defaultBranch = repoByName
        .get(checkout.repo)
        ?.default_branch.trim();
      if (defaultBranch) defaultBranches.add(defaultBranch);
    }
    return defaultBranches.size === 1
      ? defaultBranches.values().next().value
      : undefined;
  }, [repos, visibleCheckouts]);
  const sections = useMemo(
    () =>
      buildFileTreeSections({
        mode,
        agentName,
        agents,
        repos,
        checkouts: visibleCheckouts,
        skills: skillsCatalog.groups,
      }),
    [mode, agentName, agents, repos, skillsCatalog.groups, visibleCheckouts],
  );
  const allSections = useMemo(
    () =>
      buildFileTreeSections({
        mode: "workspace",
        agents,
        repos,
        checkouts,
        skills: skillsCatalog.groups,
      }),
    [agents, repos, checkouts, skillsCatalog.groups],
  );
  const computedVisibleExplorerRefs = useMemo(
    () => existingExplorerRefs(sections),
    [sections],
  );
  const visibleRefsKey = computedVisibleExplorerRefs
    .map(explorerRefKey)
    .join("|");
  const visibleRefsRef = useRef<{ key: string; refs: ExplorerRef[] }>({
    key: "",
    refs: [],
  });
  if (visibleRefsRef.current.key !== visibleRefsKey) {
    visibleRefsRef.current = {
      key: visibleRefsKey,
      refs: computedVisibleExplorerRefs,
    };
  }
  const visibleExplorerRefs = visibleRefsRef.current.refs;
  const visibleCheckoutRefs = useMemo(
    () => gitStatusRefs(sections),
    [sections],
  );
  const visibleSkillRefs = useMemo(
    () =>
      visibleExplorerRefs.filter(
        (ref): ref is SkillsExplorerRef => ref.kind === "skills",
      ),
    [visibleExplorerRefs],
  );
  const computedStoreValidRefs = useMemo(
    () => existingExplorerRefs(allSections),
    [allSections],
  );
  const storeValidRefsKey = computedStoreValidRefs
    .map(explorerRefKey)
    .join("|");
  const storeValidRefsRef = useRef<{ key: string; refs: ExplorerRef[] }>({
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
  const knownRefs = useMemo(() => gitStatusRefs(allSections), [allSections]);
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
  const branchDiffRequests = useMemo<BranchDiffRequest[]>(() => {
    const seen = new Set<string>();
    return visibleCheckouts
      .filter(
        (checkout) =>
          checkout.kind === "agent" && hasAvailableCheckoutStatus(checkout),
      )
      .map((checkout) => {
        const ref = checkoutRefFromCheckout(checkout);
        return {
          key: checkoutRefKey(ref),
          agent: checkout.agent ?? "",
        };
      })
      .filter((request) => {
        if (seen.has(request.key)) return false;
        seen.add(request.key);
        return true;
      });
  }, [visibleCheckouts]);
  const branchDiffRequestsKey = branchDiffRequests
    .map((request) => `${request.key}:${request.agent}`)
    .join("|");
  const stableBranchDiffRequestsRef = useRef<{
    key: string;
    requests: BranchDiffRequest[];
  }>({ key: "", requests: [] });
  if (stableBranchDiffRequestsRef.current.key !== branchDiffRequestsKey) {
    stableBranchDiffRequestsRef.current = {
      key: branchDiffRequestsKey,
      requests: branchDiffRequests,
    };
  }
  const stableBranchDiffRequests = stableBranchDiffRequestsRef.current.requests;
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
  const statusRefs = lens === "changes" ? changesRefs : visibleCheckoutRefs;
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
  const branchChangeGroups = useMemo(
    () => buildBranchChangeGroups(visibleCheckouts, branchDiffsByRef),
    [branchDiffsByRef, visibleCheckouts],
  );
  const branchChangeCount = useMemo(
    () =>
      branchChangeGroups.reduce(
        (sum, group) => (group.loaded ? sum + group.items.length : sum),
        0,
      ),
    [branchChangeGroups],
  );
  const activeChangeCount =
    compareMode === "branch" ? branchChangeCount : checkoutChangeCount;
  const quickOpenIndexRefs = useMemo<CheckoutRef[]>(() => {
    if (mode !== "agent") return [];
    const agentRefs = visibleCheckoutRefs.filter(
      (ref) => ref.scope === "agent",
    );
    if (agentRefs.length > 0) return agentRefs;
    return agentName ? [{ scope: "agent", target: agentName }] : [];
  }, [mode, agentName, visibleCheckoutRefs]);
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
  const activeChangeGroups =
    compareMode === "branch" ? branchChangeGroups : visibleChangeGroups;
  const activeTab =
    groups[activeGroup]?.tabs.find(
      (tab) => tabIdentityKey(tab) === groups[activeGroup]?.active,
    ) ??
    groups[0]?.tabs.find((tab) => tabIdentityKey(tab) === groups[0]?.active) ??
    null;
  const workspaceRef = useMemo<CheckoutRef>(() => ({ scope: "workspace" }), []);
  const searchScopeRef = useMemo<CheckoutRef>(() => {
    if (mode !== "agent") return workspaceRef;
    const firstAgentRef = quickOpenIndexRefs[0];
    if (quickOpenIndexRefs.length === 1 && firstAgentRef) {
      return firstAgentRef;
    }
    const activeCheckout =
      activeTab?.ref.kind === "checkout" ? activeTab.ref.checkout : null;
    if (
      activeCheckout?.scope === "agent" &&
      quickOpenIndexRefs.some((ref) => sameCheckoutRef(ref, activeCheckout))
    ) {
      return activeCheckout;
    }
    return firstAgentRef ?? { scope: "agent", target: agentName ?? "" };
  }, [activeTab, agentName, mode, quickOpenIndexRefs, workspaceRef]);

  useEffect(() => {
    for (const group of visibleChangeGroups) {
      if (group.loaded) lastLoadedChangeGroupsRef.current.set(group.id, group);
    }
  }, [visibleChangeGroups]);

  useEffect(() => {
    if (skillsCatalog.status === "loaded") {
      store.getState().pruneUnavailableRefs(storeValidRefs);
    }
  }, [skillsCatalog.status, store, storeValidRefs]);

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
    setCompareMode(getStoredCompareMode(workspaceId));
    setBranchDiffsByRef({});
    branchDiffsInFlightRef.current.clear();
  }, [workspaceId]);

  const changeLens = useCallback(
    (nextLens: ExplorerLens) => {
      setLens(nextLens);
      storeLens(workspaceId, nextLens);
    },
    [workspaceId],
  );

  const changeCompareMode = useCallback(
    (nextMode: CompareMode) => {
      setCompareMode(nextMode);
      storeCompareMode(workspaceId, nextMode);
    },
    [workspaceId],
  );

  const markIndexStale = useCallback(() => {
    quickOpenFetchedAtRef.current = 0;
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
          next[key] = (await gitStatusScoped(workspaceId, ref)).status;
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

  const refreshBranchDiffs = useCallback(async () => {
    await Promise.all(
      stableBranchDiffRequests.map(async ({ key, agent }) => {
        if (branchDiffsInFlightRef.current.has(key)) return;
        branchDiffsInFlightRef.current.add(key);
        try {
          const files = await fetchDiffFiles(workspaceId, agent, "HEAD");
          setBranchDiffsByRef((prev) => ({ ...prev, [key]: files }));
        } catch {
          setBranchDiffsByRef((prev) => ({ ...prev, [key]: [] }));
        } finally {
          branchDiffsInFlightRef.current.delete(key);
        }
      }),
    );
  }, [stableBranchDiffRequests, workspaceId]);

  const fetchQuickOpenIndex = useCallback(
    async (force = false) => {
      const now = Date.now();
      const skillsCacheIsCurrent =
        skillsCatalog.status === "loaded" &&
        quickOpenSkillsRevisionRef.current === skillsCatalog.revision;
      if (
        !force &&
        quickOpenFetchedAtRef.current > 0 &&
        now - quickOpenFetchedAtRef.current < QUICK_OPEN_STALE_MS &&
        skillsCacheIsCurrent
      ) {
        return;
      }
      const requestId = ++quickOpenRequestRef.current;
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
        const checkoutItems = indexes.flatMap(({ ref, index }) =>
          index.paths.map((rawPath) => {
            const mapped =
              mode === "agent"
                ? { ref, path: rawPath }
                : mapWorkspaceIndexPathToCheckout(rawPath, knownRefs);
            return {
              id: tabIdentityKey({
                ref: checkoutExplorerRef(mapped.ref),
                path: mapped.path,
              }),
              ref: checkoutExplorerRef(mapped.ref),
              path: mapped.path,
              checkoutLabel: checkoutLabel(mapped.ref),
            };
          }),
        );
        const skillItems = visibleSkillRefs.flatMap((ref) =>
          skillActions.listIndexPaths(ref.group).map((path) => ({
            id: tabIdentityKey({ ref, path }),
            ref,
            path,
            checkoutLabel: explorerLabel(ref),
          })),
        );
        if (requestId !== quickOpenRequestRef.current) return;
        setQuickOpenItems([...checkoutItems, ...skillItems]);
        setQuickOpenTruncated(
          indexes.some(({ index }) => Boolean(index.truncated)),
        );
        quickOpenSkillsRevisionRef.current =
          skillsCatalog.status === "loaded" ? skillsCatalog.revision : null;
        quickOpenFetchedAtRef.current = Date.now();
      } catch (err) {
        if (requestId !== quickOpenRequestRef.current) return;
        setQuickOpenError(err instanceof Error ? err.message : String(err));
      } finally {
        if (requestId === quickOpenRequestRef.current) {
          setQuickOpenLoading(false);
        }
      }
    },
    [
      knownRefs,
      mode,
      quickOpenIndexRefs,
      skillActions,
      skillsCatalog.revision,
      skillsCatalog.status,
      visibleSkillRefs,
      workspaceId,
    ],
  );

  useEffect(() => {
    if (!contextMenu && !repairMenu && !skillGroupMenu) return;
    const close = () => setContextMenu(null);
    const closeRepair = () => setRepairMenu(null);
    const closeSkillGroup = () => setSkillGroupMenu(null);
    window.addEventListener("click", close);
    window.addEventListener("click", closeRepair);
    window.addEventListener("keydown", close);
    window.addEventListener("keydown", closeRepair);
    window.addEventListener("click", closeSkillGroup);
    window.addEventListener("keydown", closeSkillGroup);
    return () => {
      window.removeEventListener("click", close);
      window.removeEventListener("click", closeRepair);
      window.removeEventListener("keydown", close);
      window.removeEventListener("keydown", closeRepair);
      window.removeEventListener("click", closeSkillGroup);
      window.removeEventListener("keydown", closeSkillGroup);
    };
  }, [contextMenu, repairMenu, skillGroupMenu]);

  useEffect(() => {
    fetchQuickOpenIndexRef.current = fetchQuickOpenIndex;
  }, [fetchQuickOpenIndex]);

  useEffect(() => {
    if (quickOpenOpen) {
      void fetchQuickOpenIndexRef.current();
    }
  }, [quickOpenOpen, skillsCatalog.revision, skillsCatalog.status]);

  useEffect(() => {
    void refreshCheckouts();
  }, [refreshCheckouts]);

  useEffect(() => {
    void refreshGitStatus();
    void refreshBranchDiffs();
  }, [refreshBranchDiffs, refreshGitStatus]);

  useEffect(() => {
    const handleFocus = () => {
      void refreshCheckouts();
      void refreshGitStatus();
      void refreshBranchDiffs();
      invalidateSkillsCatalog();
    };
    window.addEventListener("focus", handleFocus);
    return () => window.removeEventListener("focus", handleFocus);
  }, [
    refreshBranchDiffs,
    refreshCheckouts,
    refreshGitStatus,
    invalidateSkillsCatalog,
  ]);

  useEffect(() => {
    const previous = reconnectAttemptsRef.current;
    reconnectAttemptsRef.current = eventContext.reconnectAttempts;
    if (
      eventContext.reconnectAttempts > 0 ||
      (previous > 0 && eventContext.state === "connected")
    ) {
      void refreshCheckouts();
      void refreshGitStatus();
      void refreshBranchDiffs();
      invalidateSkillsCatalog();
    }
  }, [
    eventContext.reconnectAttempts,
    eventContext.state,
    refreshBranchDiffs,
    refreshCheckouts,
    refreshGitStatus,
    invalidateSkillsCatalog,
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

  const expandForRef = useCallback((ref: ExplorerRef) => {
    setExpandedRoots((prev) => {
      const next = new Set(prev);
      next.add(explorerRefKey(ref));
      const checkout = asCheckoutRef(ref);
      if (checkout?.scope === "agent" && checkout.target) {
        next.add(`agent:${checkout.target}`);
      }
      return next;
    });
  }, []);

  const revealInTree = useCallback(
    (ref: ExplorerRef, path: string) => {
      expandForRef(ref);
      const key = explorerRefKey(ref);
      setTreeRevealRequests((prev) => ({
        ...prev,
        [key]: { path, token: (prev[key]?.token ?? 0) + 1 },
      }));
    },
    [expandForRef],
  );

  const refreshParents = useCallback((ref: ExplorerRef, ...paths: string[]) => {
    const key = explorerRefKey(ref);
    setTreeRefreshRequests((prev) => ({
      ...prev,
      [key]: { paths, token: (prev[key]?.token ?? 0) + 1 },
    }));
  }, []);

  const discardTabDraft = useCallback(
    (tab: FileBrowserTab | undefined, key: string) => {
      const state = store.getState();
      state.setDirty(key, false);
      if (!tab) return;
      state.setDirty(tabIdentityKey(tab), false);
      documentRegistry.discard({
        workspaceId,
        ref: tab.ref,
        path: tab.path,
      });
    },
    [documentRegistry, store, workspaceId],
  );

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
      discardTabDraft(tab, current);
      return true;
    },
    [discardTabDraft, store],
  );

  const openFile = useCallback(
    (
      ref: ExplorerRef,
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
        const label = tab ? explorerTitle(tab.ref, tab.path) : key;
        const ok = window.confirm(`Discard unsaved changes in ${label}?`);
        if (!ok) return;
        discardTabDraft(tab, key);
      }
      setDiffViews((prev) => ({ ...prev, [groupIndex]: null }));
      setRevisionViews((prev) => ({ ...prev, [groupIndex]: null }));
      store.getState().closeTab(groupIndex, key);
    },
    [discardTabDraft, store],
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
          `Discard unsaved changes in ${explorerTitle(tab.ref, tab.path)} before splitting?`,
        );
        if (!ok) return;
        discardTabDraft(tab, key);
      }
      state.splitRight(tab);
    },
    [discardTabDraft, store],
  );

  const handleSaved = useCallback(
    (tab: FileBrowserTab) => {
      markIndexStale();
      if (tab.ref.kind === "skills") {
        invalidateSkillsCatalog();
        refreshParents(tab.ref, tab.path);
        showToast("Skill file saved", { type: "success" });
        const saved = parseSkillPath(tab.path);
        if (!saved) return;
        const siblings = store
          .getState()
          .groups.flatMap((group) => group.tabs)
          .filter((candidate) => {
            const parsed = parseSkillPath(candidate.path);
            return (
              candidate.path !== tab.path &&
              sameExplorerRef(candidate.ref, tab.ref) &&
              parsed?.skill === saved.skill
            );
          });
        void Promise.all(
          siblings.map(async (sibling) => {
            const ref = {
              workspaceId,
              ref: sibling.ref,
              path: sibling.path,
            };
            const before = documentRegistry.get(ref).baseVersion;
            await documentRegistry.refresh(ref);
            return documentRegistry.get(ref).baseVersion !== before
              ? sibling.path
              : null;
          }),
        ).then((changed) => {
          const paths = changed.filter((path): path is string => Boolean(path));
          if (paths.length > 0) {
            showToast(
              `Another file in this skill changed during save — check ${paths.join(", ")}`,
              { type: "warning" },
            );
          }
        });
        return;
      }
      void refreshCheckouts();
      void refreshGitStatus();
      void refreshBranchDiffs();
      setHistoryRefreshKey((key) => key + 1);
      showToast("File saved", { type: "success" });
      refreshParents(tab.ref, tab.path);
    },
    [
      markIndexStale,
      refreshBranchDiffs,
      refreshCheckouts,
      refreshGitStatus,
      refreshParents,
      showToast,
      invalidateSkillsCatalog,
      store,
      workspaceId,
      documentRegistry,
    ],
  );

  const openDiff = useCallback(
    (
      groupIndex: number,
      request:
        | HistoryOpenDiffRequest
        | Extract<DiffViewState, { kind: "patch" }>,
    ) => {
      if ("kind" in request && request.kind === "patch") {
        setDiffViews((prev) => ({ ...prev, [groupIndex]: request }));
        setRevisionViews((prev) => ({ ...prev, [groupIndex]: null }));
        return;
      }
      const checkoutRequest = request as HistoryOpenDiffRequest;
      const title =
        checkoutRequest.title ??
        (checkoutRequest.source === "branch"
          ? "vs base"
          : checkoutRequest.to
            ? `${checkoutRequest.from ?? "HEAD"}..${checkoutRequest.to}`
            : `${checkoutRequest.from ?? "HEAD"} vs working tree`);
      setDiffViews((prev) => ({
        ...prev,
        [groupIndex]: { kind: "checkout", ...checkoutRequest, title },
      }));
      setRevisionViews((prev) => ({ ...prev, [groupIndex]: null }));
    },
    [],
  );

  const closeDiff = useCallback((groupIndex: number) => {
    setDiffViews((prev) => ({ ...prev, [groupIndex]: null }));
  }, []);

  const openRevision = useCallback(
    (groupIndex: number, request: HistoryOpenRevisionRequest) => {
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
    (ref: ExplorerRef, dirPath: string) => {
      revealInTree(ref, dirPath);
    },
    [revealInTree],
  );

  const beginCreate = useCallback(
    (
      ref: ExplorerRef,
      kind: "create-file" | "create-folder",
      node: FileTreeNodeInfo,
    ) => {
      if (!canEditExplorer(ref)) return;
      if (ref.kind === "skills" && kind === "create-folder") return;
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
    [canEditExplorer],
  );

  const beginRename = useCallback(
    (ref: CheckoutRef, node: FileTreeNodeInfo) => {
      if (!canWrite) return;
      setContextMenu(null);
      setInlineEdit({
        ref: checkoutExplorerRef(ref),
        edit: {
          kind: "rename",
          parentPath: dirname(node.path),
          path: node.path,
          value: node.name,
          isDir: node.isDir,
        },
      });
    },
    [canWrite],
  );

  const commitInlineEdit = useCallback(async () => {
    if (!inlineEdit || !canEditExplorer(inlineEdit.ref)) return;
    const edit = inlineEdit.edit;
    const ref = inlineEdit.ref;
    const value = edit.value.trim();
    if (!value) {
      setInlineEdit(null);
      return;
    }
    const commitKey = `${explorerRefKey(ref)}:${edit.kind}:${edit.path ?? edit.parentPath}:${value}`;
    if (inlineCommitKeyRef.current === commitKey) return;
    inlineCommitKeyRef.current = commitKey;
    setInlineEdit(null);
    try {
      if (ref.kind === "skills") {
        if (edit.kind !== "create-file") return;
        const fullPath = joinPath(edit.parentPath, value);
        const parsed = parseSkillPath(fullPath);
        const validation = parsed
          ? validateSkillFilePath(parsed.file)
          : "Choose a file inside a skill";
        if (!parsed || validation || parsed.file === SKILL_MD) {
          throw new Error(validation ?? `${SKILL_MD} already exists`);
        }
        await skillActions.createFile(ref.group, parsed.skill, parsed.file);
        markIndexStale();
        refreshParents(ref, fullPath);
        openFile(ref, fullPath);
        revealInTree(ref, fullPath);
        return;
      }
      const checkoutRef = ref.checkout;
      if (edit.kind === "create-file") {
        const path = joinPath(edit.parentPath, value);
        await writeScopedFile(workspaceId, checkoutRef, path, "", {
          createOnly: true,
        });
        markIndexStale();
        void refreshCheckouts();
        void refreshGitStatus();
        void refreshBranchDiffs();
        refreshParents(ref, path);
        openFile(ref, path);
        revealInTree(ref, path);
      } else if (edit.kind === "create-folder") {
        const path = joinPath(edit.parentPath, value);
        await mkdirScoped(workspaceId, checkoutRef, path);
        markIndexStale();
        void refreshCheckouts();
        void refreshGitStatus();
        void refreshBranchDiffs();
        refreshParents(ref, path);
        revealInTree(ref, path);
      } else if (edit.path) {
        const nextPath = joinPath(edit.parentPath, value);
        if (nextPath !== edit.path) {
          const source = await statScopedPath(
            workspaceId,
            checkoutRef,
            edit.path,
          );
          await moveScopedPath(
            workspaceId,
            checkoutRef,
            edit.path,
            nextPath,
            false,
            source.version,
          );
          documentRegistry.retargetPathPrefix(
            workspaceId,
            ref,
            edit.path,
            nextPath,
          );
          store.getState().retargetPathPrefix(ref, edit.path, nextPath);
          markIndexStale();
          void refreshCheckouts();
          void refreshGitStatus();
          void refreshBranchDiffs();
          refreshParents(ref, edit.path, nextPath);
          revealInTree(ref, nextPath);
        }
      }
    } catch (err) {
      setInlineEdit({ ref, edit });
      showToast(err instanceof Error ? err.message : String(err), {
        type: "error",
      });
    } finally {
      inlineCommitKeyRef.current = null;
    }
  }, [
    inlineEdit,
    canEditExplorer,
    workspaceId,
    refreshParents,
    openFile,
    revealInTree,
    store,
    showToast,
    markIndexStale,
    refreshBranchDiffs,
    refreshCheckouts,
    refreshGitStatus,
    documentRegistry,
    skillActions,
  ]);

  const dirtyTabsForPath = useCallback(
    (ref: ExplorerRef, path: string): Array<{ key: string; path: string }> => {
      const state = store.getState();
      const dirtyKeys = new Set(Object.keys(state.dirty));
      return state.groups
        .flatMap((group) => group.tabs)
        .filter(
          (tab) =>
            dirtyKeys.has(tabIdentityKey(tab)) &&
            sameExplorerRef(tab.ref, ref) &&
            pathMatchesPrefix(tab.path, path),
        )
        .map((tab) => ({ key: tabIdentityKey(tab), path: tab.path }));
    },
    [store],
  );

  const performDelete = useCallback(
    async (
      ref: ExplorerRef,
      node: FileTreeNodeInfo,
      skipFutureFileConfirms = false,
    ) => {
      if (!canEditExplorer(ref)) return;
      const dirtyTabs = dirtyTabsForPath(ref, node.path);
      const dirtyDocuments = documentRegistry.dirtyPathsForPrefix(
        workspaceId,
        ref,
        node.path,
      );
      const dirtyCount = new Set([
        ...dirtyTabs.map((tab) => tab.path),
        ...dirtyDocuments,
      ]).size;
      if (dirtyCount > 0) {
        const ok = window.confirm(
          `Discard unsaved changes in ${dirtyCount} open file${dirtyCount === 1 ? "" : "s"}?`,
        );
        if (!ok) return;
      }
      try {
        if (ref.kind === "skills") {
          const parsed = parseSkillPath(node.path);
          if (!parsed || node.isDir || parsed.file === SKILL_MD) return;
          await skillActions.deleteFile(ref.group, parsed.skill, parsed.file);
        } else {
          const source = await statScopedPath(
            workspaceId,
            ref.checkout,
            node.path,
          );
          await deleteScopedPath(
            workspaceId,
            ref.checkout,
            node.path,
            node.isDir,
            source.version,
          );
        }
        for (const tab of dirtyTabs) {
          store.getState().setDirty(tab.key, false);
        }
        documentRegistry.resetPathPrefix(workspaceId, ref, node.path);
        markIndexStale();
        void refreshCheckouts();
        void refreshGitStatus();
        void refreshBranchDiffs();
        if (ref.kind === "checkout" && !node.isDir && skipFutureFileConfirms) {
          wsSet(workspaceId, DELETE_FILE_SKIP_KEY, "1");
        }
        store.getState().closePathPrefix(ref, node.path);
        refreshParents(ref, node.path);
        showToast("Deleted", { type: "success" });
        setDeleteConfirm(null);
      } catch (err) {
        showToast(err instanceof Error ? err.message : String(err), {
          type: "error",
        });
      }
    },
    [
      dirtyTabsForPath,
      workspaceId,
      store,
      refreshParents,
      showToast,
      markIndexStale,
      refreshBranchDiffs,
      refreshCheckouts,
      refreshGitStatus,
      documentRegistry,
      canEditExplorer,
      skillActions,
    ],
  );

  const requestDelete = useCallback(
    (ref: ExplorerRef, node: FileTreeNodeInfo) => {
      if (!canEditExplorer(ref)) return;
      setContextMenu(null);
      if (ref.kind === "skills") {
        if (node.depth === 0) {
          setDeleteSkillConfirm({ ref, name: node.name });
        } else if (!node.isDir && !node.path.endsWith(`/${SKILL_MD}`)) {
          setDeleteConfirm({ ref, node });
        }
        return;
      }
      const skipFileConfirm =
        !node.isDir && wsGet(workspaceId, DELETE_FILE_SKIP_KEY) === "1";
      if (skipFileConfirm) {
        void performDelete(ref, node);
      } else {
        setDeleteConfirm({ ref, node });
      }
    },
    [canEditExplorer, performDelete, workspaceId],
  );

  const duplicateFile = useCallback(
    async (ref: CheckoutRef, node: FileTreeNodeInfo) => {
      if (!canWrite) return;
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
        let nextPath = "";
        for (let attempt = 0; attempt < 2; attempt += 1) {
          const entries = sortedEntries(
            (await listScopedDir(workspaceId, ref, parent)).entries,
          );
          const nextName = duplicateName(basename(node.path), entries);
          nextPath = joinPath(parent, nextName);
          try {
            await writeScopedFile(
              workspaceId,
              ref,
              nextPath,
              data.content ?? "",
              { createOnly: true },
            );
            break;
          } catch (err) {
            if (attempt === 0 && isPreconditionError(err)) continue;
            throw err;
          }
        }
        markIndexStale();
        void refreshCheckouts();
        void refreshGitStatus();
        void refreshBranchDiffs();
        const explorerRef = checkoutExplorerRef(ref);
        refreshParents(explorerRef, nextPath);
        openFile(explorerRef, nextPath);
      } catch (err) {
        showToast(err instanceof Error ? err.message : String(err), {
          type: "error",
        });
      }
    },
    [
      workspaceId,
      canWrite,
      refreshParents,
      openFile,
      showToast,
      markIndexStale,
      refreshBranchDiffs,
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
      ref: ExplorerRef,
      node: FileTreeNodeInfo,
      event: MouseEvent<HTMLDivElement>,
    ) => {
      const next = {
        ref,
        node,
        x: event.clientX,
        y: event.clientY,
        duplicateEligible: false,
      };
      setContextMenu(next);
      if (ref.kind !== "checkout" || !canWrite || node.isDir) return;
      void readScopedFile(workspaceId, ref.checkout, node.path)
        .then((data) => {
          if (data.binary || data.truncated) return;
          setContextMenu((current) =>
            current &&
            sameExplorerRef(current.ref, ref) &&
            current.node.path === node.path
              ? { ...current, duplicateEligible: true }
              : current,
          );
        })
        .catch(() => {});
    },
    [canWrite, workspaceId],
  );

  const performCheckoutRepair = useCallback(
    async (ref: CheckoutRef, label: string, force = false) => {
      if (!canWrite) return;
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
        void refreshBranchDiffs();
        refreshParents(checkoutExplorerRef(ref), "");
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
      refreshBranchDiffs,
      refreshCheckouts,
      refreshGitStatus,
      refreshParents,
      showToast,
      canWrite,
    ],
  );

  const handleCheckoutContextMenu = useCallback(
    (ref: CheckoutRef, label: string, event: MouseEvent<HTMLDivElement>) => {
      if (!canWrite) return;
      setContextMenu(null);
      setRepairMenu({ ref, label, x: event.clientX, y: event.clientY });
    },
    [canWrite],
  );

  const performMove = useCallback(
    async (
      ref: CheckoutRef,
      node: FileTreeNodeInfo,
      targetFolderPath: string,
    ) => {
      if (!canWrite) return;
      const explorerRef = checkoutExplorerRef(ref);
      const move = resolveMoveToTarget(node.path, targetFolderPath);
      if (!move) return;
      let discardedDestinationTabs: string[] = [];
      let overwroteDestination = false;
      try {
        const source = await statScopedPath(workspaceId, ref, move.from);
        await moveScopedPath(
          workspaceId,
          ref,
          move.from,
          move.to,
          false,
          source.version,
        );
      } catch (err) {
        if (!isConflictError(err)) {
          showToast(err instanceof Error ? err.message : String(err), {
            type: "error",
          });
          return;
        }
        const ok = window.confirm(`Overwrite ${move.to}?`);
        if (!ok) return;
        const dirtyDestinationTabs = dirtyTabsForPath(explorerRef, move.to);
        const dirtyDestinationDocuments = documentRegistry.dirtyPathsForPrefix(
          workspaceId,
          explorerRef,
          move.to,
        );
        const dirtyDestinationCount = new Set([
          ...dirtyDestinationTabs.map((tab) => tab.path),
          ...dirtyDestinationDocuments,
        ]).size;
        if (dirtyDestinationCount > 0) {
          const discard = window.confirm(
            `Discard unsaved changes in ${dirtyDestinationCount} destination file${dirtyDestinationCount === 1 ? "" : "s"}?`,
          );
          if (!discard) return;
          discardedDestinationTabs = dirtyDestinationTabs.map((tab) => tab.key);
        }
        try {
          const [source, destination] = await Promise.all([
            statScopedPath(workspaceId, ref, move.from),
            statScopedPath(workspaceId, ref, move.to),
          ]);
          await moveScopedPath(
            workspaceId,
            ref,
            move.from,
            move.to,
            true,
            source.version,
            destination.version,
          );
          overwroteDestination = true;
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

      for (const key of discardedDestinationTabs) {
        store.getState().setDirty(key, false);
      }
      if (overwroteDestination) {
        documentRegistry.resetPathPrefix(workspaceId, explorerRef, move.to);
      }
      documentRegistry.retargetPathPrefix(
        workspaceId,
        explorerRef,
        move.from,
        move.to,
      );
      store.getState().retargetPathPrefix(explorerRef, move.from, move.to);
      markIndexStale();
      void refreshCheckouts();
      void refreshGitStatus();
      void refreshBranchDiffs();
      refreshParents(explorerRef, move.from, move.to);
      revealInTree(explorerRef, move.to);
      showToast("Moved", { type: "success" });
      setMoveDialog(null);
    },
    [
      workspaceId,
      canWrite,
      documentRegistry,
      store,
      markIndexStale,
      refreshBranchDiffs,
      refreshCheckouts,
      refreshGitStatus,
      refreshParents,
      revealInTree,
      showToast,
      dirtyTabsForPath,
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
        <CapabilityNotices
          workspaceId={workspaceId}
          filesLoading={capabilitiesLoading}
          filesError={capabilitiesError}
          retryFiles={retryCapabilities}
        />
        {searchPanelOpen ? (
          <FileSearchPanel
            workspaceId={workspaceId}
            scopeRef={searchScopeRef}
            canWrite={canWrite}
            onOpenResult={(path, line) =>
              openFile(
                checkoutExplorerRef(searchScopeRef),
                path,
                undefined,
                line,
              )
            }
            onFilesChanged={(paths, complete) => {
              for (const path of paths) {
                void documentRegistry.refresh({
                  workspaceId,
                  ref: checkoutExplorerRef(searchScopeRef),
                  path,
                });
              }
              markIndexStale();
              void refreshCheckouts();
              void refreshGitStatus();
              void refreshBranchDiffs();
              refreshParents(checkoutExplorerRef(searchScopeRef), ...paths);
              if (complete) showToast("Replace applied", { type: "success" });
            }}
            onClose={() => setSearchPanelOpen(false)}
          />
        ) : (
          <FileExplorerTreePanel
            workspaceId={workspaceId}
            lens={lens}
            changeCount={activeChangeCount}
            compareMode={compareMode}
            branchChangeCount={branchChangeCount}
            workingChangeCount={checkoutChangeCount}
            branchBaseName={branchBaseName}
            checkoutError={checkoutError}
            repairError={repairError}
            sections={sections}
            changeGroups={activeChangeGroups}
            unavailableCheckoutLabels={unavailableChangeCheckoutLabels}
            expandedRoots={expandedRoots}
            repairingCheckoutKey={repairingCheckoutKey}
            canWrite={canWrite}
            canEditSkills={(ref) => canEditExplorer(ref)}
            selectedTab={selectedTab}
            inlineEdit={inlineEdit}
            gitStatusByRef={gitStatusByRef}
            treeRevealRequests={treeRevealRequests}
            treeRefreshRequests={treeRefreshRequests}
            hideAgentSectionHeading={mode === "agent"}
            onLensChange={changeLens}
            onCompareModeChange={changeCompareMode}
            onQuickOpen={() => setQuickOpenOpen(true)}
            onOpenDiff={(request) =>
              openDiff(store.getState().activeGroup, request)
            }
            onToggleRoot={toggleRoot}
            onRepairCheckout={(ref, label) =>
              void performCheckoutRepair(ref, label)
            }
            onCheckoutContextMenu={handleCheckoutContextMenu}
            onSkillGroupContextMenu={(ref, event) => {
              if (!canEditExplorer(ref)) return;
              setContextMenu(null);
              setSkillGroupMenu({
                ref,
                x: event.clientX,
                y: event.clientY,
              });
            }}
            onNewSkill={(ref) => {
              if (canEditExplorer(ref)) setNewSkillRef(ref);
            }}
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
                openFile(checkoutExplorerRef(ref), path, groupIndex)
              }
              historyRefreshKey={historyRefreshKey}
              canWrite={canWrite}
              onDeleteSkill={(ref, name) =>
                setDeleteSkillConfirm({ ref, name })
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
                    openFile(checkoutExplorerRef(ref), path, groupIndex)
                  }
                  historyRefreshKey={historyRefreshKey}
                  canWrite={canWrite}
                  onDeleteSkill={(ref, name) =>
                    setDeleteSkillConfirm({ ref, name })
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
          canWrite={canEditExplorer(contextMenu.ref)}
          onNewFile={(node) =>
            beginCreate(contextMenu.ref, "create-file", node)
          }
          onNewFolder={(node) =>
            beginCreate(contextMenu.ref, "create-folder", node)
          }
          onRename={(node) => {
            if (contextMenu.ref.kind === "checkout") {
              beginRename(contextMenu.ref.checkout, node);
            }
          }}
          onDelete={(node) => requestDelete(contextMenu.ref, node)}
          onMove={(node) => {
            if (contextMenu.ref.kind !== "checkout") return;
            setContextMenu(null);
            setMoveDialog({ ref: contextMenu.ref.checkout, node });
          }}
          onDuplicate={(node) => {
            if (contextMenu.ref.kind === "checkout") {
              void duplicateFile(contextMenu.ref.checkout, node);
            }
          }}
          onCopyPath={copyPath}
        />
      )}
      <CheckoutRepairOverlays
        canWrite={canWrite}
        menu={repairMenu}
        confirm={repairConfirm}
        onCloseMenu={() => setRepairMenu(null)}
        onCloseConfirm={() => setRepairConfirm(null)}
        onRepair={(ref, label, force) =>
          void performCheckoutRepair(ref, label, force)
        }
      />
      {deleteConfirm && canEditExplorer(deleteConfirm.ref) && (
        <DeleteConfirmDialog
          node={deleteConfirm.node}
          allowSkip={deleteConfirm.ref.kind === "checkout"}
          onCancel={() => setDeleteConfirm(null)}
          onConfirm={(skip) =>
            void performDelete(deleteConfirm.ref, deleteConfirm.node, skip)
          }
        />
      )}
      {canWrite && moveDialog && (
        <MoveToDialog
          state={moveDialog}
          onCancel={() => setMoveDialog(null)}
          onConfirm={(target) =>
            void performMove(moveDialog.ref, moveDialog.node, target)
          }
        />
      )}
      <SkillsBrowserOverlays
        menu={skillGroupMenu}
        newSkillRef={newSkillRef}
        deleteSkill={deleteSkillConfirm}
        canEdit={(ref) => canEditExplorer(ref)}
        onChooseNew={setNewSkillRef}
        onCloseMenu={() => setSkillGroupMenu(null)}
        onCancelNew={() => setNewSkillRef(null)}
        onSkillCreated={(ref, path) => {
          setNewSkillRef(null);
          markIndexStale();
          revealInTree(ref, path);
          openFile(ref, path);
        }}
        onCancelDelete={() => setDeleteSkillConfirm(null)}
        onSkillDeleted={() => {
          setDeleteSkillConfirm(null);
          markIndexStale();
        }}
      />
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

export function WorkspaceFileBrowser({
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
    <FileCapabilitiesProvider workspaceId={workspaceId}>
      <FileDocumentRegistryProvider>
        <FileBrowserStoreProvider
          key={`${workspaceId}:${storageKey}`}
          workspaceId={workspaceId}
          storageKey={storageKey}
        >
          <FileBrowserInner
            mode={mode}
            agentName={agentName}
            isActive={isActive}
          />
        </FileBrowserStoreProvider>
      </FileDocumentRegistryProvider>
    </FileCapabilitiesProvider>
  );
}
