import { useCallback, useEffect, useMemo, useSyncExternalStore } from "react";

import type { SkillsScopeGroup } from "@/api/workspace";
import { skillsStore } from "@/stores/skillsStore";
import { explorerRefKey, type SkillsExplorerRef } from "@/utils/explorerRefs";

export function useSkillsCatalog(workspaceId: string) {
  const snapshot = useSyncExternalStore(
    skillsStore.subscribe,
    () => skillsStore.catalog(workspaceId),
    () => skillsStore.catalog(workspaceId),
  );

  useEffect(() => {
    if (snapshot.status === "idle") void skillsStore.loadCatalog(workspaceId);
  }, [snapshot.revision, snapshot.status, workspaceId]);

  const retry = useCallback(
    () => skillsStore.loadCatalog(workspaceId, true),
    [workspaceId],
  );
  const invalidate = useCallback(
    () => skillsStore.invalidate(workspaceId),
    [workspaceId],
  );
  return { ...snapshot, retry, invalidate };
}

export function useSkillsTree(workspaceId: string, group: SkillsScopeGroup) {
  const catalog = useSkillsCatalog(workspaceId);
  const groupKind = group.kind;
  const role = group.kind === "role" ? group.role : null;
  const stableGroup = useMemo<SkillsScopeGroup>(
    () =>
      groupKind === "workspace"
        ? { kind: "workspace" }
        : { kind: "role", role: role ?? "" },
    [groupKind, role],
  );
  const loader = useMemo(
    () => skillsStore.loader(workspaceId, stableGroup),
    [stableGroup, workspaceId],
  );
  const ref: SkillsExplorerRef = useMemo(
    () => ({ kind: "skills", group: stableGroup }),
    [stableGroup],
  );
  const key = explorerRefKey(ref);
  return {
    ...catalog,
    loader,
    skills: skillsStore.skills(stableGroup, workspaceId),
    shadowed: catalog.shadowedByRef[key] ?? new Set<string>(),
    shadows: catalog.shadowsByRef[key] ?? new Set<string>(),
  };
}

export function useSkill(
  workspaceId: string,
  ref: SkillsExplorerRef | null,
  name: string | null,
) {
  const catalog = useSkillsCatalog(workspaceId);
  return {
    ...catalog,
    skill: ref && name ? skillsStore.skill(workspaceId, ref.group, name) : null,
  };
}

export function useSkillCapabilities(workspaceId: string) {
  const snapshot = useSyncExternalStore(
    skillsStore.subscribe,
    () => skillsStore.capability(workspaceId),
    () => skillsStore.capability(workspaceId),
  );
  useEffect(() => {
    if (snapshot.status === "idle") {
      void skillsStore.loadCapabilities(workspaceId);
    }
  }, [snapshot.status, workspaceId]);
  const retry = useCallback(
    () => skillsStore.loadCapabilities(workspaceId, true),
    [workspaceId],
  );
  return { ...snapshot, retry };
}

export function useSkillsActions(workspaceId: string) {
  // Both subscriptions discard their snapshot on purpose. This hook returns
  // callbacks, not data, but canEdit reads catalog and capability state at call
  // time — so a consumer that never re-rendered after a load would keep asking
  // a stale question. Subscribing here is what re-renders it.
  //
  // Deliberately the raw store rather than useSkillsCatalog/useSkillCapabilities:
  // those also trigger the load, and this hook is mounted in places that only
  // want to act on skills, not fetch them. Swapping them in would add fetches
  // wherever it is used without a sibling already loading.
  useSyncExternalStore(
    skillsStore.subscribe,
    () => skillsStore.catalog(workspaceId),
    () => skillsStore.catalog(workspaceId),
  );
  useSyncExternalStore(
    skillsStore.subscribe,
    () => skillsStore.capability(workspaceId),
    () => skillsStore.capability(workspaceId),
  );
  return useMemo(
    () => ({
      canEdit: (group: SkillsScopeGroup) =>
        skillsStore.canEdit(workspaceId, group),
      createSkill: (
        group: SkillsScopeGroup,
        input: { name: string; description: string; content?: string },
      ) => skillsStore.createSkill(workspaceId, group, input),
      updateMetadata: (
        group: SkillsScopeGroup,
        name: string,
        description: string,
      ) => skillsStore.updateMetadata(workspaceId, group, name, description),
      deleteSkill: (group: SkillsScopeGroup, name: string) =>
        skillsStore.deleteSkill(workspaceId, group, name),
      createFile: (group: SkillsScopeGroup, name: string, path: string) =>
        skillsStore.createFile(workspaceId, group, name, path),
      deleteFile: (
        group: SkillsScopeGroup,
        name: string,
        path: string,
        ifMatch?: string,
      ) => skillsStore.deleteFile(workspaceId, group, name, path, ifMatch),
      invalidate: () => skillsStore.invalidate(workspaceId),
      listIndexPaths: (group: SkillsScopeGroup) =>
        skillsStore.listIndexPaths(workspaceId, group),
    }),
    [workspaceId],
  );
}
