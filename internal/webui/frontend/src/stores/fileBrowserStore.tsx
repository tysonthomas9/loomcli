/**
 * Zustand vanilla store for File Browser v3 tab state.
 * One store instance is created per workspace; each tab carries its checkout ref.
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
import { wsGet, wsSet, wsKey } from "@/utils/scopedStorage";
import {
  checkoutRefKey,
  cleanPath,
  legacyScopeStorageKey,
  normalizeCheckoutRef,
  sameCheckoutRef,
  tabIdentityKey,
  type CheckoutRef,
} from "@/utils/fileExplorerRefs";

const LEGACY_TABS_STORAGE_KEY = "file-browser-tabs";
const FILE_BROWSER_TABS_STORAGE_KEY = "file-browser-tabs:v3";

export interface FileBrowserTab {
  ref: CheckoutRef;
  path: string;
}

export interface FileBrowserGroup {
  tabs: FileBrowserTab[];
  /** Stable tab key produced by tabIdentityKey(tab). */
  active: string | null;
}

export interface PersistedFileBrowserTabsV2 {
  v: 2;
  groups: Array<{
    tabs: Array<{ path: string }>;
    active: string | null;
  }>;
  mru: string[];
}

export interface PersistedFileBrowserTabsV3 {
  v: 3;
  groups: FileBrowserGroup[];
  mru: FileBrowserTab[];
}

export interface FileBrowserStoreState extends PersistedFileBrowserTabsV3 {
  dirty: Record<string, boolean>;
  activeGroup: number;
}

export interface FileBrowserStoreActions {
  openTab: (tab: FileBrowserTab, groupIndex?: number) => void;
  activateTab: (groupIndex: number, tabKey: string) => void;
  closeTab: (groupIndex: number, tabKey: string) => void;
  closePathPrefix: (ref: CheckoutRef, path: string) => void;
  splitRight: (tab?: FileBrowserTab | null) => void;
  setDirty: (tabKey: string, dirty: boolean) => void;
  retargetPathPrefix: (ref: CheckoutRef, from: string, to: string) => void;
  pruneUnavailableRefs: (validRefs: CheckoutRef[]) => void;
  reset: () => void;
}

export type FileBrowserStore = FileBrowserStoreState & FileBrowserStoreActions;

export interface FileBrowserStoreConfig {
  workspaceId: string;
  validRefs?: CheckoutRef[] | undefined;
}

export const EMPTY_FILE_BROWSER_STATE: PersistedFileBrowserTabsV3 = {
  v: 3,
  groups: [{ tabs: [], active: null }],
  mru: [],
};

export function fileBrowserTabsStorageKey(): string {
  return FILE_BROWSER_TABS_STORAGE_KEY;
}

function tabKey(tab: FileBrowserTab): string {
  return tabIdentityKey(tab);
}

function normalizeTab(raw: unknown): FileBrowserTab | null {
  if (!raw || typeof raw !== "object") return null;
  const item = raw as { ref?: unknown; path?: unknown };
  if (typeof item.path !== "string" || item.path.trim() === "") return null;
  if (!item.ref || typeof item.ref !== "object") return null;
  const ref = item.ref as FileScopeRef;
  if (
    ref.scope !== "workspace" &&
    ref.scope !== "repo" &&
    ref.scope !== "agent"
  ) {
    return null;
  }
  if ((ref.scope === "repo" || ref.scope === "agent") && !ref.target) {
    return null;
  }
  return {
    ref: normalizeCheckoutRef(ref),
    path: cleanPath(item.path),
  };
}

function validRefSet(
  validRefs?: CheckoutRef[] | undefined,
): Set<string> | null {
  if (!validRefs) return null;
  return new Set(validRefs.map((ref) => checkoutRefKey(ref)));
}

function coerceTabToValidRef(
  tab: FileBrowserTab,
  validRefs: CheckoutRef[],
): FileBrowserTab {
  const refs = validRefSet(validRefs);
  if (refs?.has(checkoutRefKey(tab.ref))) return tab;
  if (tab.ref.scope === "agent" && tab.ref.target && !tab.ref.repo) {
    const fallback = validRefs.find(
      (ref) => ref.scope === "agent" && ref.target === tab.ref.target,
    );
    if (fallback) return { ...tab, ref: fallback };
  }
  return tab;
}

function isValidTab(tab: FileBrowserTab, refs: Set<string> | null): boolean {
  return !refs || refs.has(checkoutRefKey(tab.ref));
}

function matchesPathPrefix(
  tab: FileBrowserTab,
  ref: CheckoutRef,
  prefix: string,
): boolean {
  if (!sameCheckoutRef(tab.ref, ref)) return false;
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
    out.push({ ref: normalizeCheckoutRef(tab.ref), path: cleanPath(tab.path) });
  }
  return out;
}

function normalizeGroups(
  rawGroups: unknown,
  refs: Set<string> | null,
): FileBrowserGroup[] {
  const source = Array.isArray(rawGroups) ? rawGroups : [];
  const next = source.slice(0, 2).map((raw) => {
    const group = raw as { tabs?: unknown; active?: unknown };
    const tabs = uniqueTabs(
      Array.isArray(group.tabs)
        ? group.tabs
            .map(normalizeTab)
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
      const tab = normalizeTab(raw);
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
): PersistedFileBrowserTabsV3 {
  const normalizedGroups = normalizeGroups(groups, refs);
  return {
    v: 3,
    groups: normalizedGroups,
    mru: normalizeMru(mru, normalizedGroups, refs),
  };
}

function parsePersistedV3(
  raw: string | null,
  refs: Set<string> | null,
): PersistedFileBrowserTabsV3 | null {
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as {
      v?: unknown;
      groups?: unknown;
      mru?: unknown;
    };
    if (parsed?.v === 3) {
      return persistedFromGroups(parsed.groups, parsed.mru, refs);
    }
  } catch {
    return null;
  }
  return null;
}

function legacyTab(ref: CheckoutRef, path: string): FileBrowserTab {
  return { ref: normalizeCheckoutRef(ref), path: cleanPath(path) };
}

function parsePersistedV2(
  raw: string | null,
  ref: CheckoutRef,
): PersistedFileBrowserTabsV3 | null {
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as {
      v?: unknown;
      groups?: unknown;
      mru?: unknown;
    };
    if (parsed?.v !== 2 || !Array.isArray(parsed.groups)) return null;
    const groups = parsed.groups.slice(0, 2).map((group) => {
      const g = group as { tabs?: unknown; active?: unknown };
      const tabs = Array.isArray(g.tabs)
        ? g.tabs
            .map((tab) =>
              tab &&
              typeof tab === "object" &&
              typeof (tab as { path?: unknown }).path === "string"
                ? legacyTab(ref, (tab as { path: string }).path)
                : null,
            )
            .filter((tab): tab is FileBrowserTab => tab !== null)
        : [];
      const activePath = typeof g.active === "string" ? g.active : null;
      const activeTab = activePath
        ? tabs.find((tab) => tab.path === cleanPath(activePath))
        : undefined;
      return {
        tabs,
        active: activeTab
          ? tabKey(activeTab)
          : tabs[0]
            ? tabKey(tabs[0])
            : null,
      };
    });
    const mru = Array.isArray(parsed.mru)
      ? parsed.mru
          .filter((path): path is string => typeof path === "string")
          .map((path) => legacyTab(ref, path))
      : [];
    return persistedFromGroups(groups, mru, null);
  } catch {
    return null;
  }
}

function migrateLegacyV1(
  raw: string | null,
): PersistedFileBrowserTabsV3 | null {
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as {
      openTabs?: unknown;
      activePath?: unknown;
    };
    if (!Array.isArray(parsed.openTabs)) return null;
    const ref: CheckoutRef = { scope: "workspace" };
    const tabs = parsed.openTabs
      .filter(
        (path): path is string =>
          typeof path === "string" && path.trim() !== "",
      )
      .map((path) => legacyTab(ref, path));
    const activeTab =
      typeof parsed.activePath === "string"
        ? tabs.find(
            (tab) => tab.path === cleanPath(parsed.activePath as string),
          )
        : undefined;
    return persistedFromGroups(
      [
        {
          tabs,
          active: activeTab
            ? tabKey(activeTab)
            : tabs[0]
              ? tabKey(tabs[0])
              : null,
        },
      ],
      activeTab ? [activeTab] : [],
      null,
    );
  } catch {
    return null;
  }
}

function refFromLegacyKey(key: string): CheckoutRef | null {
  const prefix = "file-browser-tabs:v2:";
  if (!key.startsWith(prefix)) return null;
  const rest = key.slice(prefix.length);
  const [scope, target] = rest.split(":");
  if (scope !== "workspace" && scope !== "repo" && scope !== "agent")
    return null;
  if (scope === "workspace") return { scope };
  if (!target || target === "root") return null;
  return { scope, target };
}

function findLegacyV2Refs(workspaceId: string): CheckoutRef[] {
  const prefix = wsKey(workspaceId, "file-browser-tabs:v2:");
  const refs: CheckoutRef[] = [];
  try {
    for (let i = 0; i < localStorage.length; i += 1) {
      const fullKey = localStorage.key(i);
      if (!fullKey?.startsWith(prefix)) continue;
      const key = fullKey.slice(`loom:${workspaceId}:`.length);
      const ref = refFromLegacyKey(key);
      if (ref) refs.push(ref);
    }
  } catch {
    return refs;
  }
  return refs;
}

function mergePersistedStates(
  states: PersistedFileBrowserTabsV3[],
  refs: Set<string> | null,
): PersistedFileBrowserTabsV3 | null {
  if (states.length === 0) return null;
  const groups: FileBrowserGroup[] = [{ tabs: [], active: null }];
  const mru: FileBrowserTab[] = [];
  for (const state of states) {
    state.groups.forEach((group, index) => {
      while (groups.length <= index) groups.push({ tabs: [], active: null });
      groups[index]!.tabs.push(...group.tabs);
      if (!groups[index]!.active) groups[index]!.active = group.active;
    });
    mru.push(...state.mru);
  }
  return persistedFromGroups(groups, mru, refs);
}

export function loadFileBrowserTabs(
  workspaceId: string,
  validRefs?: CheckoutRef[] | undefined,
): PersistedFileBrowserTabsV3 {
  const refs = validRefSet(validRefs);
  const loaded = parsePersistedV3(
    wsGet(workspaceId, FILE_BROWSER_TABS_STORAGE_KEY),
    refs,
  );
  if (loaded) return loaded;

  const migrated: PersistedFileBrowserTabsV3[] = [];
  for (const ref of findLegacyV2Refs(workspaceId)) {
    const state = parsePersistedV2(
      wsGet(workspaceId, legacyScopeStorageKey(ref)),
      ref,
    );
    if (state) migrated.push(state);
  }
  const legacyV1 = migrateLegacyV1(wsGet(workspaceId, LEGACY_TABS_STORAGE_KEY));
  if (legacyV1) migrated.push(legacyV1);

  const folded = mergePersistedStates(migrated, refs);
  if (folded) {
    persist(workspaceId, folded);
    return folded;
  }
  return EMPTY_FILE_BROWSER_STATE;
}

function persist(workspaceId: string, state: PersistedFileBrowserTabsV3): void {
  wsSet(workspaceId, FILE_BROWSER_TABS_STORAGE_KEY, JSON.stringify(state));
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
  partial: Omit<PersistedFileBrowserTabsV3, "v">,
): PersistedFileBrowserTabsV3 {
  const persisted = persistedFromGroups(partial.groups, partial.mru, null);
  persist(workspaceId, persisted);
  return persisted;
}

export function createFileBrowserStore(
  config: FileBrowserStoreConfig,
): StoreApi<FileBrowserStore> {
  const { workspaceId, validRefs } = config;
  const initial = loadFileBrowserTabs(workspaceId, validRefs);

  return createStore<FileBrowserStore>((set, get) => ({
    ...initial,
    dirty: {},
    activeGroup: 0,

    openTab: (rawTab, groupIndex = get().activeGroup) => {
      const tab = {
        ref: normalizeCheckoutRef(rawTab.ref),
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
      const persisted = withPersistedState(workspaceId, {
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
      const persisted = withPersistedState(workspaceId, {
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
      const persisted = withPersistedState(workspaceId, {
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
      const persisted = withPersistedState(workspaceId, {
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
      const persisted = withPersistedState(workspaceId, {
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
            sameCheckoutRef(tab.ref, ref) && matchesPathPrefix(tab, ref, from)
              ? { ...tab, path: retargetPath(tab.path, from, to) }
              : tab,
          ),
          null,
        );
        const activeTab = tabs.find((tab) =>
          group.active
            ? tabKey(tab) === group.active ||
              (sameCheckoutRef(tab.ref, ref) &&
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
            sameCheckoutRef(tab.ref, ref) &&
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
      const persisted = withPersistedState(workspaceId, {
        groups,
        mru: state.mru.map((tab) =>
          sameCheckoutRef(tab.ref, ref) && matchesPathPrefix(tab, ref, from)
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
      persist(workspaceId, persisted);
      set({
        ...persisted,
        dirty,
        activeGroup: Math.min(state.activeGroup, persisted.groups.length - 1),
      });
    },

    reset: () => {
      persist(workspaceId, EMPTY_FILE_BROWSER_STATE);
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
  children,
}: FileBrowserStoreProviderProps): JSX.Element {
  const [store] = useState(() =>
    createFileBrowserStore({ workspaceId, validRefs }),
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
