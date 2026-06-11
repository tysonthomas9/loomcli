/**
 * VS Code-style editor groups for terminal tabs.
 * Split moves the active tab into a new right column (matches AgentEditorGroups).
 */

import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type DragEvent,
} from "react";

export type TabEditorGroup = {
  tabIds: string[];
  activeTabId: string;
};

function singleGroup(tabIds: string[], activeTabId: string): TabEditorGroup[] {
  if (tabIds.length === 0) return [];
  const active = tabIds.includes(activeTabId) ? activeTabId : tabIds[0]!;
  return [{ tabIds: [...tabIds], activeTabId: active }];
}

export function reconcileTabEditorGroups(
  groups: TabEditorGroup[],
  tabIds: string[],
  fallbackActiveId: string,
): TabEditorGroup[] {
  if (tabIds.length === 0) return [];

  const idSet = new Set(tabIds);
  let next = groups
    .map((group) => {
      const ids = group.tabIds.filter((id) => idSet.has(id));
      const activeTabId = ids.includes(group.activeTabId)
        ? group.activeTabId
        : (ids[0] ?? fallbackActiveId);
      return { tabIds: ids, activeTabId };
    })
    .filter((group) => group.tabIds.length > 0);

  const assigned = new Set(next.flatMap((group) => group.tabIds));
  const unassigned = tabIds.filter((id) => !assigned.has(id));
  if (unassigned.length > 0) {
    if (next.length === 0) {
      return singleGroup(tabIds, fallbackActiveId);
    }
    next = [
      {
        ...next[0]!,
        tabIds: [...next[0]!.tabIds, ...unassigned],
      },
      ...next.slice(1),
    ];
  }

  if (next.length === 0) {
    return singleGroup(tabIds, fallbackActiveId);
  }

  // The live active tab wins within the group that contains it: single-group
  // mode never calls activateInGroup, so without this the group's active id
  // goes stale after tab switches (splitting would then move the wrong tab),
  // and a newly created tab appended above would stay hidden behind the
  // group's previous active tab.
  next = next.map((group) =>
    group.tabIds.includes(fallbackActiveId) &&
    group.activeTabId !== fallbackActiveId
      ? { ...group, activeTabId: fallbackActiveId }
      : group,
  );

  return normalizeTabEditorGroups(next);
}

export function normalizeTabEditorGroups(
  groups: TabEditorGroup[],
): TabEditorGroup[] {
  const kept = groups.filter((group) => group.tabIds.length > 0);
  return kept.map((group) => ({
    tabIds: group.tabIds,
    activeTabId: group.tabIds.includes(group.activeTabId)
      ? group.activeTabId
      : group.tabIds[0]!,
  }));
}

type GroupDragPayload = {
  fromGroup: number;
  tabId: string;
};

export function useTabEditorGroups(
  tabIds: string[],
  activeTabId: string,
  workspaceId: string,
) {
  const tabIdsKey = tabIds.join("\0");
  const tabIdsRef = useRef(tabIds);
  tabIdsRef.current = tabIds;
  const [groups, setGroups] = useState<TabEditorGroup[]>(() =>
    singleGroup(tabIds, activeTabId),
  );
  const workspaceRef = useRef(workspaceId);
  const dragRef = useRef<GroupDragPayload | null>(null);

  useEffect(() => {
    const ids = tabIdsRef.current;
    if (workspaceRef.current !== workspaceId) {
      workspaceRef.current = workspaceId;
      setGroups(singleGroup(ids, activeTabId));
      return;
    }
    setGroups((prev) =>
      reconcileTabEditorGroups(
        prev,
        ids,
        activeTabId || ids[0] || "",
      ),
    );
  }, [workspaceId, tabIdsKey, activeTabId]);

  const splitActiveTab = useCallback(() => {
    setGroups((prev) => {
      if (prev.length > 1) return prev;
      const group = prev[0];
      if (!group || group.tabIds.length < 2) return prev;
      const moving = group.activeTabId;
      if (!group.tabIds.includes(moving)) return prev;
      const remaining = group.tabIds.filter((id) => id !== moving);
      const leftActive =
        remaining[Math.max(0, group.tabIds.indexOf(moving) - 1)] ??
        remaining[0]!;
      return [
        { tabIds: remaining, activeTabId: leftActive },
        { tabIds: [moving], activeTabId: moving },
      ];
    });
  }, []);

  const activateInGroup = useCallback((groupIndex: number, tabId: string) => {
    setGroups((prev) =>
      prev.map((group, index) =>
        index === groupIndex ? { ...group, activeTabId: tabId } : group,
      ),
    );
  }, []);

  const handleGroupDragStart = useCallback(
    (fromGroup: number, tabId: string) => {
      dragRef.current = { fromGroup, tabId };
    },
    [],
  );

  const handleGroupDragEnd = useCallback(() => {
    dragRef.current = null;
  }, []);

  const handleGroupDragOver = useCallback((event: DragEvent) => {
    event.preventDefault();
  }, []);

  const moveTabToGroup = useCallback((toGroup: number) => {
    const payload = dragRef.current;
    if (!payload) return;
    const { fromGroup, tabId } = payload;
    if (fromGroup === toGroup) return;

    setGroups((prev) =>
      normalizeTabEditorGroups(
        prev.map((group, index) => {
          if (index === fromGroup) {
            const tabIds = group.tabIds.filter((id) => id !== tabId);
            const activeTabId =
              group.activeTabId === tabId
                ? (tabIds[0] ?? group.activeTabId)
                : group.activeTabId;
            return { tabIds, activeTabId };
          }
          if (index === toGroup) {
            if (group.tabIds.includes(tabId)) {
              return { ...group, activeTabId: tabId };
            }
            return {
              tabIds: [...group.tabIds, tabId],
              activeTabId: tabId,
            };
          }
          return group;
        }),
      ),
    );
    dragRef.current = null;
    return tabId;
  }, []);

  const handleGroupDrop = useCallback(
    (toGroup: number) => moveTabToGroup(toGroup),
    [moveTabToGroup],
  );

  return {
    groups,
    isSplit: groups.length > 1,
    splitActiveTab,
    activateInGroup,
    handleGroupDragStart,
    handleGroupDragEnd,
    handleGroupDragOver,
    handleGroupDrop,
    moveTabToGroup,
  };
}
