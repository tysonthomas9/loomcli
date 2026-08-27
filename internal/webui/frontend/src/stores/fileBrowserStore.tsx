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
  /**
   * Close every tab whose ref no longer exists, and persist that.
   *
   * `refsWorkspaceId` names the workspace `validRefs` was derived from. The
   * caller's own sources of truth switch at different times — the route's
   * workspace id changes the instant the user switches, while the agent and
   * repo lists behind the ref universe still describe the workspace they just
   * left — so a universe is only trustworthy once it says which workspace it
   * belongs to and that answer matches this store's. Anything else (a mismatch,
   * or a caller that cannot say) is treated as "not known yet" and prunes
   * nothing: a destructive, persisted close is not a safe default.
   */
  pruneUnavailableRefs: (
    validRefs: ExplorerRef[],
    refsWorkspaceId: string | null | undefined,
  ) => void;
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

// The Skills section keeps its own tab set: it is a separate destination in the
// nav rail, so a file opened there must not appear in the Files section's tabs
// (or be pruned by it) just because both browsers share a store shape.
export function skillsFileBrowserTabsStorageKey(): string {
  return `${FILE_BROWSER_TABS_STORAGE_KEY}:skills`;
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

/**
 * Which tabs survive a rebuild of the persisted state. `refs` is the universe
 * of explorer refs that currently exist; `retain` holds tab keys that survive
 * whether or not their ref is in that universe. A null filter accepts every
 * tab — the shape used when a caller is normalizing rather than pruning.
 */
interface TabFilter {
  refs: Set<string>;
  retain: Set<string>;
}

function refKeySet(validRefs: ExplorerRef[]): Set<string> {
  return new Set(validRefs.map((ref) => explorerRefKey(ref)));
}

function tabFilter(
  validRefs?: ExplorerRef[] | undefined,
  retain: Iterable<string> = [],
): TabFilter | null {
  if (!validRefs) return null;
  return { refs: refKeySet(validRefs), retain: new Set(retain) };
}

function coerceTabToValidRef(
  tab: FileBrowserTab,
  validRefs: ExplorerRef[],
): FileBrowserTab {
  if (refKeySet(validRefs).has(explorerRefKey(tab.ref))) return tab;
  if (
    tab.ref.kind !== "checkout" ||
    tab.ref.checkout.scope !== "agent" ||
    !tab.ref.checkout.target ||
    tab.ref.checkout.repo
  ) {
    return tab;
  }
  // A repo-less agent ref is a tab saved while the agent had a single,
  // flattened checkout. Re-homing it is only unambiguous when the agent still
  // has exactly one: with two or more, "whichever comes first" silently points
  // the path the user opened at a different repo's file, and every tie-break
  // rule picks a wrong file just as confidently. One candidate coerces; more
  // than one leaves the tab alone, which closes it — honest, and recoverable
  // by reopening from the tree.
  const target = tab.ref.checkout.target;
  const candidates = validRefs.filter(
    (ref) =>
      ref.kind === "checkout" &&
      ref.checkout.scope === "agent" &&
      ref.checkout.target === target,
  );
  const only = candidates.length === 1 ? candidates[0] : undefined;
  return only ? { ...tab, ref: only } : tab;
}

function isValidTab(tab: FileBrowserTab, filter: TabFilter | null): boolean {
  if (!filter) return true;
  return (
    filter.retain.has(tabKey(tab)) || filter.refs.has(explorerRefKey(tab.ref))
  );
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
  filter: TabFilter | null,
): FileBrowserTab[] {
  const seen = new Set<string>();
  const out: FileBrowserTab[] = [];
  for (const tab of tabs) {
    if (!isValidTab(tab, filter)) continue;
    const key = tabKey(tab);
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({ ref: normalizeExplorerRef(tab.ref), path: cleanPath(tab.path) });
  }
  return out;
}

function normalizeGroups(
  rawGroups: unknown,
  filter: TabFilter | null,
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
      filter,
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
  filter: TabFilter | null,
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
      if (!tab || !isValidTab(tab, filter)) continue;
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
  filter: TabFilter | null,
  normalizer: (raw: unknown) => FileBrowserTab | null = normalizeTab,
): PersistedFileBrowserTabsV4 {
  const normalizedGroups = normalizeGroups(groups, filter, normalizer);
  return {
    v: 4,
    groups: normalizedGroups,
    mru: normalizeMru(mru, normalizedGroups, filter, normalizer),
  };
}

function parsePersistedV4(
  raw: string | null,
  filter: TabFilter | null,
): PersistedFileBrowserTabsV4 | null {
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as {
      v?: unknown;
      groups?: unknown;
      mru?: unknown;
    };
    if (parsed?.v === 4) {
      return persistedFromGroups(parsed.groups, parsed.mru, filter);
    }
    if (parsed?.v === 3) {
      return persistedFromGroups(
        parsed.groups,
        parsed.mru,
        filter,
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
  const loaded = parsePersistedV4(
    wsGet(workspaceId, storageKey),
    tabFilter(validRefs),
  );
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

    pruneUnavailableRefs: (validRefs, refsWorkspaceId) => {
      if (!refsWorkspaceId || refsWorkspaceId !== workspaceId) return;
      const state = get();
      // A tab holding unsaved edits is user data, and a ref being absent from
      // the universe is usually transient — a checkout mid-rebuild, a role
      // scope the catalog has not listed yet. Closing such a tab would discard
      // the draft with no warning and persist that, so dirty tabs survive
      // pruning. They are recognised by tabKey, the same key `dirty` is keyed
      // by, and they are also left un-coerced: retargeting a tab's ref changes
      // its identity, which would strand the draft the editor holds under the
      // old one. Once the edits are saved the tab is ordinary again and the
      // next prune closes it if the ref is still gone.
      const dirtyKeys = new Set(
        Object.entries(state.dirty)
          .filter(([, isDirty]) => isDirty)
          .map(([key]) => key),
      );
      const settle = (tab: FileBrowserTab): FileBrowserTab =>
        dirtyKeys.has(tabKey(tab)) ? tab : coerceTabToValidRef(tab, validRefs);
      const groups = state.groups.map((group) => ({
        ...group,
        tabs: group.tabs.map(settle),
      }));
      const mru = state.mru.map(settle);
      const persisted = persistedFromGroups(
        groups,
        mru,
        tabFilter(validRefs, dirtyKeys),
      );
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
      store.getState().pruneUnavailableRefs(validRefs, workspaceId);
    }
  }, [store, validRefs, workspaceId]);

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
