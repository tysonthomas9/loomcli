import { useEffect, useMemo, useState, type MouseEvent } from "react";

import { useScopedFileTreeCore, useSkillsTree } from "@/hooks";
import type { FileBrowserTab } from "@/stores";
import {
  explorerRefKey,
  sameExplorerRef,
  type SkillsExplorerRef,
} from "@/utils/explorerRefs";

import {
  FileTree,
  type FileTreeInlineEdit,
  type FileTreeNodeInfo,
} from "../FileTree";
import { dirname } from "../fileExplorerLocalUtils";
import styles from "../FileExplorer.module.css";
import type {
  ScopedInlineEdit,
  TreeRefreshRequest,
  TreeRevealRequest,
} from "../workspaceFileBrowserTypes";

export function SkillsTreeBlock({
  workspaceId,
  refInfo,
  depthOffset,
  canEdit,
  selectedTab,
  inlineEdit,
  revealRequest,
  refreshRequest,
  onOpenFile,
  onContextMenu,
  onRequestDelete,
  onNewSkill,
  onInlineEditChange,
  onInlineEditCommit,
  onInlineEditCancel,
}: {
  workspaceId: string;
  refInfo: SkillsExplorerRef;
  depthOffset: number;
  canEdit: boolean;
  selectedTab: FileBrowserTab | null;
  inlineEdit: ScopedInlineEdit | null;
  revealRequest?: TreeRevealRequest | undefined;
  refreshRequest?: TreeRefreshRequest | undefined;
  onOpenFile: (ref: SkillsExplorerRef, path: string) => void;
  onContextMenu: (
    ref: SkillsExplorerRef,
    node: FileTreeNodeInfo,
    event: MouseEvent<HTMLDivElement>,
  ) => void;
  onRequestDelete: (ref: SkillsExplorerRef, node: FileTreeNodeInfo) => void;
  onNewSkill: (ref: SkillsExplorerRef) => void;
  onInlineEditChange: (value: string) => void;
  onInlineEditCommit: () => void;
  onInlineEditCancel: () => void;
}) {
  const catalog = useSkillsTree(workspaceId, refInfo.group);
  const tree = useScopedFileTreeCore(
    catalog.loader,
    catalog.status === "loaded",
    false,
  );
  const { loadDir, revealPath } = tree;
  const [scrollTarget, setScrollTarget] = useState<string | null>(null);
  const selectedPath =
    selectedTab && sameExplorerRef(selectedTab.ref, refInfo)
      ? selectedTab.path
      : null;
  const scopedInlineEdit: FileTreeInlineEdit | null =
    inlineEdit && sameExplorerRef(inlineEdit.ref, refInfo)
      ? inlineEdit.edit
      : null;
  const annotations = useMemo(() => {
    const next: Record<string, { label: string; tone?: "shadowed" | "info" }> =
      {};
    for (const skill of catalog.shadowed) {
      next[skill] = { label: "overridden", tone: "shadowed" };
    }
    for (const skill of catalog.shadows) {
      next[skill] = { label: "overrides workspace", tone: "info" };
    }
    return next;
  }, [catalog.shadowed, catalog.shadows]);

  useEffect(() => {
    if (!revealRequest) return;
    void revealPath(revealRequest.path).then(() =>
      setScrollTarget(revealRequest.path),
    );
  }, [revealPath, revealRequest]);

  useEffect(() => {
    if (!refreshRequest) return;
    const parents = new Set(refreshRequest.paths.map(dirname));
    void Promise.all([...parents].map((parent) => loadDir(parent)));
  }, [loadDir, refreshRequest]);

  if (catalog.status === "idle" || catalog.status === "loading") {
    return (
      <div
        className={styles.checkoutTreeState}
        style={{ paddingLeft: 8 + depthOffset * 16 }}
      >
        Loading skills...
      </div>
    );
  }
  if (catalog.status === "error" || tree.error) {
    return (
      <div
        className={styles.skillsTreeError}
        style={{ marginLeft: 8 + depthOffset * 16 }}
        role="alert"
      >
        <span>{catalog.error ?? tree.error}</span>
        <button type="button" onClick={() => void catalog.retry()}>
          Retry
        </button>
      </div>
    );
  }
  if (catalog.skills.length === 0) {
    const readOnly = refInfo.group.kind === "workspace";
    return (
      <div
        className={styles.skillsEmptyState}
        style={{ marginLeft: 8 + depthOffset * 16 }}
      >
        <span>No skills in this scope.</span>
        <button
          type="button"
          disabled={!canEdit}
          title={
            readOnly
              ? "Use `loom skill update` for workspace skills"
              : undefined
          }
          onClick={() => onNewSkill(refInfo)}
        >
          New skill
        </button>
      </div>
    );
  }

  return (
    <FileTree
      treeData={tree.treeData}
      expanded={tree.expanded}
      selectedPath={selectedPath}
      filterText={tree.debouncedFilterText}
      onToggle={tree.toggle}
      onSelectFile={(path) => {
        if (path) onOpenFile(refInfo, path);
      }}
      onContextMenuNode={(node, event) => onContextMenu(refInfo, node, event)}
      {...(canEdit
        ? {
            onRequestDelete: (node: FileTreeNodeInfo) =>
              onRequestDelete(refInfo, node),
          }
        : {})}
      inlineEdit={scopedInlineEdit}
      onInlineEditChange={onInlineEditChange}
      onInlineEditCommit={onInlineEditCommit}
      onInlineEditCancel={onInlineEditCancel}
      annotations={annotations}
      scrollToPath={scrollTarget}
      depthOffset={depthOffset}
      idPrefix={`ft-${explorerRefKey(refInfo).replace(/[^a-zA-Z0-9_-]/g, "-")}`}
    />
  );
}
