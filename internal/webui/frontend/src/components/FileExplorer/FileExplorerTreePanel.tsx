import {
  useEffect,
  useState,
  type KeyboardEvent,
  type MouseEvent,
} from "react";

import { useScopedFileTree } from "@/hooks";
import type { FileBrowserTab } from "@/stores";
import {
  checkoutLabel,
  checkoutRefKey,
  sameCheckoutRef,
  type CheckoutRef,
} from "@/utils/fileExplorerRefs";
import { getCompactAvatarInitials } from "@/utils/compactAvatarInitials";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

import {
  FileTree,
  type FileTreeInlineEdit,
  type FileTreeNodeInfo,
} from "./FileTree";
import type { ChangeCheckoutGroup } from "./changesLens";
import { dirname } from "./fileExplorerLocalUtils";
import styles from "./FileExplorer.module.css";
import type {
  ExplorerLens,
  OpenDiffRequest,
  ScopedInlineEdit,
  TreeRefreshRequest,
  TreeRevealRequest,
} from "./workspaceFileBrowserTypes";
import type { FileTreeRoot, FileTreeSection } from "./treeRoots";

function LensToggle({
  lens,
  changeCount,
  onChange,
}: {
  lens: ExplorerLens;
  changeCount: number;
  onChange: (lens: ExplorerLens) => void;
}) {
  const tabs: Array<{ id: ExplorerLens; label: string }> = [
    { id: "files", label: "Files" },
    { id: "changes", label: "Changes" },
  ];

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const currentIndex = tabs.findIndex((tab) => tab.id === lens);
    let nextIndex = -1;
    if (event.key === "ArrowRight" || event.key === "ArrowDown") {
      nextIndex = (currentIndex + 1) % tabs.length;
    } else if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
      nextIndex = (currentIndex - 1 + tabs.length) % tabs.length;
    }
    if (nextIndex >= 0) {
      event.preventDefault();
      onChange(tabs[nextIndex]!.id);
    }
  };

  return (
    <div
      className={styles.lensToggle}
      role="tablist"
      aria-label="File explorer lens"
      onKeyDown={handleKeyDown}
    >
      {tabs.map((tab) => {
        const active = lens === tab.id;
        return (
          <button
            key={tab.id}
            type="button"
            role="tab"
            className={styles.lensTab}
            data-active={active || undefined}
            aria-selected={active}
            aria-label={
              tab.id === "changes" ? `Changes ${changeCount}` : tab.label
            }
            tabIndex={active ? 0 : -1}
            onClick={() => onChange(tab.id)}
          >
            <span>{tab.label}</span>
            {tab.id === "changes" && (
              <span className={styles.lensBadge}>{changeCount}</span>
            )}
          </button>
        );
      })}
    </div>
  );
}

function ChangesList({
  groups,
  unavailableCount,
  onOpenDiff,
}: {
  groups: ChangeCheckoutGroup[];
  unavailableCount: number;
  onOpenDiff: (request: OpenDiffRequest) => void;
}) {
  if (groups.length === 0 && unavailableCount === 0) {
    return (
      <div className={styles.changesEmpty}>
        No uncommitted changes across this workspace.
      </div>
    );
  }

  return (
    <div className={styles.changesList} aria-label="Workspace changes">
      {unavailableCount > 0 && (
        <div className={styles.changesNotice} role="status">
          {unavailableCount === 1
            ? "1 checkout unavailable"
            : `${unavailableCount} checkouts unavailable`}
        </div>
      )}
      {groups.length === 0 && (
        <div className={styles.changesEmpty}>
          No uncommitted changes across this workspace.
        </div>
      )}
      {groups.map((group) => (
        <section key={group.id} className={styles.changesGroup}>
          <h2 className={styles.changesGroupHeader}>{group.label}</h2>
          {!group.loaded ? (
            <div className={styles.changesLoading}>Loading changes...</div>
          ) : group.items.length === 0 ? (
            <div className={styles.changesLoading}>No changed files found</div>
          ) : (
            group.items.map((item) => (
              <button
                type="button"
                key={item.path}
                className={styles.changeRow}
                aria-label={`Open diff for ${item.path} (${item.status.label})`}
                onClick={() =>
                  onOpenDiff({
                    ref: group.ref,
                    path: item.path,
                    from: "HEAD",
                    title: checkoutLabel(group.ref),
                    canOpenFile: item.status.kind !== "deleted",
                  })
                }
              >
                <span className={styles.changePath}>
                  <span className={styles.changeName}>{item.name}</span>
                  {item.parentPath && (
                    <span className={styles.changeParent}>
                      {item.parentPath}
                    </span>
                  )}
                </span>
                <span
                  className={styles.changeStatusChip}
                  data-status={item.status.kind}
                >
                  {item.status.label}
                </span>
              </button>
            ))
          )}
        </section>
      ))}
    </div>
  );
}

function ChangeBadge({ count }: { count: number }): JSX.Element | null {
  if (count <= 0) return null;
  return <span className={styles.checkoutBadge}>{count}</span>;
}

function AgentAvatar({ name }: { name: string }): JSX.Element {
  const bg = getAvatarColor(name);
  return (
    <span
      className={styles.agentAvatar}
      style={{
        background: bg,
        color: shouldUseWhiteText(bg) ? "#fff" : "#1a1a1a",
      }}
      aria-hidden="true"
    >
      {getCompactAvatarInitials(name)}
    </span>
  );
}

function RootIcon({ icon }: { icon: "agent" | "repo" | "workspace" }) {
  if (icon === "agent") return null;
  return (
    <span className={styles.rootIcon} aria-hidden="true">
      {icon === "workspace" ? (
        <svg viewBox="0 0 16 16">
          <path
            d="M2.5 4.5h11v8h-11zM4 3h8"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.3"
            strokeLinejoin="round"
          />
        </svg>
      ) : (
        <svg viewBox="0 0 16 16">
          <path
            d="M2 3h4l2 2h6v8H2V3z"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.3"
            strokeLinejoin="round"
          />
        </svg>
      )}
    </span>
  );
}

function RootRow({
  root,
  expanded,
  depth = 0,
  onToggle,
}: {
  root: FileTreeRoot;
  expanded: boolean;
  depth?: number | undefined;
  onToggle: () => void;
}) {
  const isAgent = root.kind === "agent";
  const label = root.label;
  const secondary = root.secondary;
  const exists = root.exists;
  const icon = isAgent ? "agent" : root.icon;
  const disabledTitle = exists ? undefined : "not checked out on this machine";
  return (
    <button
      type="button"
      className={styles.rootRow}
      data-dimmed={root.kind === "checkout" && root.dimmed ? true : undefined}
      data-disabled={!exists || undefined}
      style={{ paddingLeft: 8 + depth * 16 }}
      disabled={!exists}
      title={disabledTitle}
      onClick={onToggle}
    >
      <span
        className={`${styles.chevron} ${expanded ? styles.chevronExpanded : ""}`}
        aria-hidden="true"
      >
        <svg viewBox="0 0 16 16">
          <path
            d="M6 4l4 4-4 4"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
          />
        </svg>
      </span>
      {isAgent ? (
        <AgentAvatar name={root.agentName} />
      ) : (
        <RootIcon icon={icon} />
      )}
      <span className={styles.rootLabel}>{label}</span>
      {secondary && <span className={styles.rootSecondary}>· {secondary}</span>}
      <ChangeBadge count={root.changeCount} />
    </button>
  );
}

function sameRefInlineEdit(
  inlineEdit: ScopedInlineEdit | null,
  ref: CheckoutRef,
): FileTreeInlineEdit | null {
  return inlineEdit && sameCheckoutRef(inlineEdit.ref, ref)
    ? inlineEdit.edit
    : null;
}

function CheckoutTreeBlock({
  refInfo,
  depthOffset,
  selectedTab,
  inlineEdit,
  gitStatus,
  revealRequest,
  refreshRequest,
  onOpenFile,
  onContextMenu,
  onRequestRename,
  onRequestDelete,
  onInlineEditChange,
  onInlineEditCommit,
  onInlineEditCancel,
}: {
  refInfo: CheckoutRef;
  depthOffset: number;
  selectedTab: FileBrowserTab | null;
  inlineEdit: ScopedInlineEdit | null;
  gitStatus: Record<string, string>;
  revealRequest?: TreeRevealRequest | undefined;
  refreshRequest?: TreeRefreshRequest | undefined;
  onOpenFile: (ref: CheckoutRef, path: string) => void;
  onContextMenu: (
    ref: CheckoutRef,
    node: FileTreeNodeInfo,
    event: MouseEvent<HTMLDivElement>,
  ) => void;
  onRequestRename: (ref: CheckoutRef, node: FileTreeNodeInfo) => void;
  onRequestDelete: (ref: CheckoutRef, node: FileTreeNodeInfo) => void;
  onInlineEditChange: (value: string) => void;
  onInlineEditCommit: () => void;
  onInlineEditCancel: () => void;
}) {
  const {
    expanded,
    treeData,
    isLoading,
    error,
    debouncedFilterText,
    toggle,
    loadDir,
    revealPath,
  } = useScopedFileTree(refInfo);
  const [scrollTarget, setScrollTarget] = useState<string | null>(null);
  const selectedPath =
    selectedTab && sameCheckoutRef(selectedTab.ref, refInfo)
      ? selectedTab.path
      : null;

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

  if (isLoading) {
    return (
      <div
        className={styles.checkoutTreeState}
        style={{ paddingLeft: 8 + depthOffset * 16 }}
      >
        Loading...
      </div>
    );
  }
  if (error) {
    return (
      <div
        className={styles.checkoutTreeError}
        style={{ paddingLeft: 8 + depthOffset * 16 }}
      >
        {error}
      </div>
    );
  }

  return (
    <FileTree
      treeData={treeData}
      expanded={expanded}
      selectedPath={selectedPath}
      filterText={debouncedFilterText}
      onToggle={toggle}
      onSelectFile={(path) => {
        if (path) onOpenFile(refInfo, path);
      }}
      onContextMenuNode={(node, event) => onContextMenu(refInfo, node, event)}
      onRequestRename={(node) => onRequestRename(refInfo, node)}
      onRequestDelete={(node) => onRequestDelete(refInfo, node)}
      inlineEdit={sameRefInlineEdit(inlineEdit, refInfo)}
      onInlineEditChange={onInlineEditChange}
      onInlineEditCommit={onInlineEditCommit}
      onInlineEditCancel={onInlineEditCancel}
      gitStatus={gitStatus}
      scrollToPath={scrollTarget}
      depthOffset={depthOffset}
      idPrefix={`ft-${checkoutRefKey(refInfo).replace(/[^a-zA-Z0-9_-]/g, "-")}`}
    />
  );
}

export function FileExplorerTreePanel({
  lens,
  changeCount,
  checkoutError,
  sections,
  changeGroups,
  unavailableCheckoutCount,
  expandedRoots,
  selectedTab,
  inlineEdit,
  gitStatusByRef,
  treeRevealRequests,
  treeRefreshRequests,
  hideAgentSectionHeading,
  onLensChange,
  onQuickOpen,
  onOpenDiff,
  onToggleRoot,
  onOpenFile,
  onContextMenu,
  onRequestRename,
  onRequestDelete,
  onInlineEditChange,
  onInlineEditCommit,
  onInlineEditCancel,
}: {
  lens: ExplorerLens;
  changeCount: number;
  checkoutError: string | null;
  sections: FileTreeSection[];
  changeGroups: ChangeCheckoutGroup[];
  unavailableCheckoutCount: number;
  expandedRoots: Set<string>;
  selectedTab: FileBrowserTab | null;
  inlineEdit: ScopedInlineEdit | null;
  gitStatusByRef: Record<string, Record<string, string>>;
  treeRevealRequests: Record<string, TreeRevealRequest>;
  treeRefreshRequests: Record<string, TreeRefreshRequest>;
  hideAgentSectionHeading: boolean;
  onLensChange: (lens: ExplorerLens) => void;
  onQuickOpen: () => void;
  onOpenDiff: (request: OpenDiffRequest) => void;
  onToggleRoot: (key: string) => void;
  onOpenFile: (ref: CheckoutRef, path: string) => void;
  onContextMenu: (
    ref: CheckoutRef,
    node: FileTreeNodeInfo,
    event: MouseEvent<HTMLDivElement>,
  ) => void;
  onRequestRename: (ref: CheckoutRef, node: FileTreeNodeInfo) => void;
  onRequestDelete: (ref: CheckoutRef, node: FileTreeNodeInfo) => void;
  onInlineEditChange: (value: string) => void;
  onInlineEditCommit: () => void;
  onInlineEditCancel: () => void;
}) {
  const renderCheckoutRoot = (
    root: Extract<FileTreeRoot, { kind: "checkout" }>,
    depth: number,
  ) => {
    const key = checkoutRefKey(root.ref);
    const expanded = expandedRoots.has(key);
    return (
      <div key={root.id}>
        <RootRow
          root={root}
          depth={depth}
          expanded={expanded}
          onToggle={() => onToggleRoot(key)}
        />
        {expanded && root.exists && (
          <CheckoutTreeBlock
            refInfo={root.ref}
            depthOffset={depth + 1}
            selectedTab={selectedTab}
            inlineEdit={inlineEdit}
            gitStatus={gitStatusByRef[key] ?? {}}
            revealRequest={treeRevealRequests[key]}
            refreshRequest={treeRefreshRequests[key]}
            onOpenFile={onOpenFile}
            onContextMenu={onContextMenu}
            onRequestRename={onRequestRename}
            onRequestDelete={onRequestDelete}
            onInlineEditChange={onInlineEditChange}
            onInlineEditCommit={onInlineEditCommit}
            onInlineEditCancel={onInlineEditCancel}
          />
        )}
      </div>
    );
  };

  const renderRoot = (root: FileTreeRoot) => {
    if (root.kind === "checkout") return renderCheckoutRoot(root, 0);
    if (root.flattenedRef) {
      const key = checkoutRefKey(root.flattenedRef);
      const expanded = expandedRoots.has(key);
      return (
        <div key={root.id}>
          <RootRow
            root={root}
            expanded={expanded}
            onToggle={() => onToggleRoot(key)}
          />
          {expanded && root.exists && (
            <CheckoutTreeBlock
              refInfo={root.flattenedRef}
              depthOffset={1}
              selectedTab={selectedTab}
              inlineEdit={inlineEdit}
              gitStatus={gitStatusByRef[key] ?? {}}
              revealRequest={treeRevealRequests[key]}
              refreshRequest={treeRefreshRequests[key]}
              onOpenFile={onOpenFile}
              onContextMenu={onContextMenu}
              onRequestRename={onRequestRename}
              onRequestDelete={onRequestDelete}
              onInlineEditChange={onInlineEditChange}
              onInlineEditCommit={onInlineEditCommit}
              onInlineEditCancel={onInlineEditCancel}
            />
          )}
        </div>
      );
    }
    const expanded = expandedRoots.has(root.id);
    return (
      <div key={root.id}>
        <RootRow
          root={root}
          expanded={expanded}
          onToggle={() => onToggleRoot(root.id)}
        />
        {expanded && root.children.map((child) => renderCheckoutRoot(child, 1))}
      </div>
    );
  };

  const renderWorkspaceSection = (
    section: FileTreeSection,
    root: Extract<FileTreeRoot, { kind: "checkout" }>,
  ) => {
    const key = checkoutRefKey(root.ref);
    const expanded = expandedRoots.has(key);
    return (
      <section
        key={section.id}
        className={styles.rootSection}
        data-dimmed={section.dimmed || undefined}
      >
        <h2 className={styles.rootSectionHeading}>
          <button
            type="button"
            className={styles.workspaceSectionToggle}
            aria-expanded={expanded}
            onClick={() => onToggleRoot(key)}
          >
            <span
              className={`${styles.chevron} ${expanded ? styles.chevronExpanded : ""}`}
              aria-hidden="true"
            >
              <svg viewBox="0 0 16 16">
                <path
                  d="M6 4l4 4-4 4"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.5"
                />
              </svg>
            </span>
            <span>{section.title}</span>
            {root.changeCount > 0 && (
              <span className={styles.checkoutBadge}>{root.changeCount}</span>
            )}
          </button>
        </h2>
        {expanded && root.exists && (
          <CheckoutTreeBlock
            refInfo={root.ref}
            depthOffset={0}
            selectedTab={selectedTab}
            inlineEdit={inlineEdit}
            gitStatus={gitStatusByRef[key] ?? {}}
            revealRequest={treeRevealRequests[key]}
            refreshRequest={treeRefreshRequests[key]}
            onOpenFile={onOpenFile}
            onContextMenu={onContextMenu}
            onRequestRename={onRequestRename}
            onRequestDelete={onRequestDelete}
            onInlineEditChange={onInlineEditChange}
            onInlineEditCommit={onInlineEditCommit}
            onInlineEditCancel={onInlineEditCancel}
          />
        )}
      </section>
    );
  };

  return (
    <>
      <div className={styles.toolbar}>
        <LensToggle
          lens={lens}
          changeCount={changeCount}
          onChange={onLensChange}
        />
        <button
          type="button"
          className={styles.quickOpenButton}
          onClick={onQuickOpen}
        >
          <svg viewBox="0 0 16 16" aria-hidden="true">
            <circle
              cx="7"
              cy="7"
              r="4"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.4"
            />
            <path
              d="M10.5 10.5L14 14"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.4"
              strokeLinecap="round"
            />
          </svg>
          <span>Go to file...</span>
          <kbd>Cmd+P</kbd>
        </button>
      </div>
      <div className={styles.treeScroll}>
        {checkoutError && (
          <div className={styles.checkoutError}>{checkoutError}</div>
        )}
        {lens === "changes" ? (
          <ChangesList
            groups={changeGroups}
            unavailableCount={unavailableCheckoutCount}
            onOpenDiff={onOpenDiff}
          />
        ) : (
          sections.map((section) => {
            const workspaceRoot =
              section.id === "workspace"
                ? section.roots.find(
                    (
                      root,
                    ): root is Extract<FileTreeRoot, { kind: "checkout" }> =>
                      root.kind === "checkout" &&
                      root.ref.scope === "workspace",
                  )
                : undefined;
            if (workspaceRoot) {
              return renderWorkspaceSection(section, workspaceRoot);
            }
            if (hideAgentSectionHeading && section.id === "agents") {
              return (
                <div key={section.id} className={styles.rootSection}>
                  {section.roots.length === 0 ? (
                    <div className={styles.rootSectionEmpty}>None</div>
                  ) : (
                    section.roots.map(renderRoot)
                  )}
                </div>
              );
            }
            return (
              <section
                key={section.id}
                className={styles.rootSection}
                data-dimmed={section.dimmed || undefined}
              >
                <h2 className={styles.rootSectionHeading}>{section.title}</h2>
                {section.roots.length === 0 ? (
                  <div className={styles.rootSectionEmpty}>None</div>
                ) : (
                  section.roots.map(renderRoot)
                )}
              </section>
            );
          })
        )}
      </div>
    </>
  );
}
