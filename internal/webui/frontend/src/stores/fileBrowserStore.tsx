/**
 * Zustand vanilla store for File Browser v4 tab state.
 * One store instance is created per workspace; each tab carries an explorer ref.
 */

import {
  createContext,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import { useStore } from "zustand";
import { createStore, type StoreApi } from "zustand/vanilla";

import type { FileScopeRef } from "@/api/workspace";
import { wsGet, wsSet } from "@/utils/scopedStorage";
import { cleanPath } from "@/utils/fileExplorerRefs";
import {
  checkoutExplorerRef,
  explorerRefKey,
  normalizeExplorerRef,
  sameExplorerRef,
  tabIdentityKey,
  type ExplorerRef,
} from "@/utils/explorerRefs";
import { parseSkillPath, validateRoleName } from "@/utils/skillsPaths";

const FILE_BROWSER_TABS_STORAGE_KEY = "file-browser-tabs:v3";

export interface FileBrowserTab {
  ref: ExplorerRef;
  path: string;
}

export interface FileBrowserGroup {
  tabs: FileBrowserTab[];
  /** Stable tab key produced by tabIdentityKey(tab). */
  active: string | null;
}

export interface PersistedFileBrowserTabsV4 {
  v: 4;
  groups: FileBrowserGroup[];
  mru: FileBrowserTab[];
}

export interface FileBrowserStoreState extends PersistedFileBrowserTabsV4 {
  dirty: Record<string, boolean>;
  activeGroup: number;
}

export interface FileBrowserStoreActions {
  openTab: (tab: FileBrowserTab, groupIndex?: number) => void;
  activateTab: (groupIndex: number, tabKey: string) => void;
  closeTab: (groupIndex: number, tabKey: string) => void;
  closePathPrefix: (ref: ExplorerRef, path: string) => void;
  splitRight: (tab?: FileBrowserTab | null) => void;
  setDirty: (tabKey: string, dirty: boolean) => void;
  retargetPathPrefix: (ref: ExplorerRef, from: string, to: string) => void;
  pruneUnavailableRefs: (validRefs: ExplorerRef[]) => void;
  reset: () => void;
}

export type FileBrowserStore = FileBrowserStoreState & FileBrowserStoreActions;

export interface FileBrowserStoreConfig {
  workspaceId: string;
  validRefs?: ExplorerRef[] | undefined;
  storageKey?: string | undefined;
}

const EMPTY_FILE_BROWSER_STATE: PersistedFileBrowserTabsV4 = {
  v: 4,
  groups: [{ tabs: [], active: null }],
  mru: [],
};

export function fileBrowserTabsStorageKey(): string {
  return FILE_BROWSER_TABS_STORAGE_KEY;
}

export function agentFileBrowserTabsStorageKey(agentName: string): string {
  return `${FILE_BROWSER_TABS_STORAGE_KEY}:agent:${agentName}`;
}

function tabKey(tab: FileBrowserTab): string {
  return tabIdentityKey(tab);
}

function normalizeTab(raw: unknown): FileBrowserTab | null {
  if (!raw || typeof raw !== "object") return null;
  const item = raw as { ref?: unknown; path?: unknown };
  if (typeof item.path !== "string" || item.path.trim() === "") return null;
  if (!item.ref || typeof item.ref !== "object") return null;
  const ref = item.ref as Partial<ExplorerRef>;
  if (ref.kind === "checkout") {
    const checkout = ref.checkout as FileScopeRef | undefined;
    if (
      !checkout ||
      (checkout.scope !== "workspace" &&
        checkout.scope !== "repo" &&
        checkout.scope !== "agent") ||
      ((checkout.scope === "repo" || checkout.scope === "agent") &&
        !checkout.target)
    ) {
      return null;
    }
  } else if (ref.kind === "skills") {
    const group = ref.group;
    if (
      !group ||
      (group.kind !== "workspace" &&
        (group.kind !== "role" ||
          typeof group.role !== "string" ||
          !!validateRoleName(group.role))) ||
      !parseSkillPath(item.path)
    ) {
      return null;
    }
  } else {
    return null;
  }
  return {
    ref: normalizeExplorerRef(ref as ExplorerRef),
    path: cleanPath(item.path),
  };
}

function normalizeLegacyTab(raw: unknown): FileBrowserTab | null {
  if (!raw || typeof raw !== "object") return null;
  const item = raw as { ref?: unknown; path?: unknown };
  if (typeof item.path !== "string" || item.path.trim() === "") return null;
  if (!item.ref || typeof item.ref !== "object") return null;
  const ref = item.ref as FileScopeRef;
  if (
    (ref.scope !== "workspace" &&
      ref.scope !== "repo" &&
      ref.scope !== "agent") ||
    ((ref.scope === "repo" || ref.scope === "agent") && !ref.target)
  ) {
    return null;
  }
  return {
    ref: checkoutExplorerRef(ref),
    path: cleanPath(item.path),
  };
}

function validRefSet(
  validRefs?: ExplorerRef[] | undefined,
): Set<string> | null {
  if (!validRefs) return null;
  return new Set(validRefs.map((ref) => explorerRefKey(ref)));
}

function coerceTabToValidRef(
  tab: FileBrowserTab,
  validRefs: ExplorerRef[],
): FileBrowserTab {
  const refs = validRefSet(validRefs);
  if (refs?.has(explorerRefKey(tab.ref))) return tab;
  if (
    tab.ref.kind === "checkout" &&
    tab.ref.checkout.scope === "agent" &&
    tab.ref.checkout.target &&
    !tab.ref.checkout.repo
  ) {
    const target = tab.ref.checkout.target;
    const fallback = validRefs.find(
      (ref) =>
        ref.kind === "checkout" &&
        ref.checkout.scope === "agent" &&
        ref.checkout.target === target,
    );
    if (fallback) return { ...tab, ref: fallback };
  }
  return tab;
}

function isValidTab(tab: FileBrowserTab, refs: Set<string> | null): boolean {
  return !refs || refs.has(explorerRefKey(tab.ref));
}

function matchesPathPrefix(
  tab: FileBrowserTab,
  ref: ExplorerRef,
  prefix: string,
): boolean {
  if (!sameExplorerRef(tab.ref, ref)) return false;
  const p = cleanPath(tab.path);
  const base = cleanPath(prefix);
  return p === base || p.startsWith(`${base}/`);
}

function retargetPath(path: string, from: string, to: string): string {
  const p = cleanPath(path);
  const src = cleanPath(from);
  const dst = cleanPath(to);
  if (p === src) return dst;
  if (p.startsWith(`${src}/`)) return `${dst}/${p.slice(src.length + 1)}`;
  return p;
}

function uniqueTabs(
  tabs: FileBrowserTab[],
  refs: Set<string> | null,
): FileBrowserTab[] {
  const seen = new Set<string>();
  const out: FileBrowserTab[] = [];
  for (const tab of tabs) {
    if (!isValidTab(tab, refs)) continue;
    const key = tabKey(tab);
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({ ref: normalizeExplorerRef(tab.ref), path: cleanPath(tab.path) });
  }
  return out;
}

function normalizeGroups(
  rawGroups: unknown,
  refs: Set<string> | null,
  normalizer: (raw: unknown) => FileBrowserTab | null = normalizeTab,
): FileBrowserGroup[] {
  const source = Array.isArray(rawGroups) ? rawGroups : [];
  const next = source.slice(0, 2).map((raw) => {
    const group = raw as { tabs?: unknown; active?: unknown };
    const tabs = uniqueTabs(
      Array.isArray(group.tabs)
        ? group.tabs
            .map(normalizer)
            .filter((tab): tab is FileBrowserTab => tab !== null)
        : [],
      refs,
    );
    const active =
      typeof group.active === "string" &&
      tabs.some((tab) => tabKey(tab) === group.active)
        ? group.active
        : tabs[0]
          ? tabKey(tabs[0])
          : null;
    return { tabs, active };
  });
  if (next.length === 0) {
    next.push({ tabs: [], active: null });
  }
  const second = next[1];
  if (second && second.tabs.length === 0) {
    next.splice(1, 1);
  }
  return next;
}

function normalizeMru(
  rawMru: unknown,
  groups: FileBrowserGroup[],
  refs: Set<string> | null,
  normalizer: (raw: unknown) => FileBrowserTab | null = normalizeTab,
): FileBrowserTab[] {
  const open = new Map<string, FileBrowserTab>();
  for (const group of groups) {
    for (const tab of group.tabs) {
      open.set(tabKey(tab), tab);
    }
  }
  const seen = new Set<string>();
  const out: FileBrowserTab[] = [];
  if (Array.isArray(rawMru)) {
    for (const raw of rawMru) {
      const tab = normalizer(raw);
      if (!tab || !isValidTab(tab, refs)) continue;
      const key = tabKey(tab);
      const openTab = open.get(key);
      if (openTab && !seen.has(key)) {
        seen.add(key);
        out.push(openTab);
      }
    }
  }
  for (const group of groups) {
    if (group.active && open.has(group.active) && !seen.has(group.active)) {
      seen.add(group.active);
      out.push(open.get(group.active)!);
    }
  }
  for (const tab of open.values()) {
    const key = tabKey(tab);
    if (!seen.has(key)) {
      seen.add(key);
      out.push(tab);
    }
  }
  return out;
}

function persistedFromGroups(
  groups: unknown,
  mru: unknown,
  refs: Set<string> | null,
  normalizer: (raw: unknown) => FileBrowserTab | null = normalizeTab,
): PersistedFileBrowserTabsV4 {
  const normalizedGroups = normalizeGroups(groups, refs, normalizer);
  return {
    v: 4,
    groups: normalizedGroups,
    mru: normalizeMru(mru, normalizedGroups, refs, normalizer),
  };
}

function parsePersistedV4(
  raw: string | null,
  refs: Set<string> | null,
): PersistedFileBrowserTabsV4 | null {
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as {
      v?: unknown;
      groups?: unknown;
      mru?: unknown;
    };
    if (parsed?.v === 4) {
      return persistedFromGroups(parsed.groups, parsed.mru, refs);
    }
    if (parsed?.v === 3) {
      return persistedFromGroups(
        parsed.groups,
        parsed.mru,
        refs,
        normalizeLegacyTab,
      );
    }
  } catch {
    return null;
  }
  return null;
}

function loadFileBrowserTabs(
  workspaceId: string,
  validRefs?: ExplorerRef[] | undefined,
  storageKey = FILE_BROWSER_TABS_STORAGE_KEY,
): PersistedFileBrowserTabsV4 {
  const refs = validRefSet(validRefs);
  const loaded = parsePersistedV4(wsGet(workspaceId, storageKey), refs);
  if (loaded) return loaded;
  return EMPTY_FILE_BROWSER_STATE;
}

function persist(
  workspaceId: string,
  storageKey: string,
  state: PersistedFileBrowserTabsV4,
): void {
  wsSet(workspaceId, storageKey, JSON.stringify(state));
}

function touchMru(
  mru: FileBrowserTab[],
  tab: FileBrowserTab,
): FileBrowserTab[] {
  const key = tabKey(tab);
  return [tab, ...mru.filter((item) => tabKey(item) !== key)];
}

function withPersistedState(
  workspaceId: string,
  storageKey: string,
  partial: Omit<PersistedFileBrowserTabsV4, "v">,
): PersistedFileBrowserTabsV4 {
  const persisted = persistedFromGroups(partial.groups, partial.mru, null);
  persist(workspaceId, storageKey, persisted);
  return persisted;
}

export function createFileBrowserStore(
  config: FileBrowserStoreConfig,
): StoreApi<FileBrowserStore> {
  const {
    workspaceId,
    validRefs,
    storageKey = FILE_BROWSER_TABS_STORAGE_KEY,
  } = config;
  const initial = loadFileBrowserTabs(workspaceId, validRefs, storageKey);

  return createStore<FileBrowserStore>((set, get) => ({
    ...initial,
    dirty: {},
    activeGroup: 0,

    openTab: (rawTab, groupIndex = get().activeGroup) => {
      const tab = {
        ref: normalizeExplorerRef(rawTab.ref),
        path: cleanPath(rawTab.path),
      };
      if (!tab.path) return;
      const targetGroup = Math.min(1, Math.max(0, groupIndex));
      const groups = normalizeGroups(get().groups, null);
      while (groups.length <= targetGroup) {
        groups.push({ tabs: [], active: null });
      }
      const group = groups[targetGroup];
      if (!group) return;
      const key = tabKey(tab);
      if (!group.tabs.some((existing) => tabKey(existing) === key)) {
        group.tabs = [...group.tabs, tab];
      }
      group.active = key;
      const persisted = withPersistedState(workspaceId, storageKey, {
        groups,
        mru: touchMru(get().mru, tab),
      });
      set({ ...persisted, activeGroup: targetGroup });
    },

    activateTab: (groupIndex, key) => {
      const groups = normalizeGroups(get().groups, null);
      const group = groups[groupIndex];
      if (!group || !group.tabs.some((tab) => tabKey(tab) === key)) return;
      group.active = key;
      const tab = group.tabs.find((item) => tabKey(item) === key);
      const persisted = withPersistedState(workspaceId, storageKey, {
        groups,
        mru: tab ? touchMru(get().mru, tab) : get().mru,
      });
      set({ ...persisted, activeGroup: groupIndex });
    },

    closeTab: (groupIndex, key) => {
      const groups = normalizeGroups(get().groups, null);
      const group = groups[groupIndex];
      if (!group) return;
      const idx = group.tabs.findIndex((tab) => tabKey(tab) === key);
      if (idx === -1) return;
      const nextTabs = group.tabs.filter((tab) => tabKey(tab) !== key);
      const nextCandidate = nextTabs[idx] ?? nextTabs[idx - 1];
      const nextActive =
        group.active === key && nextCandidate
          ? tabKey(nextCandidate)
          : group.active === key
            ? null
            : group.active;
      groups[groupIndex] = { tabs: nextTabs, active: nextActive };
      const dirty = { ...get().dirty };
      delete dirty[key];
      const persisted = withPersistedState(workspaceId, storageKey, {
        groups,
        mru: get().mru.filter((tab) => tabKey(tab) !== key),
      });
      set({
        ...persisted,
        dirty,
        activeGroup: Math.min(get().activeGroup, persisted.groups.length - 1),
      });
    },

    closePathPrefix: (ref, path) => {
      const groups = normalizeGroups(get().groups, null).map((group) => {
        const tabs = group.tabs.filter(
          (tab) => !matchesPathPrefix(tab, ref, path),
        );
        const active =
          group.active &&
          group.tabs.some(
            (tab) =>
              tabKey(tab) === group.active && matchesPathPrefix(tab, ref, path),
          )
            ? tabs[0]
              ? tabKey(tabs[0])
              : null
            : group.active;
        return { tabs, active };
      });
      const dirty = { ...get().dirty };
      for (const [key, isDirty] of Object.entries(dirty)) {
        if (!isDirty) continue;
        for (const group of get().groups) {
          const tab = group.tabs.find((candidate) => tabKey(candidate) === key);
          if (tab && matchesPathPrefix(tab, ref, path)) delete dirty[key];
        }
      }
      const persisted = withPersistedState(workspaceId, storageKey, {
        groups,
        mru: get().mru.filter((tab) => !matchesPathPrefix(tab, ref, path)),
      });
      set({
        ...persisted,
        dirty,
        activeGroup: Math.min(get().activeGroup, persisted.groups.length - 1),
      });
    },

    splitRight: (explicitTab) => {
      const state = get();
      const groups = normalizeGroups(state.groups, null);
      const sourceTab =
        explicitTab ??
        groups[state.activeGroup]?.tabs.find(
          (tab) => tabKey(tab) === groups[state.activeGroup]?.active,
        ) ??
        groups[0]?.tabs.find((tab) => tabKey(tab) === groups[0]?.active) ??
        null;
      if (!sourceTab) return;
      while (groups.length < 2) {
        groups.push({ tabs: [], active: null });
      }
      const rightGroup = groups[1];
      if (!rightGroup) return;
      const key = tabKey(sourceTab);
      if (!rightGroup.tabs.some((tab) => tabKey(tab) === key)) {
        rightGroup.tabs = [...rightGroup.tabs, sourceTab];
      }
      rightGroup.active = key;
      const persisted = withPersistedState(workspaceId, storageKey, {
        groups,
        mru: touchMru(state.mru, sourceTab),
      });
      set({ ...persisted, activeGroup: 1 });
    },

    setDirty: (key, isDirty) => {
      const dirty = { ...get().dirty };
      if (isDirty) dirty[key] = true;
      else delete dirty[key];
      set({ dirty });
    },

    retargetPathPrefix: (ref, from, to) => {
      const state = get();
      const groups = normalizeGroups(state.groups, null).map((group) => {
        const tabs = uniqueTabs(
          group.tabs.map((tab) =>
            sameExplorerRef(tab.ref, ref) && matchesPathPrefix(tab, ref, from)
              ? { ...tab, path: retargetPath(tab.path, from, to) }
              : tab,
          ),
          null,
        );
        const activeTab = tabs.find((tab) =>
          group.active
            ? tabKey(tab) === group.active ||
              (sameExplorerRef(tab.ref, ref) &&
                matchesPathPrefix(tab, ref, to) &&
                tabKey({ ...tab, path: retargetPath(tab.path, to, from) }) ===
                  group.active)
            : false,
        );
        return {
          tabs,
          active: activeTab
            ? tabKey(activeTab)
            : tabs[0]
              ? tabKey(tabs[0])
              : null,
        };
      });
      const oldToNewKeys = new Map<string, string>();
      for (const group of state.groups) {
        for (const tab of group.tabs) {
          if (
            sameExplorerRef(tab.ref, ref) &&
            matchesPathPrefix(tab, ref, from)
          ) {
            const next = { ...tab, path: retargetPath(tab.path, from, to) };
            oldToNewKeys.set(tabKey(tab), tabKey(next));
          }
        }
      }
      const dirty: Record<string, boolean> = {};
      for (const [key, isDirty] of Object.entries(state.dirty)) {
        dirty[oldToNewKeys.get(key) ?? key] = isDirty;
      }
      const persisted = withPersistedState(workspaceId, storageKey, {
        groups,
        mru: state.mru.map((tab) =>
          sameExplorerRef(tab.ref, ref) && matchesPathPrefix(tab, ref, from)
            ? { ...tab, path: retargetPath(tab.path, from, to) }
            : tab,
        ),
      });
      set({ ...persisted, dirty });
    },

    pruneUnavailableRefs: (validRefs) => {
      const refs = validRefSet(validRefs);
      const state = get();
      const groups = state.groups.map((group) => ({
        ...group,
        tabs: group.tabs.map((tab) => coerceTabToValidRef(tab, validRefs)),
      }));
      const mru = state.mru.map((tab) => coerceTabToValidRef(tab, validRefs));
      const persisted = persistedFromGroups(groups, mru, refs);
      const open = new Set(
        persisted.groups.flatMap((group) =>
          group.tabs.map((tab) => tabKey(tab)),
        ),
      );
      const dirty: Record<string, boolean> = {};
      for (const [key, isDirty] of Object.entries(state.dirty)) {
        if (open.has(key)) dirty[key] = isDirty;
      }
      persist(workspaceId, storageKey, persisted);
      set({
        ...persisted,
        dirty,
        activeGroup: Math.min(state.activeGroup, persisted.groups.length - 1),
      });
    },

    reset: () => {
      persist(workspaceId, storageKey, EMPTY_FILE_BROWSER_STATE);
      set({ ...EMPTY_FILE_BROWSER_STATE, dirty: {}, activeGroup: 0 });
    },
  }));
}

const FileBrowserStoreContext = createContext<
  StoreApi<FileBrowserStore> | undefined
>(undefined);

export interface FileBrowserStoreProviderProps extends FileBrowserStoreConfig {
  children: ReactNode;
}

export function FileBrowserStoreProvider({
  workspaceId,
  validRefs,
  storageKey,
  children,
}: FileBrowserStoreProviderProps): JSX.Element {
  const [store] = useState(() =>
    createFileBrowserStore({ workspaceId, validRefs, storageKey }),
  );

  useEffect(() => {
    if (validRefs) {
      store.getState().pruneUnavailableRefs(validRefs);
    }
  }, [store, validRefs]);

  return (
    <FileBrowserStoreContext.Provider value={store}>
      {children}
    </FileBrowserStoreContext.Provider>
  );
}

export function useFileBrowserStoreInstance(): StoreApi<FileBrowserStore> {
  const store = useContext(FileBrowserStoreContext);
  if (!store) {
    throw new Error(
      "useFileBrowserStoreInstance must be used within FileBrowserStoreProvider",
    );
  }
  return store;
}

export function useFileBrowserStore<T>(
  selector: (state: FileBrowserStore) => T,
): T {
  return useStore(useFileBrowserStoreInstance(), selector);
}
