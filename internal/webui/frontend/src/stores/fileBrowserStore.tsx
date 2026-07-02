/**
 * Zustand vanilla store for File Browser v2 tab state.
 * One store instance is created per (workspace, scope, target) browser.
 */

import { createContext, useContext, useState, type ReactNode } from "react";
import { useStore } from "zustand";
import { createStore, type StoreApi } from "zustand/vanilla";

import type { FileScopeRef } from "@/api/workspace";
import { wsGet, wsSet } from "@/utils/scopedStorage";

const LEGACY_TABS_STORAGE_KEY = "file-browser-tabs";

export interface FileBrowserTab {
  path: string;
}

export interface FileBrowserGroup {
  tabs: FileBrowserTab[];
  active: string | null;
}

export interface PersistedFileBrowserTabsV2 {
  v: 2;
  groups: FileBrowserGroup[];
  mru: string[];
}

export interface FileBrowserStoreState extends PersistedFileBrowserTabsV2 {
  dirty: Record<string, boolean>;
  activeGroup: number;
}

export interface FileBrowserStoreActions {
  openTab: (path: string, groupIndex?: number) => void;
  activateTab: (groupIndex: number, path: string) => void;
  closeTab: (groupIndex: number, path: string) => void;
  closePathPrefix: (path: string) => void;
  splitRight: (path?: string | null) => void;
  setDirty: (path: string, dirty: boolean) => void;
  retargetPathPrefix: (from: string, to: string) => void;
  reset: () => void;
}

export type FileBrowserStore = FileBrowserStoreState & FileBrowserStoreActions;

export interface FileBrowserStoreConfig {
  workspaceId: string;
  scopeRef: FileScopeRef;
}

export const EMPTY_FILE_BROWSER_STATE: PersistedFileBrowserTabsV2 = {
  v: 2,
  groups: [{ tabs: [], active: null }],
  mru: [],
};

function normalizeTarget(target?: string | null): string {
  return target && target.trim() ? target.trim() : "root";
}

export function fileBrowserTabsStorageKey(scopeRef: FileScopeRef): string {
  return `file-browser-tabs:v2:${scopeRef.scope}:${normalizeTarget(scopeRef.target)}`;
}

function cleanPath(path: string): string {
  return path.replace(/^\/+|\/+$/g, "");
}

function matchesPathPrefix(path: string, prefix: string): boolean {
  const p = cleanPath(path);
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

function uniqueTabs(tabs: FileBrowserTab[]): FileBrowserTab[] {
  const seen = new Set<string>();
  const out: FileBrowserTab[] = [];
  for (const tab of tabs) {
    if (typeof tab.path !== "string" || tab.path === "" || seen.has(tab.path)) {
      continue;
    }
    seen.add(tab.path);
    out.push({ path: tab.path });
  }
  return out;
}

function normalizeGroups(groups: FileBrowserGroup[]): FileBrowserGroup[] {
  const next = groups.slice(0, 2).map((group) => {
    const tabs = uniqueTabs(group.tabs ?? []);
    const active =
      typeof group.active === "string" &&
      tabs.some((t) => t.path === group.active)
        ? group.active
        : (tabs[0]?.path ?? null);
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

function normalizeMru(mru: unknown, groups: FileBrowserGroup[]): string[] {
  const open = new Set(
    groups.flatMap((group) => group.tabs.map((tab) => tab.path)),
  );
  const seen = new Set<string>();
  const out: string[] = [];
  if (Array.isArray(mru)) {
    for (const item of mru) {
      if (typeof item === "string" && open.has(item) && !seen.has(item)) {
        seen.add(item);
        out.push(item);
      }
    }
  }
  for (const group of groups) {
    if (group.active && open.has(group.active) && !seen.has(group.active)) {
      seen.add(group.active);
      out.push(group.active);
    }
  }
  for (const group of groups) {
    for (const tab of group.tabs) {
      if (!seen.has(tab.path)) {
        seen.add(tab.path);
        out.push(tab.path);
      }
    }
  }
  return out;
}

function persistedFromGroups(
  groups: FileBrowserGroup[],
  mru: unknown,
): PersistedFileBrowserTabsV2 {
  const normalizedGroups = normalizeGroups(groups);
  return {
    v: 2,
    groups: normalizedGroups,
    mru: normalizeMru(mru, normalizedGroups),
  };
}

function parsePersisted(raw: string | null): PersistedFileBrowserTabsV2 | null {
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as unknown;
    if (
      parsed &&
      typeof parsed === "object" &&
      (parsed as { v?: unknown }).v === 2 &&
      Array.isArray((parsed as { groups?: unknown }).groups)
    ) {
      const source = parsed as { groups: FileBrowserGroup[]; mru?: unknown };
      return persistedFromGroups(source.groups, source.mru);
    }
  } catch {
    return null;
  }
  return null;
}

function migrateLegacy(raw: string | null): PersistedFileBrowserTabsV2 | null {
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as {
      openTabs?: unknown;
      activePath?: unknown;
    };
    if (!Array.isArray(parsed.openTabs)) return null;
    const tabs = parsed.openTabs
      .filter((path): path is string => typeof path === "string" && path !== "")
      .map((path) => ({ path }));
    const active =
      typeof parsed.activePath === "string" &&
      tabs.some((tab) => tab.path === parsed.activePath)
        ? parsed.activePath
        : (tabs[0]?.path ?? null);
    return persistedFromGroups([{ tabs, active }], active ? [active] : []);
  } catch {
    return null;
  }
}

export function loadFileBrowserTabs(
  workspaceId: string,
  scopeRef: FileScopeRef,
): PersistedFileBrowserTabsV2 {
  const key = fileBrowserTabsStorageKey(scopeRef);
  const loaded = parsePersisted(wsGet(workspaceId, key));
  if (loaded) return loaded;

  const migrated =
    scopeRef.scope === "workspace" && !scopeRef.target
      ? migrateLegacy(wsGet(workspaceId, LEGACY_TABS_STORAGE_KEY))
      : null;
  if (migrated) {
    wsSet(workspaceId, key, JSON.stringify(migrated));
    return migrated;
  }
  return EMPTY_FILE_BROWSER_STATE;
}

function persist(
  workspaceId: string,
  scopeRef: FileScopeRef,
  state: PersistedFileBrowserTabsV2,
): void {
  wsSet(
    workspaceId,
    fileBrowserTabsStorageKey(scopeRef),
    JSON.stringify(state),
  );
}

function touchMru(mru: string[], path: string): string[] {
  return [path, ...mru.filter((p) => p !== path)];
}

function withPersistedState(
  workspaceId: string,
  scopeRef: FileScopeRef,
  partial: Omit<PersistedFileBrowserTabsV2, "v">,
): PersistedFileBrowserTabsV2 {
  const persisted = persistedFromGroups(partial.groups, partial.mru);
  persist(workspaceId, scopeRef, persisted);
  return persisted;
}

export function createFileBrowserStore(
  config: FileBrowserStoreConfig,
): StoreApi<FileBrowserStore> {
  const { workspaceId, scopeRef } = config;
  const initial = loadFileBrowserTabs(workspaceId, scopeRef);

  return createStore<FileBrowserStore>((set, get) => ({
    ...initial,
    dirty: {},
    activeGroup: 0,

    openTab: (path, groupIndex = get().activeGroup) => {
      const targetGroup = Math.min(1, Math.max(0, groupIndex));
      const groups = normalizeGroups(get().groups);
      while (groups.length <= targetGroup) {
        groups.push({ tabs: [], active: null });
      }
      const group = groups[targetGroup];
      if (!group) return;
      if (!group.tabs.some((tab) => tab.path === path)) {
        group.tabs = [...group.tabs, { path }];
      }
      group.active = path;
      const persisted = withPersistedState(workspaceId, scopeRef, {
        groups,
        mru: touchMru(get().mru, path),
      });
      set({ ...persisted, activeGroup: targetGroup });
    },

    activateTab: (groupIndex, path) => {
      const groups = normalizeGroups(get().groups);
      const group = groups[groupIndex];
      if (!group || !group.tabs.some((tab) => tab.path === path)) return;
      group.active = path;
      const persisted = withPersistedState(workspaceId, scopeRef, {
        groups,
        mru: touchMru(get().mru, path),
      });
      set({ ...persisted, activeGroup: groupIndex });
    },

    closeTab: (groupIndex, path) => {
      const groups = normalizeGroups(get().groups);
      const group = groups[groupIndex];
      if (!group) return;
      const idx = group.tabs.findIndex((tab) => tab.path === path);
      if (idx === -1) return;
      const nextTabs = group.tabs.filter((tab) => tab.path !== path);
      const nextActive =
        group.active === path
          ? (nextTabs[idx]?.path ?? nextTabs[idx - 1]?.path ?? null)
          : group.active;
      groups[groupIndex] = { tabs: nextTabs, active: nextActive };
      const dirty = { ...get().dirty };
      delete dirty[path];
      const persisted = withPersistedState(workspaceId, scopeRef, {
        groups,
        mru: get().mru.filter((p) => p !== path),
      });
      set({
        ...persisted,
        dirty,
        activeGroup: Math.min(get().activeGroup, persisted.groups.length - 1),
      });
    },

    closePathPrefix: (path) => {
      const groups = normalizeGroups(get().groups).map((group) => {
        const tabs = group.tabs.filter(
          (tab) => !matchesPathPrefix(tab.path, path),
        );
        const active =
          group.active && matchesPathPrefix(group.active, path)
            ? (tabs[0]?.path ?? null)
            : group.active;
        return { tabs, active };
      });
      const dirty = { ...get().dirty };
      for (const key of Object.keys(dirty)) {
        if (matchesPathPrefix(key, path)) delete dirty[key];
      }
      const persisted = withPersistedState(workspaceId, scopeRef, {
        groups,
        mru: get().mru.filter((p) => !matchesPathPrefix(p, path)),
      });
      set({
        ...persisted,
        dirty,
        activeGroup: Math.min(get().activeGroup, persisted.groups.length - 1),
      });
    },

    splitRight: (explicitPath) => {
      const state = get();
      const groups = normalizeGroups(state.groups);
      const sourcePath =
        explicitPath ??
        groups[state.activeGroup]?.active ??
        groups[0]?.active ??
        null;
      if (!sourcePath) return;
      while (groups.length < 2) {
        groups.push({ tabs: [], active: null });
      }
      const rightGroup = groups[1];
      if (!rightGroup) return;
      if (!rightGroup.tabs.some((tab) => tab.path === sourcePath)) {
        rightGroup.tabs = [...rightGroup.tabs, { path: sourcePath }];
      }
      rightGroup.active = sourcePath;
      const persisted = withPersistedState(workspaceId, scopeRef, {
        groups,
        mru: touchMru(state.mru, sourcePath),
      });
      set({ ...persisted, activeGroup: 1 });
    },

    setDirty: (path, isDirty) => {
      const dirty = { ...get().dirty };
      if (isDirty) dirty[path] = true;
      else delete dirty[path];
      set({ dirty });
    },

    retargetPathPrefix: (from, to) => {
      const state = get();
      const groups = normalizeGroups(state.groups).map((group) => {
        const tabs = uniqueTabs(
          group.tabs.map((tab) => ({
            path: retargetPath(tab.path, from, to),
          })),
        );
        const active = group.active
          ? retargetPath(group.active, from, to)
          : null;
        return {
          tabs,
          active: tabs.some((tab) => tab.path === active)
            ? active
            : (tabs[0]?.path ?? null),
        };
      });
      const dirty: Record<string, boolean> = {};
      for (const [path, isDirty] of Object.entries(state.dirty)) {
        dirty[retargetPath(path, from, to)] = isDirty;
      }
      const persisted = withPersistedState(workspaceId, scopeRef, {
        groups,
        mru: state.mru.map((path) => retargetPath(path, from, to)),
      });
      set({ ...persisted, dirty });
    },

    reset: () => {
      persist(workspaceId, scopeRef, EMPTY_FILE_BROWSER_STATE);
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
  scopeRef,
  children,
}: FileBrowserStoreProviderProps): JSX.Element {
  const [store] = useState(() =>
    createFileBrowserStore({ workspaceId, scopeRef }),
  );
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
